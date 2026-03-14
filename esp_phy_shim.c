//go:build esp32c3

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "blobs/include/esp_phy_init.h"
#include "blobs/include/esp_phy.h"
#include "blobs/include/esp_timer.h"

#ifndef ESPRADIO_PHY_STUB_TEST
#define ESPRADIO_PHY_STUB_TEST 0
#endif

#ifndef ESPRADIO_PHY_USE_PROBE
#define ESPRADIO_PHY_USE_PROBE 0
#endif

#ifndef ESPRADIO_PHY_DEBUG
#define ESPRADIO_PHY_DEBUG 0
#endif


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

#define SYSCON_CLK_EN_REG     (*(volatile uint32_t *)0x60026014)
#define SYSCON_WIFI_RST_EN    (*(volatile uint32_t *)0x60026018)
#define RTC_CNTL_DIG_PWC      (*(volatile uint32_t *)0x60008088)
#define RTC_CNTL_DIG_ISO      (*(volatile uint32_t *)0x6000808c)
#define WIFI_BT_CLK_EN_MASK   0x0078078fU
#define WIFI_BT_CLK_DIS_MASK  0xff87f870U
#define MODEM_RESET_WHEN_PU   (0x01U | 0x02U | 0x04U | 0x08U | 0x10U | (1U<<9) | (1U<<11) | (1U<<13))
#define RTC_WIFI_FORCE_PD     (1U << 17)
#define RTC_WIFI_FORCE_ISO    (1U << 28)

static uint8_t s_wifi_bt_common_ref;
static uint32_t s_wifi_bt_pd_ref;
static volatile uint32_t s_wifi_bt_pd_lock;

static void esp_rom_delay_us(uint32_t us) {
    for (volatile uint32_t i = 0; i < us * 20; i++) {
        (void)i;
    }
}

void wifi_bt_common_module_enable(void);
void wifi_bt_common_module_disable(void);

void esp_wifi_bt_power_domain_on(void) {
    printf("espradio: esp_wifi_bt_power_domain_on\n");
    while (__sync_lock_test_and_set(&s_wifi_bt_pd_lock, 1U)) {}
    uint32_t prev = s_wifi_bt_pd_ref;
    s_wifi_bt_pd_ref++;
    if (prev == 0U) {
        RTC_CNTL_DIG_PWC &= ~RTC_WIFI_FORCE_PD;
        esp_rom_delay_us(10);
        wifi_bt_common_module_enable();
        SYSCON_WIFI_RST_EN |= MODEM_RESET_WHEN_PU;
        SYSCON_WIFI_RST_EN &= ~MODEM_RESET_WHEN_PU;
        RTC_CNTL_DIG_ISO &= ~RTC_WIFI_FORCE_ISO;
        wifi_bt_common_module_disable();
    }
    __sync_lock_release(&s_wifi_bt_pd_lock);
}

void esp_wifi_bt_power_domain_off(void) {
    while (__sync_lock_test_and_set(&s_wifi_bt_pd_lock, 1U)) {}
    if (s_wifi_bt_pd_ref > 0U) {
        s_wifi_bt_pd_ref--;
        if (s_wifi_bt_pd_ref == 0U) {
            RTC_CNTL_DIG_ISO |= RTC_WIFI_FORCE_ISO;
            RTC_CNTL_DIG_PWC |= RTC_WIFI_FORCE_PD;
        }
    }
    __sync_lock_release(&s_wifi_bt_pd_lock);
}

void wifi_bt_common_module_enable(void) {
    if (s_wifi_bt_common_ref == 0U) {
        SYSCON_CLK_EN_REG |= WIFI_BT_CLK_EN_MASK;
    }
    s_wifi_bt_common_ref++;
}

void wifi_bt_common_module_disable(void) {
    if (s_wifi_bt_common_ref > 0U) {
        s_wifi_bt_common_ref--;
        if (s_wifi_bt_common_ref == 0U) {
            SYSCON_CLK_EN_REG &= WIFI_BT_CLK_DIS_MASK;
        }
    }
}
extern void ant_dft_cfg(uint32_t ant) __attribute__((weak));
extern void ant_tx_cfg(uint32_t tx_ant) __attribute__((weak));
extern void ant_rx_cfg(uint32_t rx_auto, uint32_t rx_ant0, uint32_t rx_ant1) __attribute__((weak));
extern void *heap_caps_malloc(size_t size, uint32_t caps) __attribute__((weak));
extern void phy_param_track_tot(uint32_t wifi_track_pll, uint32_t ble_154_track_pll) __attribute__((weak));
extern uint8_t phy_dig_reg_backup(bool backup_en, uint32_t *mem_addr) __attribute__((weak));
extern void espradio_hal_init_clocks_go(void);
extern void espradio_hal_disable_clocks_go(void);
extern int rtc_get_reset_reason(int cpu_no);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
extern void espradio_phy_hook_trace_set(unsigned int enabled);
extern void espradio_pll_trace_set_enabled(unsigned int enabled);
extern int espradio_register_chipv7_phy_probe(const esp_phy_init_data_t *init_data,
                                              esp_phy_calibration_data_t *cal_data,
                                              esp_phy_calibration_mode_t cal_mode);

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
static uint8_t s_phy_dig_reg_backup_warned_once;
static esp_timer_handle_t s_phy_track_pll_timer;
static int64_t s_wifi_prev_timestamp_local;
static esp_phy_ant_config_t s_phy_ant_config_local = {
    .rx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .rx_ant_default = ESP_PHY_ANT_ANT0,
    .tx_ant_mode = ESP_PHY_ANT_MODE_ANT0,
    .enabled_ant0 = 0,
    .enabled_ant1 = 1,
};

static void espradio_phy_debug_dump(const esp_phy_init_data_t *init_data,
                                    esp_phy_calibration_data_t *cal_data,
                                    esp_phy_calibration_mode_t cal_mode);

static int espradio_register_chipv7_phy_logged(const esp_phy_init_data_t *init_data,
                                               esp_phy_calibration_data_t *cal_data,
                                               esp_phy_calibration_mode_t cal_mode) {
    int rr = rtc_get_reset_reason(0);
    uint8_t mac0 = 0;
    uint8_t mac1 = 0;
    if (cal_data) {
        mac0 = cal_data->mac[0];
        mac1 = cal_data->mac[1];
    }
    printf("espradio: register_chipv7_phy call begin init=%p cal=%p mode=%u reset_reason=%d mac=%02x:%02x\n",
           (void *)init_data, (void *)cal_data, (unsigned)cal_mode, rr, (unsigned)mac0, (unsigned)mac1);

    int rc = register_chipv7_phy(init_data, cal_data, cal_mode);
    mac0 = 0;
    mac1 = 0;
    if (cal_data) {
        mac0 = cal_data->mac[0];
        mac1 = cal_data->mac[1];
    }
    printf("espradio: register_chipv7_phy call done rc=%d mac=%02x:%02x\n",
           rc, (unsigned)mac0, (unsigned)mac1);
    return rc;
}

void esp_phy_load_cal_and_init(void) {
    const esp_phy_init_data_t *init_data = esp_phy_get_init_data();
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
    printf("espradio: phy_bbpll_en_usb=%u reset_reason=%d\n",
           (unsigned)(bbpll_usb ? 1u : 0u), rr);
    bool force_cal_none = (rr == 21);
    esp_phy_calibration_mode_t cal_mode = force_cal_none
                                              ? PHY_RF_CAL_NONE
                                              : (esp_phy_calibration_mode_t)(rr == 5 ? PHY_RF_CAL_NONE : PHY_RF_CAL_FULL);

    esp_err_t nvs_rc = esp_phy_load_cal_data_from_nvs(cal_data);
    if (nvs_rc != ESP_OK && !force_cal_none) {
        cal_mode = PHY_RF_CAL_FULL;
    }
    int mac_rc = espradio_hal_read_mac_go((unsigned char *)cal_data->mac, 0);
    printf("espradio: cal_data mac rc=%d mac=%02x:%02x:%02x:%02x:%02x:%02x nvs_rc=%d cal_mode=%u\n",
           mac_rc,
           (unsigned)cal_data->mac[0], (unsigned)cal_data->mac[1],
           (unsigned)cal_data->mac[2], (unsigned)cal_data->mac[3],
           (unsigned)cal_data->mac[4], (unsigned)cal_data->mac[5],
           (int)nvs_rc, (unsigned)cal_mode);
    espradio_phy_debug_dump(init_data, cal_data, cal_mode);
    /* Keep trace off during rf_init; probe enables it right before bb_init. */
    espradio_phy_hook_trace_set(0u);
    espradio_pll_trace_set_enabled(0u);
#if ESPRADIO_PHY_USE_PROBE
    int rc = espradio_register_chipv7_phy_probe(init_data, cal_data, cal_mode);
#else
    int rc = espradio_register_chipv7_phy_logged(init_data, cal_data, cal_mode);
#endif
    printf("espradio: register_chipv7_phy rc=%d\n", rc);
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

__attribute__((weak)) void esp_phy_common_clock_enable(void) {
    wifi_bt_common_module_enable();
}

__attribute__((weak)) void esp_phy_common_clock_disable(void) {
    wifi_bt_common_module_disable();
}

static uint32_t phy_enabled_modem_contains_local(uint32_t modem) {
    return (uint32_t)((s_phy_modem_flags_local & (uint16_t)modem) != 0u);
}

static void phy_track_pll_internal_local(void) {
    if (phy_enabled_modem_contains_local(1u) == 0u) {
        return;
    }
    s_wifi_prev_timestamp_local = esp_timer_get_time();
    if (phy_param_track_tot != NULL) {
        phy_param_track_tot(1u, 0u);
    } else {
        rom_phy_track_pll_cap();
    }
}

static void phy_track_pll_timer_callback_local(void *arg) {
    (void)arg;
    espradio_phy_lock();
    phy_track_pll_internal_local();
    espradio_phy_unlock();
}

__attribute__((weak)) void phy_track_pll_init(void) {
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

__attribute__((weak)) void phy_track_pll_deinit(void) {
    if (s_phy_track_pll_timer != NULL) {
        (void)esp_timer_stop(s_phy_track_pll_timer);
        (void)esp_timer_delete(s_phy_track_pll_timer);
        s_phy_track_pll_timer = NULL;
    }
    s_phy_track_pll_started_local = 0u;
}

__attribute__((weak)) void phy_track_pll(void) {
    if ((s_phy_track_pll_started_local != 0u) && (phy_enabled_modem_contains_local(1u) != 0u)) {
        int64_t now = esp_timer_get_time();
        if ((now - s_wifi_prev_timestamp_local) > 1000000LL) {
            phy_track_pll_internal_local();
        }
    }
}

__attribute__((weak)) void phy_digital_regs_load(void) {
    if ((s_phy_is_digital_regs_stored_local != 0u) &&
        (s_phy_digital_regs_mem_ptr != NULL) &&
        (phy_dig_reg_backup != NULL)) {
        (void)phy_dig_reg_backup(false, s_phy_digital_regs_mem_ptr);
    }
}

__attribute__((weak)) void phy_digital_regs_store(void) {
    if ((s_phy_digital_regs_mem_ptr != NULL) && (phy_dig_reg_backup != NULL)) {
        (void)phy_dig_reg_backup(true, s_phy_digital_regs_mem_ptr);
        s_phy_is_digital_regs_stored_local = 1u;
    } else if (s_phy_dig_reg_backup_warned_once == 0u) {
        s_phy_dig_reg_backup_warned_once = 1u;
        printf("espradio: phy_dig_reg_backup missing\n");
    }
}

__attribute__((weak)) void phy_set_modem_flag(uint32_t modem) {
    s_phy_modem_flags_local = (uint16_t)(s_phy_modem_flags_local | (uint16_t)modem);
}

__attribute__((weak)) void phy_clr_modem_flag(uint32_t modem) {
    s_phy_modem_flags_local = (uint16_t)(s_phy_modem_flags_local & (uint16_t)(~(uint16_t)modem));
}

__attribute__((weak)) uint32_t phy_get_modem_flag(void) {
    return (uint32_t)s_phy_modem_flags_local;
}

void esp_phy_modem_init(void) {
    espradio_phy_lock();
    s_phy_modem_init_ref = (uint8_t)(s_phy_modem_init_ref + 1u);
    if (s_phy_digital_regs_mem_ptr == NULL && heap_caps_malloc != NULL) {
        s_phy_digital_regs_mem_ptr = (uint32_t *)heap_caps_malloc(0x54u, 0x808u);
    }
    espradio_phy_unlock();
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

__attribute__((weak)) bool phy_ant_need_update(void) {
    return s_phy_ant_need_update_local != 0u;
}

__attribute__((weak)) void phy_ant_update(void) {
    if ((ant_dft_cfg == NULL) || (ant_tx_cfg == NULL) || (ant_rx_cfg == NULL)) {
        return;
    }
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

    ant_dft_cfg((uint32_t)(s_phy_ant_config_local.rx_ant_default == ESP_PHY_ANT_ANT1));
    ant_tx_cfg(tx_ant0);
    ant_rx_cfg(rx_auto, rx_ant0, rx_ant1);
}

__attribute__((weak)) void phy_ant_clr_update_flag(void) {
    s_phy_ant_need_update_local = 0u;
}

static void espradio_phy_debug_dump(const esp_phy_init_data_t *init_data,
                                    esp_phy_calibration_data_t *cal_data,
                                    esp_phy_calibration_mode_t cal_mode) {
#if ESPRADIO_PHY_DEBUG
    if (s_phy_debug_once != 0u) {
        return;
    }
    s_phy_debug_once = 1u;
    const char *ver = get_phy_version_str();
    uint32_t *tbl = (uint32_t *)g_phyFuns;
    printf("espradio: phy_debug ver=%s g_phyFuns=%p init=%p cal=%p mode=%u\n",
           ver ? ver : "?", g_phyFuns, (void *)init_data, (void *)cal_data, (unsigned)cal_mode);
    if (tbl) {
        printf("espradio: phy_debug tbl[0x1ac]=%p tbl[0x228]=%p tbl[0x1b4]=%p tbl[0x1b8]=%p tbl[0x1bc]=%p\n",
               (void *)(uintptr_t)tbl[0x1acu / 4u],
               (void *)(uintptr_t)tbl[0x228u / 4u],
               (void *)(uintptr_t)tbl[0x1b4u / 4u],
               (void *)(uintptr_t)tbl[0x1b8u / 4u],
               (void *)(uintptr_t)tbl[0x1bcu / 4u]);
    }
#else
    (void)init_data;
    (void)cal_data;
    (void)cal_mode;
#endif
}

void esp_phy_enable(uint32_t modem) {
    espradio_phy_lock();
    uint32_t modem_flags = phy_get_modem_flag();
    printf("espradio: esp_phy_enable modem=%lu flags=%lu calibrated=%u\n",
           (unsigned long)modem, (unsigned long)modem_flags, (unsigned)s_is_phy_calibrated);
    if (modem_flags == 0u) {
        esp_phy_common_clock_enable();
        if (s_is_phy_calibrated == 0u) {
            esp_phy_load_cal_and_init();
            s_is_phy_calibrated = 1u;
        } else {
            printf("espradio: esp_phy_enable phy_wakeup_init\n");
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

void esp_phy_disable(uint32_t modem) {
    espradio_phy_lock();
    phy_clr_modem_flag(modem);
    if (phy_get_modem_flag() == 0u) {
#if ESPRADIO_PHY_STUB_TEST
        printf("espradio: esp_phy_disable STUB begin\n");
        phy_wifi_enable_set(0);
        printf("espradio: esp_phy_disable STUB done\n");
#else
        phy_track_pll_deinit();
        phy_digital_regs_store();
        phy_close_rf();
        phy_xpd_tsens();
        esp_phy_common_clock_disable();
#endif
    }
    espradio_phy_unlock();
}
