//go:build esp32s3

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "../blobs/include/include.h"
#include "esp_phy.h"

#define PHY_ADAPTER_DBG(...) ((void)0)

extern void phy_wifi_enable_set(uint8_t enable);
extern void *g_phyFuns;
extern int register_chipv7_phy(const esp_phy_init_data_t *init_data,
                               esp_phy_calibration_data_t *cal_data,
                               esp_phy_calibration_mode_t cal_mode);
extern void phy_wakeup_init(void);
extern void phy_close_rf(void);
extern void phy_init_flag(void);
extern void phy_xpd_tsens(void);
extern void phy_init_param_set(uint8_t param);
extern void phy_bbpll_en_usb(bool en);
extern void rom_phy_ant_init(void);
extern void rom_phy_track_pll_cap(void);
extern esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void));

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

/* ---------- PHY ROM function table ----------
 *
 * The ROM section .data_phyrom (VMA 0x3fcef3d4, 0x298 bytes) contains:
 *   [0x000] rom_phyFuns pointer = 0x3fcef3d8 (points to the table below)
 *   [0x004..0x298] g_phyFuns_instance: 165 ROM function pointers
 *
 * The ROM bootloader initialises this from its own flash image, but TinyGo's
 * startup code zeros all application DRAM which includes this region.  We must
 * restore the ROM initial values before calling register_chipv7_phy.
 *
 * Values extracted from esp32s3_rev0_rom.elf, section .data_phyrom,
 * file offset 0x062128, size 0x298 (166 words). */

/* Address in ROM DRAM where phy_get_romfuncs() reads the table pointer from. */
#define PHY_ROM_FUNCS_PTR_ADDR  ((volatile uint32_t *)0x3fcef3d4)
/* The ROM's PHY function table in DRAM (g_phyFuns_instance). */
#define PHY_ROM_FUNCS_INSTANCE  ((uint32_t *)0x3fcef3d8)

static const uint32_t s_phyrom_data_init[166] = {
    0x3fcef3d8,                                      /* [0x000] ptr → INSTANCE */
    0x40037fa8, 0x40037fcc, 0x40038020,              /* [0x004] INSTANCE[0..2] */
    0x40038068, 0x400380b4, 0x400380e0, 0x40055cc8,  /* [0x010] */
    0x400380f8, 0x40038154, 0x40038194, 0x40055cdc,  /* [0x020] */
    0x400381d8, 0x40055cec, 0x40055d08, 0x40055d24,  /* [0x030] */
    0x40038210, 0x40038254, 0x40038298, 0x400382c4,  /* [0x040] */
    0x40038348, 0x40055d70, 0x400385cc, 0x400386f8,  /* [0x050] */
    0x40038fc8, 0x40038730, 0x4003900c, 0x40039034,  /* [0x060] */
    0x40039074, 0x40038750, 0x4003879c, 0x400387e0,  /* [0x070] */
    0x40038884, 0x4003889c, 0x400390fc, 0x400388e0,  /* [0x080] */
    0x40038904, 0x40038928, 0x40038940, 0x40055d4c,  /* [0x090] slot 0x98=0x40038940 */
    0x40038974, 0x400389f0, 0x40038b00, 0x40038b44,  /* [0x0a0] */
    0x40038c5c, 0x40038cfc, 0x40038d0c, 0x40038d4c,  /* [0x0b0] */
    0x40039168, 0x40038d80, 0x40038db4, 0x40038e60,  /* [0x0c0] */
    0x40038f60, 0x400391c0, 0x40038eac, 0x40038f38,  /* [0x0d0] */
    0x40055bc8, 0x40036438, 0x40036470, 0x40036508,  /* [0x0e0] */
    0x40055bf4, 0x4003658c, 0x4003660c, 0x40036648,  /* [0x0f0] */
    0x400366c0, 0x40036714, 0x40036794, 0x40036804,  /* [0x100] */
    0x40036820, 0x400368c4, 0x4003696c, 0x400369ec,  /* [0x110] */
    0x40055bfc, 0x40036a18, 0x40036aa4, 0x40036ac8,  /* [0x120] */
    0x40036afc, 0x40036b44, 0x40036b98, 0x40055c1c,  /* [0x130] */
    0x40036bc8, 0x40036bec, 0x40055c70, 0x40036c28,  /* [0x140] */
    0x40036ce0, 0x40036d1c, 0x40035474, 0x40035494,  /* [0x150] */
    0x400354bc, 0x40055bb8, 0x40055bc0, 0x400354fc,  /* [0x160] slots 0x160/0x164 patched below */
    0x40035548, 0x40035594, 0x400355d8, 0x40035614,  /* [0x170] */
    0x40035684, 0x400356f0, 0x40035738, 0x400357f8,  /* [0x180] */
    0x40035818, 0x40035880, 0x4003589c, 0x400358d8,  /* [0x190] */
    0x40035964, 0x40035a2c, 0x40035a78, 0x40035acc,  /* [0x1a0] */
    0x40035b28, 0x40035b6c, 0x40035b80, 0x40035b94,  /* [0x1b0] */
    0x40035bf8, 0x40035c2c, 0x40035c80, 0x40035cd4,  /* [0x1c0] */
    0x40035d60, 0x40035dc4, 0x40035e3c, 0x40035e7c,  /* [0x1d0] */
    0x40035eac, 0x40035f44, 0x4003600c, 0x40036020,  /* [0x1e0] */
    0x4003603c, 0x4003605c, 0x400360d4, 0x40036120,  /* [0x1f0] */
    0x40036184, 0x40036210, 0x40036230, 0x4003627c,  /* [0x200] slots 0x204/0x208 */
    0x40036d50, 0x40036d94, 0x40036e78, 0x40036f1c,  /* [0x210] */
    0x40036fc4, 0x40055c7c, 0x40037050, 0x40037118,  /* [0x220] */
    0x400371f0, 0x40037254, 0x4003726c, 0x400372c0,  /* [0x230] slot 0x234 */
    0x40037310, 0x40037348, 0x40037370, 0x4003737c,  /* [0x240] */
    0x400373fc, 0x40037498, 0x40037600, 0x400377a4,  /* [0x250] */
    0x40037804, 0x40037854, 0x40037894, 0x40037958,  /* [0x260] */
    0x40037a44, 0x40037a7c, 0x40037b08, 0x40037c34,  /* [0x270] */
    0x40037c70, 0x40037c88, 0x40037d28, 0x40037d74,  /* [0x280] */
    0x40037d8c, 0x40037dec,                          /* [0x290] */
};

static void espradio_init_phy_funcs_table(void) {
    /* Enable I2C analog master paths needed by PHY calibration.
     * ANA_CONFIG_REG (0x6000E044): clear bit 17 = BBPLL I2C, clear bit 18 = SAR I2C.
     * ANA_CONFIG2_REG (0x6000E048): set bit 16 = SAR ADC config.
     * RTC_CNTL_OPTIONS0_REG: clear bb_i2c_force_pd, bbpll_force_pd, bbpll_i2c_force_pd. */
    #define ANA_CONFIG_REG    (*(volatile uint32_t *)0x6000E044)
    #define ANA_CONFIG2_REG   (*(volatile uint32_t *)0x6000E048)
    #define RTC_CNTL_OPTIONS0 (*(volatile uint32_t *)0x60008000)
    ANA_CONFIG_REG &= ~((1u << 17) | (1u << 18));
    ANA_CONFIG2_REG |= (1u << 16);
    RTC_CNTL_OPTIONS0 &= ~((1u << 6) | (1u << 8) | (1u << 10));

    /* Restore the ROM .data_phyrom region that TinyGo startup zeroed.
     * This restores both the table pointer at 0x3fcef3d4 and the 165-entry
     * function table starting at 0x3fcef3d8 with ROM function addresses.
     * register_chipv7_phy → phy_get_romfunc_addr will then overwrite ~31
     * slots with IRAM addresses from libphy.a; all other slots keep their
     * ROM addresses (e.g. slot 0x98 used by phy_rfcal_data_check). */
    memcpy((void *)0x3fcef3d4, s_phyrom_data_init, sizeof(s_phyrom_data_init));

    /* Patch the two critical-section slots with our OS-aware versions. */
    PHY_ROM_FUNCS_INSTANCE[0x160 / 4] = (uint32_t)(uintptr_t)phy_enter_critical;
    PHY_ROM_FUNCS_INSTANCE[0x164 / 4] = (uint32_t)(uintptr_t)phy_exit_critical;
    g_phyFuns = PHY_ROM_FUNCS_INSTANCE;
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

extern void phy_param_track_tot(uint32_t wifi_track_pll, uint32_t ble_154_track_pll);
extern uint8_t phy_dig_reg_backup(bool backup_en, uint32_t *mem_addr);
void *heap_caps_malloc(size_t size, uint32_t caps);
extern int rtc_get_reset_reason(int cpu_no);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
static uint8_t s_is_phy_calibrated;
static uint8_t s_phy_modem_init_ref;
static esp_phy_calibration_data_t s_phy_cal_data;
static volatile uint32_t s_phy_spin_lock;
static uint16_t s_phy_modem_flags_local;
static uint32_t s_phy_track_pll_started_local;
static uint32_t s_phy_debug_once;
static uint8_t s_phy_ant_need_update_local = 1u;
static uint32_t *s_phy_digital_regs_mem_ptr;
static uint8_t s_phy_is_digital_regs_stored_local;
static esp_timer_handle_t s_phy_track_pll_timer;
static int64_t s_wifi_prev_timestamp_local;
static esp_phy_ant_config_t s_phy_ant_config_local = {
    .rx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .rx_ant_default = ESP_PHY_ANT_ANT0,
    .tx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .enabled_ant0 = 0,
    .enabled_ant1 = 1,
};

/* Static PHY init data blob (128 bytes for ESP32-S3).
 * Values from esp-hal PHY_INIT_DATA_DEFAULT with CONFIG_ESP32_PHY_MAX_TX_POWER=20.
 * Same as ESP32-C3 — both use identical PHY init data layout.
 */
static const esp_phy_init_data_t phy_init_data = { .params = {
    0x00, 0x00, 0x50, 0x50, 0x50, 0x4c, 0x4c, 0x48,   /*   0 -   7 */
    0x4c, 0x48, 0x48, 0x44, 0x4a, 0x46, 0x46, 0x42,   /*   8 -  15 */
    0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  16 -  23 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  24 -  31 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  32 -  39 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  40 -  47 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  48 -  55 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  56 -  63 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00, 0x00,   /*  64 -  71 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  72 -  79 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  80 -  87 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  88 -  95 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  96 - 103 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x74,   /* 104 - 111 */
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
    bool bbpll_usb = true;
    phy_bbpll_en_usb(bbpll_usb);
    PHY_ADAPTER_DBG("espradio: phy_bbpll_en_usb=%u reset_reason=%d\n",
                    (unsigned)(bbpll_usb ? 1u : 0u), rr);
    bool force_cal_none = false; /* USB reset (21) needs calibration just like cold boot */
    esp_phy_calibration_mode_t cal_mode = force_cal_none
                                              ? PHY_RF_CAL_NONE
                                              : (esp_phy_calibration_mode_t)(rr == 5 ? PHY_RF_CAL_NONE : PHY_RF_CAL_FULL);

    esp_err_t nvs_rc = esp_phy_load_cal_data_from_nvs(cal_data);
    if (nvs_rc != ESP_OK && !force_cal_none) {
        cal_mode = PHY_RF_CAL_FULL;
    }
    (void)espradio_hal_read_mac_go((unsigned char *)cal_data->mac, 0);
    espradio_init_phy_funcs_table();
    int rc = register_chipv7_phy(init_data, cal_data, cal_mode);
    /* phy_get_romfunc_addr (inside register_chipv7_phy) already updated
     * g_phyFuns = *PHY_ROM_FUNCS_PTR_ADDR = PHY_ROM_FUNCS_INSTANCE. */
    g_phyFuns = PHY_ROM_FUNCS_INSTANCE;
    if (cal_mode != PHY_RF_CAL_NONE && (nvs_rc != ESP_OK || rc == 1)) {
        esp_phy_store_cal_data_to_nvs(cal_data);
    }
    if (esp_deep_sleep_register_phy_hook(phy_close_rf) == ESP_OK &&
        esp_deep_sleep_register_phy_hook(phy_xpd_tsens) == ESP_OK &&
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

static void phy_track_pll_internal_local(void) {
    if (phy_enabled_modem_contains_local(1u) == 0u) {
        return;
    }
    s_wifi_prev_timestamp_local = esp_timer_get_time();
    phy_param_track_tot(1u, 0u);
}

static void phy_track_pll_timer_callback_local(void *arg) {
    (void)arg;
    espradio_phy_lock();
    phy_track_pll_internal_local();
    espradio_phy_unlock();
}

void phy_track_pll_init(void) {
    esp_timer_create_args_t args = {
        .callback = phy_track_pll_timer_callback_local,
        .arg = NULL,
        .dispatch_method = ESP_TIMER_TASK,
        .name = "phy_track_pll_t",
        .skip_unhandled_events = false,
    };
    if (esp_timer_create(&args, &s_phy_track_pll_timer) == ESP_OK) {
        (void)esp_timer_start_periodic(s_phy_track_pll_timer, 1000000ULL);
        s_phy_track_pll_started_local = 1u;
    }
}

void phy_track_pll_deinit(void) {
    if (s_phy_track_pll_timer != NULL) {
        (void)esp_timer_stop(s_phy_track_pll_timer);
        (void)esp_timer_delete(s_phy_track_pll_timer);
        s_phy_track_pll_timer = NULL;
    }
    s_phy_track_pll_started_local = 0u;
}

void phy_track_pll(void) {
    if ((s_phy_track_pll_started_local != 0u) && (phy_enabled_modem_contains_local(1u) != 0u)) {
        int64_t now = esp_timer_get_time();
        if ((now - s_wifi_prev_timestamp_local) > 1000000LL) {
            phy_track_pll_internal_local();
        }
    }
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

void esp_phy_enable(esp_phy_modem_t modem) {
    espradio_phy_lock();
    /* Re-assert g_phyFuns on every enable — BSS can be zeroed between scans. */
    g_phyFuns = PHY_ROM_FUNCS_INSTANCE;
    uint32_t modem_flags = phy_get_modem_flag();
    PHY_ADAPTER_DBG("espradio: esp_phy_enable modem=%lu flags=%lu calibrated=%u\n",
                    (unsigned long)modem, (unsigned long)modem_flags, (unsigned)s_is_phy_calibrated);
    if (modem_flags == 0u) {
        if (s_is_phy_calibrated == 0u) {
            esp_phy_load_cal_and_init();
            s_is_phy_calibrated = 1u;
        } else {
            PHY_ADAPTER_DBG("espradio: esp_phy_enable phy_wakeup_init\n");
            phy_wakeup_init();
            phy_digital_regs_load();
        }
        phy_track_pll_init();
        if (phy_ant_need_update()) {
            phy_ant_update();
            phy_ant_clr_update_flag();
        }
    }
    phy_set_modem_flag(modem);
    phy_track_pll();
    espradio_phy_unlock();
}

void esp_phy_disable(esp_phy_modem_t modem) {
    espradio_phy_lock();
    phy_clr_modem_flag(modem);
    if (phy_get_modem_flag() == 0u) {
        phy_track_pll_deinit();
        phy_digital_regs_store();
        phy_close_rf();
        phy_xpd_tsens();
        /* Force full re-init (register_chipv7_phy) on the next esp_phy_enable
         * instead of phy_wakeup_init.  phy_wakeup_init dispatches through
         * g_phyFuns noop entries that fail to restore RF state, causing MAC
         * interrupt storms on scan 2+. */
        s_is_phy_calibrated = 0u;
    }
    espradio_phy_unlock();
}
