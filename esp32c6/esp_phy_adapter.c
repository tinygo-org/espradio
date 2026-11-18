//go:build esp32c6

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "../blobs/include/include.h"
#include "esp_phy.h"

#ifndef ESPRADIO_PHY_DEBUG
#define ESPRADIO_PHY_DEBUG 0
#endif
#if ESPRADIO_PHY_DEBUG
#define PHY_ADAPTER_DBG(...) printf(__VA_ARGS__)
#else
#define PHY_ADAPTER_DBG(...) ((void)0)
#endif

extern void phy_wifi_enable_set(uint8_t enable);
extern void *g_phyFuns;
extern int register_chipv7_phy(const esp_phy_init_data_t *init_data,
                               esp_phy_calibration_data_t *cal_data,
                               esp_phy_calibration_mode_t cal_mode);
extern void phy_wakeup_init(void);
extern void phy_close_rf(void);
extern void phy_xpd_tsens(void);
extern void phy_init_param_set(uint8_t param);
extern void phy_bbpll_en_usb(bool en);
extern esp_err_t esp_deep_sleep_register_phy_hook(void (*hook)(void));

/* Weak stubs, so an application can add stored calibration data. */
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
    PHY_ADAPTER_DBG("espradio: esp_wifi_bt_power_domain_on\n");
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
static esp_timer_handle_t s_phy_track_pll_timer;
static int64_t s_wifi_prev_timestamp_local;
static esp_phy_ant_config_t s_phy_ant_config_local = {
    .rx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .rx_ant_default = ESP_PHY_ANT_ANT0,
    .tx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .enabled_ant0 = 0,
    .enabled_ant1 = 1,
};

/* PHY init data from esp-hal esp-phy/src/phy_init_data/phy_init_data_esp32c6.rs
 * with CONFIG_ESP_PHY_MAX_TX_POWER=20. */
static const esp_phy_init_data_t phy_init_data = { .params = {
    0x01, 0x00, 0x50, 0x50, 0x50, 0x50, 0x50, 0x4c,   /*   0 -   7 */
    0x4c, 0x4c, 0x4c, 0x48, 0x28, 0x28, 0x28, 0x28,   /*   8 -  15 */
    0x4c, 0x4c, 0x4c, 0x4c, 0x48, 0x28, 0x28, 0x28,   /*  16 -  23 */
    0x28, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff,   /*  24 -  31 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  32 -  39 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  40 -  47 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  48 -  55 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  56 -  63 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,   /*  64 -  71 */
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00,   /*  72 -  79 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  80 -  87 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  88 -  95 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /*  96 - 103 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x9b,   /* 104 - 111 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /* 112 - 119 */
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,   /* 120 - 127 */
} };

void esp_phy_load_cal_and_init(void) {
    const esp_phy_init_data_t *init_data = &phy_init_data;
    if (init_data == NULL) {
        printf("espradio: esp_phy_get_init_data returned NULL\n");
        return;
    }
    void *espradio_arena_calloc(size_t, size_t);
    esp_phy_calibration_data_t *cal_data = (esp_phy_calibration_data_t *)espradio_arena_calloc(1, sizeof(esp_phy_calibration_data_t));
    if (cal_data == NULL) {
        printf("espradio: calloc cal_data failed, using static\n");
        cal_data = &s_phy_cal_data;
        memset(cal_data, 0, sizeof(*cal_data));
    }
    int rr = rtc_get_reset_reason(0);
    phy_init_param_set(1u);
    bool bbpll_usb = true;
    phy_bbpll_en_usb(bbpll_usb);
    PHY_ADAPTER_DBG("espradio: phy_bbpll_en_usb=%u reset_reason=%d\n",
                    (unsigned)(bbpll_usb ? 1u : 0u), rr);
    /* Same calibration mode choice as ESP-IDF esp_phy_load_cal_and_init().
     * The enum is PARTIAL=0, NONE=1, FULL=2. */
    esp_phy_calibration_mode_t cal_mode = PHY_RF_CAL_PARTIAL;
    if (rr == 5 /* RESET_REASON_CORE_DEEP_SLEEP */) {
        cal_mode = PHY_RF_CAL_NONE;
    }

    esp_err_t nvs_rc = esp_phy_load_cal_data_from_nvs(cal_data);
    if (nvs_rc != ESP_OK) {
        cal_mode = PHY_RF_CAL_FULL;
    }
    (void)espradio_hal_read_mac_go((unsigned char *)cal_data->mac, 0);
    int rc = register_chipv7_phy(init_data, cal_data, cal_mode);
    PHY_ADAPTER_DBG("espradio: register_chipv7_phy(cal_mode=%d) -> %d (nvs_rc=%d)\n",
                    (int)cal_mode, rc, (int)nvs_rc);
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

extern void espradio_hal_enable_phy_clocks_go(int en);

void esp_phy_common_clock_enable(void) {
    espradio_hal_enable_phy_clocks_go(1);
}

void esp_phy_common_clock_disable(void) {
    espradio_hal_enable_phy_clocks_go(0);
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
    espradio_phy_unlock();
}

void *heap_caps_malloc(size_t size, uint32_t caps) {
    (void)caps;
    void *espradio_arena_alloc(size_t);
    return espradio_arena_alloc(size);
}

void esp_phy_modem_deinit(void) {
    espradio_phy_lock();
    s_phy_modem_init_ref = (uint8_t)(s_phy_modem_init_ref - 1u);
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
    if (phy_get_modem_flag() == 0u) {
        esp_phy_common_clock_enable();
        if (s_is_phy_calibrated == 0u) {
            esp_phy_load_cal_and_init();
            s_is_phy_calibrated = 1u;
        } else {
            phy_wakeup_init();
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
        phy_close_rf();
        phy_xpd_tsens();
        esp_phy_common_clock_disable();
    }
    espradio_phy_unlock();
}

extern uint32_t ets_clk_get_xtal_freq(void);

/* The C6 blobs need this, as in esp-hal esp-rom-sys/src/rom/mod.rs. */
uint32_t rtc_clk_xtal_freq_get(void) {
    return ets_clk_get_xtal_freq() / 1000000u;
}
