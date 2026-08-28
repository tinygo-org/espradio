//go:build esp32

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "../blobs/include/include.h"
#include "esp_phy.h"

#define PHY_ADAPTER_DBG(...) ((void)0)

/* Strong override of the (weak) phy_printf in radio.c.  The ESP32 cgo varargs
 * path is unreliable and register_chipv7_phy calls phy_printf with %-format
 * banners; expanding those with vprintf produces garbage and can fault on a
 * bogus %s pointer.  When debug is enabled, print only the literal format text
 * via direct UART; otherwise this is a no-op. */
void phy_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    volatile uint32_t *fifo = (volatile uint32_t *)0x3FF40000;
    volatile uint32_t *status = (volatile uint32_t *)0x3FF4001C;
    if (format == 0) {
        return;
    }
    for (const char *s = format; *s; s++) {
        *fifo = (uint32_t)(unsigned char)*s;
        while (((*status >> 16) & 0xFF) != 0) {
        }
    }
#else
    (void)format;
#endif
}

extern void phy_wifi_enable_set(uint8_t enable);
extern void *g_phyFuns;
extern int register_chipv7_phy(const esp_phy_init_data_t *init_data,
                               esp_phy_calibration_data_t *cal_data,
                               esp_phy_calibration_mode_t cal_mode);
extern void phy_wakeup_init(void);
extern void phy_close_rf(void);
extern void phy_init_flag(void);
extern void phy_init_param_set(uint8_t param);
extern void phy_bbpll_en_usb(bool en);
extern void rom_phy_ant_init(void);
extern void rom_phy_track_pll_cap(void);
extern esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void));
extern uint32_t rom_phyFuns;
extern uint32_t g_phyFuns_instance;

/* PHY critical section — spinlock-based.
 * The blob calls phy_enter_critical/phy_exit_critical (U symbols in libphy.a)
 * to protect PHY register access.  We use Xtensa RSIL/WSR.PS to
 * disable/restore interrupts. */

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
 * The ROM section .data_phyrom (VMA 0x3ffae0c0, 0x1b0 bytes) contains:
 *   [0x000] rom_phyFuns pointer = 0x3ffae0c4 (points to the table below)
 *   [0x004..0x1ac] g_phyFuns_instance: 107 ROM function pointers
 *
 * We intentionally do not patch this ROM-owned DRAM area from firmware.
 * Keep behavior aligned with esp-hal/IDF-style startup and only reference
 * the table address expected by libphy.a (via phy_get_romfuncs / g_phyFuns). */

/* Address in ROM DRAM where phy_get_romfuncs() reads the table pointer from. */
#define PHY_ROM_FUNCS_PTR_ADDR  ((volatile uint32_t *)&rom_phyFuns)
/* The ROM's PHY function table in DRAM (g_phyFuns_instance). */
#define PHY_ROM_FUNCS_INSTANCE  ((uint32_t *)&g_phyFuns_instance)

static void espradio_init_phy_funcs_table(void) {
    /* Keep ROM table untouched; only set the pointer used by libphy call sites. */
    (void)PHY_ROM_FUNCS_PTR_ADDR;

    /* NOTE: unlike ESP32-S3, the ESP32 libphy.a references phy_enter_critical /
     * phy_exit_critical as *direct* undefined symbols (U in the archive), so it
     * calls OUR definitions straight through the linker — it does NOT dispatch
     * through the g_phyFuns table.  Patching table slots here would overwrite
     * genuine ROM function pointers and crash register_chipv7_phy, so we leave
     * the restored ROM table untouched. */
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

/* Static PHY init data blob (128 bytes for ESP32). Verified byte-for-byte
 * against esp-idf's components/esp_phy/esp32/phy_init_data.c: the first
 * 44 entries and the tail (indices 50-127) are literal constants there;
 * indices 44-49 (TX power) are LIMIT(CONFIG_ESP_PHY_MAX_TX_POWER * 4, lo, hi)
 * with CONFIG_ESP_PHY_MAX_TX_POWER defaulting to 20 (esp_phy/Kconfig), so
 * val=80 clamps to each entry's hi bound: 78,72,66,60,56,52. Indices
 * 107-127 aren't listed in esp-idf's initializer at all — C zero-fills the
 * rest of a partially-initialized array, so they're 0x00 here too.
 * The Rust esp-wifi-sys crate vendored under esp-wifi/ has no independent
 * copy of this table; it links against this same esp-idf source.
 * Different from ESP32-S3/C3 — those use different calibration constants.
 */
static const esp_phy_init_data_t phy_init_data = { .params = {
    0x03, 0x03, 0x05, 0x09, 0x06, 0x05, 0x03, 0x06,   /*   0 -   7 */
    0x05, 0x04, 0x06, 0x04, 0x05, 0x00, 0x00, 0x00,   /*   8 -  15 */
    0x00, 0x05, 0x09, 0x06, 0x05, 0x03, 0x06, 0x05,   /*  16 -  23 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  24 -  31 */
    0xfc, 0xfc, 0xfe, 0xf0, 0xf0, 0xf0, 0xe0, 0xe0,   /*  32 -  39 */
    0xe0, 0x18, 0x18, 0x18, 0x4e, 0x48, 0x42, 0x3c,   /*  40 -  47: TX power */
    0x38, 0x34, 0x00, 0x01, 0x01, 0x02, 0x02, 0x03,   /*  48 -  55 */
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
    bool bbpll_usb = false; /* ESP32 has no built-in USB */
    PHY_ADAPTER_DBG("espradio: phy reset_reason=%d\n", rr);
    esp_phy_calibration_mode_t cal_mode = (esp_phy_calibration_mode_t)(rr == 5 ? PHY_RF_CAL_FULL : PHY_RF_CAL_NONE);

    esp_err_t nvs_rc = esp_phy_load_cal_data_from_nvs(cal_data);
    if (nvs_rc != ESP_OK) {
        cal_mode = PHY_RF_CAL_FULL;
    }
    (void)espradio_hal_read_mac_go((unsigned char *)cal_data->mac, 0);
    espradio_init_phy_funcs_table();
    int rc = register_chipv7_phy(init_data, cal_data, cal_mode);
    g_phyFuns = PHY_ROM_FUNCS_INSTANCE;
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

    rom_phy_ant_init();
    s_phy_ant_need_update_local = 0u;
}

/* NOTE: wifi_rf_phy_enable / wifi_rf_phy_disable are provided by the blob
 * (libnet80211.a) as strong symbols; the blob's wifi_rf_phy_enable calls
 * esp_phy_enable() itself.  We must NOT define our own (even weak) versions
 * here — the calibration must live in esp_phy_enable() below, which is what
 * both the blob and the OSI adapter call.  Defining a weak wifi_rf_phy_enable
 * that contains the calibration would be silently overridden by the blob's
 * strong version, so register_chipv7_phy would never run and g_phyFuns would
 * stay uninitialised (→ jump through a garbage function pointer). */

/* phy_get_romfuncs: called by libphy.a to obtain the ROM function table pointer.
 * Returns the address of the table instance (0x3ffae0c4). */
void *phy_get_romfuncs(void) {
    return (void *)PHY_ROM_FUNCS_INSTANCE;
}

/* esp_dport_access_reg_read: required by libphy.a on ESP32.
 * On single-core operation (which TinyGo uses), this is just a volatile read. */
uint32_t esp_dport_access_reg_read(uint32_t reg) {
    return *(volatile uint32_t *)reg;
}

/* config_is_cache_tx_buf_enabled: required by libpp.a.
 * We don't use cache TX buffers. */
__attribute__((weak)) int config_is_cache_tx_buf_enabled(void) {
    return 0;
}

/* esp_phy_enable / esp_phy_disable: called by the OSI adapter (espradio_phy_enable).
 * Modelled on IDF components/esp_phy/src/phy_init.c for CONFIG_IDF_TARGET_ESP32.
 * The original ESP32 PHY is monolithic (libphy.a) — it has NO phy_track_pll /
 * phy_ant_update / phy_xpd_tsens / phy_digital_regs split-out functions that the
 * newer chips (S3/C3) use, so we must NOT call them here.  This is where the PHY
 * calibration actually runs (esp_phy_load_cal_and_init -> register_chipv7_phy). */
extern void coex_bt_high_prio(void);

static uint8_t s_phy_access_ref;

void esp_phy_enable(esp_phy_modem_t modem) {
    (void)modem;
    espradio_phy_lock();
    /* Re-assert g_phyFuns on every enable — BSS can be zeroed between scans. */
    g_phyFuns = PHY_ROM_FUNCS_INSTANCE;
    if (s_phy_access_ref == 0u) {
        /* WiFi/BT common clock is already fully enabled by our clock init
         * (WIFI_CLK_EN = 0xFFFFFFFF), so esp_phy_common_clock_enable() is a
         * no-op for us — go straight to calibration. */
        if (s_is_phy_calibrated == 0u) {
            esp_phy_load_cal_and_init();
            s_is_phy_calibrated = 1u;
        } else {
            phy_wakeup_init();
        }
        coex_bt_high_prio();
    }
    s_phy_access_ref++;
    espradio_phy_unlock();
}

void esp_phy_disable(esp_phy_modem_t modem) {
    (void)modem;
    espradio_phy_lock();
    if (s_phy_access_ref > 0u) {
        s_phy_access_ref--;
    }
    if (s_phy_access_ref == 0u) {
        phy_close_rf();
    }
    espradio_phy_unlock();
}

/* rtc_get_xtal: referenced by libphy.a (register_chipv7_phy) but not present in
 * the ESP32 ROM or any blob.  Returns the crystal frequency in MHz.  The
 * ESP32-mini32 / ESP-WROOM-32 uses a 40 MHz crystal. */
int rtc_get_xtal(void) {
    return 40;
}

/* ets_install_lock: not present in ESP32 ROM. Stub. */
__attribute__((weak)) void ets_install_lock(void (*lock)(void), void (*unlock)(void)) {
    (void)lock;
    (void)unlock;
}
