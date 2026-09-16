//go:build esp32

/* Controller start for the classic ESP32. See bt_ble.h.
 * The ESP32-C3 and the ESP32-S3 use a different sequence, in
 * bt_ble_init_c3s3.c. The blob and the ROM are not the same on this chip.
 * The order follows ESP-IDF v5.5 components/bt/controller/esp32/bt.c,
 * esp_bt_controller_init and esp_bt_controller_enable. */

#include "espradio.h"

/* ESP-IDF sets this from the BR/EDR synchronous connection count, which the
 * vendored headers do not carry. BLE only, thus there are none. */
#ifndef UT_BR_EDR_CTRL_MAX_SYNC_CONN_EFF
#define UT_BR_EDR_CTRL_MAX_SYNC_CONN_EFF 0
#endif

/* ESP-IDF takes this from FreeRTOS. The default gives a controller task
 * priority of 23. espradio runs the task as a goroutine and ignores the
 * priority, but the value is part of the config structure.
 * See ESP-IDF components/freertos/Kconfig, CONFIG_FREERTOS_MAX_TASK_PRIORITY. */
#ifndef configMAX_PRIORITIES
#define configMAX_PRIORITIES 25
#endif

#include "esp_task.h"
#include "esp_bt.h"
#include "bt_ble.h"
#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>

#ifndef ESPRADIO_BLE_DEBUG
#define ESPRADIO_BLE_DEBUG 0
#endif

#if ESPRADIO_BLE_DEBUG
#define BLE_DBG(...) printf(__VA_ARGS__)
#else
#define BLE_DBG(...) ((void)0)
#endif

/* The linker script tests this symbol. When the Bluetooth driver is linked in,
 * targets/esp32.ld keeps 0x3FFB0000 to 0x3FFC0000 free for the controller.
 * --gc-sections removes the symbol again when no program calls BLEInit(), and
 * then the memory goes back to the Go heap. The start code below reads it, so
 * that the symbol stays exactly when the driver stays. */
volatile uint32_t _btdm_mem_needed = 1;

/* ROM and blob entry points. btdm_controller_init takes a config mask on this
 * chip, which the ESP32-C3 and the ESP32-S3 do not. */
extern int  btdm_controller_init(uint32_t config_mask, void *config_opts);
extern int  btdm_controller_enable(uint32_t mode);
extern void btdm_controller_enable_sleep(bool enable);
extern void btdm_controller_set_sleep_mode(uint8_t mode);
extern void sdk_config_set_bt_pll_track_enable(bool enable);
extern void btdm_rf_bb_init_phase2(void);
extern void r_rwip_prevent_sleep_set(uint32_t prv_slp_bit);
extern void ets_delay_us(uint32_t us);
extern void ets_update_cpu_frequency_rom(uint32_t ticks_per_us);
extern void esp_phy_modem_init(void);
extern void esp_wifi_bt_power_domain_on(void);
extern void phy_set_wifi_mode_only(bool wifi_only);
extern uint32_t g_bt_plf_log_level;

/* The controller .data image in ROM and the areas that must be cleared.
 * See ESP-IDF bt.c btdm_controller_mem_init and btdm_dram_available_region. */
extern uint32_t _data_start_btdm;
extern uint32_t _data_end_btdm;
extern uint32_t _data_start_btdm_rom;

#define ESP_BT_MODE_BLE    1

/* No HCI UART, the controller is not on the APP CPU. See ESP-IDF bt.c
 * btdm_config_mask_load. */
#define BTDM_CFG_SCAN_DUPLICATE_OPTIONS   (1u << 3)
#define BTDM_CFG_SEND_ADV_RESERVED_SIZE   (1u << 4)
#define BTDM_CFG_BLE_FULL_SCAN_SUPPORTED  (1u << 5)
#define BT_CFG_MASK_ESP32 (BTDM_CFG_SCAN_DUPLICATE_OPTIONS | \
                           BTDM_CFG_SEND_ADV_RESERVED_SIZE | \
                           BTDM_CFG_BLE_FULL_SCAN_SUPPORTED)

/* DPORT holds the modem clock and reset on this chip, not APB_CTRL.
 * See ESP-IDF components/soc/esp32/register/soc/dport_reg.h. */
#define DPORT_WIFI_CLK_EN_REG  (*(volatile uint32_t *)0x3FF000CCu)
#define DPORT_CORE_RST_EN_REG  (*(volatile uint32_t *)0x3FF000D0u)

/* DPORT_WIFI_CLK_BT_EN_M is 0x61 << 11. The reset mask is BTBB, BTMAC,
 * RW_BTMAC and RW_BTLP. See ESP-IDF hal/esp32/clk_gate_ll.h PERIPH_BT_MODULE. */
#define BT_CLK_EN_BITS   (0x61u << 11)
#define BT_RST_BITS      ((1u << 3) | (1u << 4) | (1u << 9) | (1u << 10))

void espradio_bt_chip_clocks_up(void) {
    /* Keep the marker, and thus the reserved memory. See _btdm_mem_needed. */
    if (_btdm_mem_needed == 0) {
        return;
    }
#if ESPRADIO_BLE_DEBUG
    g_bt_plf_log_level = 10;
#endif
    BLE_DBG("  clk before: clk=0x%08lx rst=0x%08lx\n",
            (unsigned long)DPORT_WIFI_CLK_EN_REG,
            (unsigned long)DPORT_CORE_RST_EN_REG);

    /* Clock first, then release the reset, so the blocks leave reset with a
     * running clock. See the same note in bt_ble_init_c3s3.c. */
    DPORT_WIFI_CLK_EN_REG |= BT_CLK_EN_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(50);

    DPORT_CORE_RST_EN_REG |= BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(10);
    DPORT_CORE_RST_EN_REG &= ~BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(50);

    /* TinyGo raises the CPU clock without telling the ROM, which leaves ROM
     * ets_delay_us() too short and makes the RF calibration fail. */
    ets_update_cpu_frequency_rom(espradio_bt_cpu_ticks_per_us());

    BLE_DBG("  clk after:  clk=0x%08lx rst=0x%08lx\n",
            (unsigned long)DPORT_WIFI_CLK_EN_REG,
            (unsigned long)DPORT_CORE_RST_EN_REG);
}

/* Copy the controller .data image from ROM and clear the areas that the
 * hardware uses. BLE only, thus the BR/EDR area stays untouched.
 * See ESP-IDF bt.c btdm_controller_mem_init. */
static void btdm_controller_mem_init(void) {
    size_t len = (size_t)((uint8_t *)&_data_end_btdm - (uint8_t *)&_data_start_btdm);
    memcpy(&_data_start_btdm, (void *)_data_start_btdm_rom, len);

    memset((void *)SOC_MEM_BT_EM_BTDM0_START, 0,
           SOC_MEM_BT_EM_BTDM0_END - SOC_MEM_BT_EM_BTDM0_START);
    memset((void *)SOC_MEM_BT_EM_BLE_START, 0,
           SOC_MEM_BT_EM_BLE_END - SOC_MEM_BT_EM_BLE_START);
    memset((void *)SOC_MEM_BT_EM_BTDM1_START, 0,
           SOC_MEM_BT_EM_BTDM1_END - SOC_MEM_BT_EM_BTDM1_START);
    memset((void *)SOC_MEM_BT_BSS_START, 0,
           SOC_MEM_BT_BSS_END - SOC_MEM_BT_BSS_START);
    memset((void *)SOC_MEM_BT_MISC_START, 0,
           SOC_MEM_BT_MISC_END - SOC_MEM_BT_MISC_START);

    BLE_DBG("  mem_init: .data %u bytes from 0x%08lx\n",
            (unsigned)len, (unsigned long)_data_start_btdm_rom);
}

int espradio_bt_chip_controller_bringup(void) {
    esp_phy_modem_init();
    esp_wifi_bt_power_domain_on();
    btdm_controller_mem_init();

    /* Sleep stays off. ESP-IDF uses only these calls on this chip. The blob
     * keeps rw_sleep_enable local, so it cannot be written from here as the
     * ESP32-C3 and the ESP32-S3 do. */
    btdm_controller_set_sleep_mode(0 /* BTDM_MODEM_SLEEP_MODE_NONE */);

    /* The vendored sdkconfig.h selects BLE only, so the default config is
     * correct except for hli. Level 4 interrupts need the ESP-IDF high level
     * assembly, which espradio does not have. */
    esp_bt_controller_config_t cfg = BT_CONTROLLER_INIT_CONFIG_DEFAULT();
    cfg.mode = ESP_BT_MODE_BLE;
    cfg.hli = false;

    int res = btdm_controller_init(BT_CFG_MASK_ESP32, &cfg);
    if (res != 0) {
        BLE_DBG("  btdm_controller_init FAILED: %d\n", res);
        return -1;
    }
    BLE_DBG("  controller_init done\n");

    /* The PHY comes up after the controller on this chip, which is the
     * opposite of the ESP32-C3. See ESP-IDF esp_bt_controller_enable. */
    esp_phy_enable(PHY_MODEM_BT);
    phy_set_wifi_mode_only(false);
    sdk_config_set_bt_pll_track_enable(true);
    btdm_rf_bb_init_phase2();

    res = btdm_controller_enable(ESP_BT_MODE_BLE);
    if (res != 0) {
        BLE_DBG("  btdm_controller_enable FAILED: %d\n", res);
        return -2;
    }
    BLE_DBG("  controller enabled (BLE)\n");

    /* Let the controller task run, as a preemptive scheduler would. */
    for (int i = 0; i < 50; i++) {
        espradio_task_yield_go();
    }

    btdm_controller_enable_sleep(false);
    r_rwip_prevent_sleep_set(0x0F);
    return 0;
}

/* The controller task blocks on its queue, and espradio_queue_recv in osi.c
 * already gives up after 10 ms and yields. Nothing more is needed. */
int espradio_bt_chip_wake_task(void) { return 1; }

/* r_ke_task_schedule is not in the ESP32 ROM. r_ke_event_schedule already
 * dispatches the messages, thus nothing is lost. */
void espradio_bt_chip_ke_task_schedule(void) {}
