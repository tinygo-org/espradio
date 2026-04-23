//go:build esp32

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "../blobs/include/include.h"
#include "esp_phy.h"

/* ROM printf only — never libprintf.a (varargs broken from Clang). */
extern int ets_printf(const char *fmt, ...);

#define PHY_ADAPTER_DBG(...) ets_printf(__VA_ARGS__)

extern void phy_wifi_enable_set(uint8_t enable);
extern void *g_phyFuns;
extern int register_chipv7_phy(const esp_phy_init_data_t *init_data,
                               esp_phy_calibration_data_t *cal_data,
                               esp_phy_calibration_mode_t cal_mode);
extern void phy_wakeup_init(void);
extern void phy_close_rf(void);
extern void phy_init_flag(void);
extern void phy_init_param_set(uint8_t param);
extern void rom_phy_ant_init(void);
extern esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void));

/* On ESP32 the ROM provides phy_get_romfuncs() which returns a pointer to
 * the PHY function table. Unlike ESP32-S3, we don't need to manually
 * restore the table — the ROM's .data section is not in application DRAM
 * on the original ESP32, so TinyGo's BSS clearing doesn't touch it. */
extern uint32_t *phy_get_romfuncs(void);

/* PHY critical section — spinlock-based, matching esp-hal approach.
 * The blob calls phy_enter_critical/phy_exit_critical (U symbols in libphy.a)
 * to protect PHY register access.  We use Xtensa RSIL/WSR.PS to
 * disable/restore interrupts, matching esp-hal's single-core approach. */

uint32_t phy_enter_critical(void) {
    uint32_t old;
    __asm__ __volatile__("rsil %0, 3" : "=r"(old));
    return old;
}

void phy_exit_critical(uint32_t level) {
    __asm__ __volatile__("wsr.ps %0; rsync" :: "r"(level));
}

static uint32_t s_phy_i2c_saved_ps;

void phy_i2c_enter_critical(void) {
    s_phy_i2c_saved_ps = phy_enter_critical();
}

void phy_i2c_exit_critical(void) {
    phy_exit_critical(s_phy_i2c_saved_ps);
}

/* esp_dport_access_reg_read is needed by libphy.a on ESP32 for safe
 * DPORT register access. On single-core operation (our case), a direct
 * read is sufficient. */
uint32_t esp_dport_access_reg_read(uint32_t reg) {
    return *(volatile uint32_t *)reg;
}

/* ESP32's libphy.a references rtc_get_xtal (an S3 name).  On ESP32 the
 * ROM provides ets_get_detected_xtal_freq() which returns MHz. */
extern uint32_t ets_get_detected_xtal_freq(void);
uint32_t rtc_get_xtal(void) {
    return ets_get_detected_xtal_freq() / 1000000u;
}

/* Weak stubs for NVS-based PHY calibration and deep sleep hook registration. */
__attribute__((weak)) esp_err_t esp_phy_load_cal_data_from_nvs(esp_phy_calibration_data_t *out_cal_data) {
    (void)out_cal_data;
    return ESP_ERR_NOT_FOUND;
}

__attribute__((weak)) esp_err_t esp_phy_store_cal_data_to_nvs(const esp_phy_calibration_data_t *cal_data) {
    (void)cal_data;
    return ESP_OK;
}

__attribute__((weak)) esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void)) {
    (void)hook;
    return ESP_OK;
}

extern void espradio_hal_init_clocks_go(void);
extern void espradio_hal_disable_clocks_go(void);

static volatile uint32_t s_wifi_bt_pd_lock;

void esp_wifi_bt_power_domain_on(void) {
    while (__sync_lock_test_and_set(&s_wifi_bt_pd_lock, 1U)) {}
    espradio_hal_init_clocks_go();
    __sync_lock_release(&s_wifi_bt_pd_lock);
}

void esp_wifi_bt_power_domain_off(void) {
    while (__sync_lock_test_and_set(&s_wifi_bt_pd_lock, 1U)) {}
    espradio_hal_disable_clocks_go();
    __sync_lock_release(&s_wifi_bt_pd_lock);
}

extern uint8_t phy_dig_reg_backup(bool backup_en, uint32_t *mem_addr);
void *heap_caps_malloc(size_t size, uint32_t caps);
extern int rtc_get_reset_reason(int cpu_no);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
static uint8_t s_is_phy_calibrated;
static uint8_t s_phy_modem_init_ref;
static esp_phy_calibration_data_t s_phy_cal_data;
static volatile uint32_t s_phy_spin_lock;
static uint16_t s_phy_modem_flags_local;
static uint8_t s_phy_ant_need_update_local = 1u;
static uint32_t *s_phy_digital_regs_mem_ptr;
static uint8_t s_phy_is_digital_regs_stored_local;
static esp_phy_ant_config_t s_phy_ant_config_local = {
    .rx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .rx_ant_default = ESP_PHY_ANT_ANT0,
    .tx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .enabled_ant0 = 0,
    .enabled_ant1 = 1,
};

/* Static PHY init data blob (128 bytes for ESP32).
 * Values from esp-hal PHY_INIT_DATA_DEFAULT with CONFIG_ESP32_PHY_MAX_TX_POWER=20.
 * This is DIFFERENT from ESP32-S3/C3 — ESP32 has its own calibration layout. */
static const esp_phy_init_data_t phy_init_data = { .params = {
    0x03, 0x03, 0x05, 0x09, 0x06, 0x05, 0x03, 0x06,   /*   0 -   7 */
    0x05, 0x04, 0x06, 0x04, 0x05, 0x00, 0x00, 0x00,   /*   8 -  15 */
    0x00, 0x05, 0x09, 0x06, 0x05, 0x03, 0x06, 0x05,   /*  16 -  23 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  24 -  31 */
    0xfc, 0xfc, 0xfe, 0xf0, 0xf0, 0xf0, 0xe0, 0xe0,   /*  32 -  39 */
    0xe0, 0x18, 0x18, 0x18, 0x50, 0x48, 0x42, 0x3c,    /*  40 -  47: TX power limits */
    0x38, 0x34, 0x00, 0x01, 0x01, 0x02, 0x02, 0x03,    /*  48 -  55 */
    0x04, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  56 -  63 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  64 -  71 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  72 -  79 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  80 -  87 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  88 -  95 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  96 - 103 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /* 104 - 111 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /* 112 - 119 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /* 120 - 127 */
} };

void esp_phy_load_cal_and_init(void) {
    const esp_phy_init_data_t *init_data = &phy_init_data;
    if (init_data == NULL) {
        return;
    }
    void *espradio_arena_calloc(size_t, size_t);
    esp_phy_calibration_data_t *cal_data = (esp_phy_calibration_data_t *)espradio_arena_calloc(1, sizeof(esp_phy_calibration_data_t));
    if (cal_data == NULL) {
        cal_data = &s_phy_cal_data;
        memset(cal_data, 0, sizeof(*cal_data));
    }
    int rr = rtc_get_reset_reason(0);
    phy_init_param_set(1u);

    /* ESP32 does not have phy_bbpll_en_usb (no USB PHY). */

    bool force_cal_none = false;
    esp_phy_calibration_mode_t cal_mode = force_cal_none
                                              ? PHY_RF_CAL_NONE
                                              : (esp_phy_calibration_mode_t)(rr == 5 ? PHY_RF_CAL_NONE : PHY_RF_CAL_FULL);

    esp_err_t nvs_rc = esp_phy_load_cal_data_from_nvs(cal_data);
    if (nvs_rc != ESP_OK && !force_cal_none) {
        cal_mode = PHY_RF_CAL_FULL;
    }
    (void)espradio_hal_read_mac_go((unsigned char *)cal_data->mac, 0);

    /* On ESP32, phy_get_romfuncs() returns the ROM's PHY function table.
     * register_chipv7_phy() will read and patch this table internally. */
    g_phyFuns = phy_get_romfuncs();

    ets_printf("phy: rr=%d nvs_rc=%d cal_mode=%d cal_data=%p is_fallback=%d\n",
               rr, (int)nvs_rc, (int)cal_mode, cal_data,
               cal_data == &s_phy_cal_data);
    ets_printf("phy: mac=%02x:%02x:%02x:%02x:%02x:%02x\n",
               cal_data->mac[0], cal_data->mac[1], cal_data->mac[2],
               cal_data->mac[3], cal_data->mac[4], cal_data->mac[5]);
    ets_printf("phy: &s_is_phy_calibrated=%p pre-call val=%d\n",
               &s_is_phy_calibrated, s_is_phy_calibrated);

    int rc = register_chipv7_phy(init_data, cal_data, cal_mode);
    ets_printf("phy: register_chipv7_phy rc=%d post-call s_is_phy_calibrated=%d\n",
               rc, s_is_phy_calibrated);
    if (cal_mode != PHY_RF_CAL_NONE && (nvs_rc != ESP_OK || rc == 1)) {
        esp_phy_store_cal_data_to_nvs(cal_data);
    }
    if (esp_deep_sleep_register_phy_hook(phy_close_rf) == ESP_OK &&
        cal_data != &s_phy_cal_data) {
        void espradio_arena_free(void *);
        espradio_arena_free(cal_data);
    }
}

static void espradio_phy_lock(void) {
    while (__sync_lock_test_and_set(&s_phy_spin_lock, 1u)) {
    }
}

static void espradio_phy_unlock(void) {
    __sync_lock_release(&s_phy_spin_lock);
}

void esp_phy_common_clock_enable(void) {
    (void)0;
}

void esp_phy_common_clock_disable(void) {
    (void)0;
}

static uint32_t phy_enabled_modem_contains_local(uint32_t modem) {
    return (uint32_t)((s_phy_modem_flags_local & (uint16_t)modem) != 0u);
}

/* ESP32 does not have phy_param_track_tot, so PLL tracking is not needed. */
void phy_track_pll_init(void) {
    /* no-op on ESP32 */
}

void phy_track_pll_deinit(void) {
    /* no-op on ESP32 */
}

void phy_track_pll(void) {
    /* no-op on ESP32 */
}

void phy_digital_regs_load(void) {
    if ((s_phy_is_digital_regs_stored_local != 0u) &&
        (s_phy_digital_regs_mem_ptr != NULL)) {
        (void)phy_dig_reg_backup(false, s_phy_digital_regs_mem_ptr);
    }
}

void phy_digital_regs_store(void) {
    if (s_phy_digital_regs_mem_ptr != NULL) {
        (void)phy_dig_reg_backup(true, s_phy_digital_regs_mem_ptr);
        s_phy_is_digital_regs_stored_local = 1u;
    }
}

void phy_set_modem_flag(uint32_t modem) {
    s_phy_modem_flags_local = (uint16_t)(s_phy_modem_flags_local | (uint16_t)modem);
}

void phy_clr_modem_flag(uint32_t modem) {
    s_phy_modem_flags_local = (uint16_t)(s_phy_modem_flags_local & (uint16_t)(~(uint16_t)modem));
}

uint32_t phy_get_modem_flag(void) {
    return (uint32_t)s_phy_modem_flags_local;
}

void esp_phy_modem_init(void) {
    espradio_phy_lock();
    s_phy_modem_init_ref = (uint8_t)(s_phy_modem_init_ref + 1u);
    if (s_phy_digital_regs_mem_ptr == NULL) {
        s_phy_digital_regs_mem_ptr = (uint32_t *)heap_caps_malloc(0x54u, 0x808u);
    }
    espradio_phy_unlock();
}

void *heap_caps_malloc(size_t size, uint32_t caps) {
    (void)caps;
    void *espradio_arena_alloc(size_t);
    return espradio_arena_alloc(size);
}

void esp_phy_modem_deinit(void) {
    espradio_phy_lock();
    uint8_t prev_ref = s_phy_modem_init_ref;
    s_phy_modem_init_ref = (uint8_t)(s_phy_modem_init_ref - 1u);
    if (prev_ref == 1u) {
        s_phy_is_digital_regs_stored_local = 0u;
        if (s_phy_digital_regs_mem_ptr != NULL) {
            void espradio_arena_free(void *);
            espradio_arena_free(s_phy_digital_regs_mem_ptr);
        }
        s_phy_digital_regs_mem_ptr = NULL;
        phy_init_flag();
    }
    espradio_phy_unlock();
}

bool phy_ant_need_update(void) {
    return s_phy_ant_need_update_local != 0u;
}

void phy_ant_update(void) {
    uint32_t ant0 = (uint32_t)s_phy_ant_config_local.enabled_ant0 & 0x0fu;
    uint32_t ant1 = (uint32_t)s_phy_ant_config_local.enabled_ant1 & 0x0fu;
    uint32_t rx_ant0 = ant0;
    uint32_t rx_ant1 = ant0;
    uint32_t rx_auto = 0u;

    if (s_phy_ant_config_local.rx_ant_mode == ESP_PHY_ANT_MODE_ANT1) {
        rx_ant1 = ant1;
        rx_auto = 0u;
    } else {
        rx_ant1 = ant1;
        if (s_phy_ant_config_local.rx_ant_mode == ESP_PHY_ANT_MODE_AUTO) {
            rx_auto = 1u;
        } else {
            rx_auto = 0u;
            rx_ant1 = ant0;
        }
    }

    uint32_t tx_ant0 = ant1;
    if (s_phy_ant_config_local.tx_ant_mode != ESP_PHY_ANT_MODE_ANT1) {
        tx_ant0 = ant0;
    }

    ant_dft_cfg(s_phy_ant_config_local.rx_ant_default == ESP_PHY_ANT_ANT1);
    ant_tx_cfg((uint8_t)tx_ant0);
    ant_rx_cfg(rx_auto != 0u, (uint8_t)rx_ant0, (uint8_t)rx_ant1);
}

void phy_ant_clr_update_flag(void) {
    s_phy_ant_need_update_local = 0u;
}

void espradio_phy_adapter_reset(void) {
    s_is_phy_calibrated = 0u;
    s_phy_modem_flags_local = 0u;
    s_phy_modem_init_ref = 0u;
    s_phy_is_digital_regs_stored_local = 0u;
    s_phy_digital_regs_mem_ptr = NULL;
    s_phy_ant_need_update_local = 1u;
    uint32_t addr = (uint32_t)(uintptr_t)&s_is_phy_calibrated;
    uint32_t val = (uint32_t)s_is_phy_calibrated;
    ets_printf("espradio: reset addr=0x%x\n", (unsigned)addr);
    ets_printf("espradio: reset val=%d\n", (int)val);
}

uint32_t espradio_phy_adapter_get_calibrated(void) {
    return (uint32_t)s_is_phy_calibrated;
}

uint32_t espradio_phy_adapter_get_calibrated_addr(void) {
    return (uint32_t)(uintptr_t)&s_is_phy_calibrated;
}

void esp_phy_enable(esp_phy_modem_t modem) {
    espradio_phy_lock();
    /* Re-assert g_phyFuns on every enable — BSS can be zeroed between scans. */
    g_phyFuns = phy_get_romfuncs();
    uint32_t modem_flags = phy_get_modem_flag();
    ets_printf("phy: esp_phy_enable modem=%d flags=%d calibrated=%d (@%p byte=%x)\n",
               (int)modem, (int)modem_flags, (int)s_is_phy_calibrated,
               &s_is_phy_calibrated,
               (unsigned)(*(volatile uint8_t *)&s_is_phy_calibrated));
    if (modem_flags == 0u) {
        if (s_is_phy_calibrated == 0u) {
            esp_phy_load_cal_and_init();
            s_is_phy_calibrated = 1u;
        } else {
            PHY_ADAPTER_DBG("espradio: esp_phy_enable phy_wakeup_init\n");
            phy_wakeup_init();
            phy_digital_regs_load();
        }
        if (phy_ant_need_update()) {
            phy_ant_update();
            phy_ant_clr_update_flag();
        }
    }
    phy_set_modem_flag(modem);
    espradio_phy_unlock();
}

void esp_phy_disable(esp_phy_modem_t modem) {
    espradio_phy_lock();
    phy_clr_modem_flag(modem);
    if (phy_get_modem_flag() == 0u) {
        phy_digital_regs_store();
        phy_close_rf();
        /* ESP32 does NOT call phy_xpd_tsens (not available on this chip). */
        /* Force full re-init on next enable. */
        s_is_phy_calibrated = 0u;
    }
    espradio_phy_unlock();
}
