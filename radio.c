#include "sdkconfig.h"
#include "include.h"
#include "soc/interrupts.h"
#include "rom.h"
#include <string.h>

#ifndef ESPRADIO_RADIO_DEBUG
#define ESPRADIO_RADIO_DEBUG 0
#endif

#if ESPRADIO_RADIO_DEBUG
#define RADIO_DBG(...) printf(__VA_ARGS__)
#else
#define RADIO_DBG(...) ((void)0)
#endif

/* phy_get_romfunc_addr() from libphy.a would set g_phyFuns via ROM; in our build it leads to
 * an error/crash, so we never call it and always use the stub table below. */

/*
 * Typical IDF flow (esp_wifi_init in wifi_init.c):
 *   1. esp_wifi_set_sleep_* / esp_wifi_set_keep_alive_time — power management
 *   2. esp_wifi_set_log_level()           — in Go before init (g_log_level)
 *   3. esp_wifi_bt_power_domain_on()      — power domain
 *   4. esp_wifi_init_internal(config)     — see below
 *   5. esp_phy_modem_init()               — not in blobs
 *   6. esp_supplicant_init()              — called (WPA2-PSK)
 *   7. wifi_init_completed()
 *
 * esp_wifi_init_internal (implemented in libnet80211.a, no sources):
 *   According to esp_private/wifi.h: allocates resources for the driver — control structure,
 *   RX/TX buffers, WiFi NVS, etc. Must be called before any other WiFi API. Internally (from logs)
 *   it creates a queue, semaphores, and starts the wifi driver task (worker) that processes
 *   cmd 6 (set_log?), cmd 15 (init step). Return: ESP_OK (0) or an error code; the blob sometimes
 *   returns a DRAM address (0x3FC8xxxx) — in Go we treat that as success (isPointerLike).
 */
extern void wifi_init_completed(void);  /* libnet80211.a */
extern uint32_t g_log_level;            /* libnet80211.a: blob log level (0=none..5=verbose), wifi_log checks param_3 <= g_log_level */
extern char gChmCxt[252];               /* libnet80211.a: channel manager context */
extern void phy_get_romfunc_addr(void);
extern void *g_phyFuns;
extern wifi_osi_funcs_t *g_osi_funcs_p;
extern int rtc_get_reset_reason(int cpu_no);

/* Stub for the WIFI_INIT_CONFIG_DEFAULT() macro; we actually use espradio_osi_funcs below */
wifi_osi_funcs_t g_wifi_osi_funcs = {0};

extern wifi_osi_funcs_t espradio_osi_funcs;

#ifndef ESPRADIO_PHY_PATCH_ROMFUNCS
#define ESPRADIO_PHY_PATCH_ROMFUNCS 0
#endif

void espradio_set_blob_log_level(uint32_t level) {
    g_log_level = level;
}

uint32_t espradio_wifi_boot_state(void) {
    uint32_t state = 0;
    if (g_osi_funcs_p == &espradio_osi_funcs) {
        state |= 1u;
    }
    if (g_phyFuns != NULL) {
        state |= 2u;
    }
    return state;
}

const wpa_crypto_funcs_t g_wifi_default_wpa_crypto_funcs = {
    .size = sizeof(wpa_crypto_funcs_t),
    .version = ESP_WIFI_CRYPTO_VERSION,
};

/* One-time ROM hook setup: route ets_printf/esp_rom_printf to UART
 * and install a global lock based on ROM interrupt lock/unlock.
 * This mirrors what ESP-IDF does very early in startup. */
static int s_rom_hooks_inited;
static int s_bt_irq_wired;

static void espradio_bt_irq_stub(void *arg) {
    (void)arg;
}

static void espradio_bt_irq_prewire(void) {
    if (s_bt_irq_wired) {
        return;
    }
    s_bt_irq_wired = 1;

    enum {
        ESPRADIO_BTBB_INUM = 28,
        ESPRADIO_RWBT_INUM = 29,
        ESPRADIO_RWBLE_INUM = 30,
    };

    intr_matrix_set(0u, ETS_BT_BB_INTR_SOURCE, ESPRADIO_BTBB_INUM);
    intr_matrix_set(0u, ETS_RWBT_INTR_SOURCE, ESPRADIO_RWBT_INUM);
    intr_matrix_set(0u, ETS_RWBLE_INTR_SOURCE, ESPRADIO_RWBLE_INUM);

    ets_isr_attach(ESPRADIO_BTBB_INUM, espradio_bt_irq_stub, NULL);
    ets_isr_attach(ESPRADIO_RWBT_INUM, espradio_bt_irq_stub, NULL);
    ets_isr_attach(ESPRADIO_RWBLE_INUM, espradio_bt_irq_stub, NULL);

    ets_isr_unmask((1u << ESPRADIO_BTBB_INUM) | (1u << ESPRADIO_RWBT_INUM) | (1u << ESPRADIO_RWBLE_INUM));
}

void espradio_rom_hooks_init(void) {
    if (s_rom_hooks_inited) {
        return;
    }
    s_rom_hooks_inited = 1;

    /* Enable ROM printf via UART and install global lock callbacks. */
    ets_install_uart_printf();
    ets_install_lock(ets_intr_lock, ets_intr_unlock);
}

esp_err_t espradio_wifi_init(void) {
    espradio_rom_hooks_init();
    RADIO_DBG("espradio: early_reset_reason cpu0=%d cpu1=%d\n",
              rtc_get_reset_reason(0), rtc_get_reset_reason(1));
    phy_get_romfunc_addr();
    RADIO_DBG("espradio: phy_get_romfunc_addr g_phyFuns=%p\n", g_phyFuns);
    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    cfg.osi_funcs = &espradio_osi_funcs;
    cfg.nvs_enable = 0;

    extern wifi_osi_funcs_t *wifi_funcs;
    wifi_funcs = &espradio_osi_funcs;
    memcpy(&g_wifi_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));

    esp_wifi_set_sleep_min_active_time(50000);
    esp_wifi_set_keep_alive_time(10000000);
    esp_wifi_set_sleep_wait_broadcast_data_time(15000);
    RADIO_DBG("espradio: before esp_wifi_bt_power_domain_on\n");
    esp_wifi_bt_power_domain_on();
    RADIO_DBG("espradio: after esp_wifi_bt_power_domain_on\n");
    espradio_bt_irq_prewire();

    extern void espradio_coex_adapter_init(void);
    extern int coex_pre_init(void);
    espradio_coex_adapter_init();
    int coex_rc = coex_pre_init();
    RADIO_DBG("espradio: coex_pre_init -> %d\n", coex_rc);

    esp_err_t ret = esp_wifi_init_internal(&cfg);
    if (ret == 0) {
        extern void esp_phy_modem_init(void);
        esp_phy_modem_init();

        extern esp_err_t esp_supplicant_init(void);
        esp_err_t sup_rc = esp_supplicant_init();
        RADIO_DBG("espradio: esp_supplicant_init -> %d\n", (int)sup_rc);
    }
    return ret;
}

void espradio_wifi_init_completed(void) {
    wifi_init_completed();
    RADIO_DBG("espradio: wifi_init_completed\n");
}

/* Minimal symbol expected by blobs (wifi_event_post in libnet80211.a).
 * In IDF this is ESP_EVENT_DECLARE_BASE(WIFI_EVENT), i.e. extern esp_event_base_t const WIFI_EVENT;
 * where esp_event_base_t = const char*. Here we provide the same definition without linking libesp_event. */
esp_event_base_t const WIFI_EVENT = "WIFI_EVENT";

__attribute__((weak)) void net80211_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    va_list args;
    va_start(args, format);
    printf("espradio net80211: ");
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

static volatile uint32_t s_phy_printf_count;

__attribute__((weak)) void phy_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    uint32_t n = s_phy_printf_count++;
    va_list args;
    va_start(args, format);
    printf("espradio phy: [%lu] ", (unsigned long)n);
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

__attribute__((weak)) void pp_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    va_list args;
    va_start(args, format);
    printf("espradio pp: ");
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

esp_err_t espradio_set_country_eu_manual(void) {
    wifi_country_t c;
    esp_err_t rc = esp_wifi_get_country(&c);
    if (rc != ESP_OK) return rc;
    c.cc[0] = 'E'; c.cc[1] = 'U'; c.cc[2] = ' ';
    c.schan = 1; c.nchan = 13;
    c.policy = WIFI_COUNTRY_POLICY_MANUAL;
    return esp_wifi_set_country(&c);
}

esp_err_t espradio_sta_set_config(const char *ssid, int ssid_len,
                                  const char *pwd, int pwd_len) {
    wifi_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    if (ssid_len > 32) ssid_len = 32;
    memcpy(cfg.sta.ssid, ssid, ssid_len);
    if (pwd_len > 64) pwd_len = 64;
    memcpy(cfg.sta.password, pwd, pwd_len);
    if (pwd_len > 0)
        cfg.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
    return esp_wifi_set_config(WIFI_IF_STA, &cfg);
}

esp_err_t espradio_ap_set_config(const char *ssid, int ssid_len,
                                 const char *pwd, int pwd_len,
                                 uint8_t channel, int auth_open) {
    wifi_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    if (ssid_len > 32) ssid_len = 32;
    memcpy(cfg.ap.ssid, ssid, ssid_len);
    cfg.ap.ssid_len = (uint8_t)ssid_len;
    if (pwd_len > 64) pwd_len = 64;
    memcpy(cfg.ap.password, pwd, pwd_len);
    cfg.ap.channel = channel ? channel : 1;
    cfg.ap.max_connection = 4;
    cfg.ap.authmode = (auth_open || pwd_len == 0) ? WIFI_AUTH_OPEN : WIFI_AUTH_WPA2_PSK;
    return esp_wifi_set_config(WIFI_IF_AP, &cfg);
}

static volatile uint32_t espradio_sniff_packets = 0;

static void espradio_promisc_rx_cb(void *buf, wifi_promiscuous_pkt_type_t type) {
    (void)buf; (void)type;
    espradio_sniff_packets++;
}

esp_err_t espradio_sniff_begin(uint8_t channel) {
    wifi_promiscuous_filter_t filter;
    filter.filter_mask = WIFI_PROMIS_FILTER_MASK_MGMT | WIFI_PROMIS_FILTER_MASK_CTRL | WIFI_PROMIS_FILTER_MASK_DATA;
    espradio_sniff_packets = 0;
    esp_err_t rc = esp_wifi_set_promiscuous(false);
    (void)rc;
    rc = esp_wifi_set_channel(channel, WIFI_SECOND_CHAN_NONE);
    if (rc != ESP_OK) return rc;
    rc = esp_wifi_set_promiscuous_filter(&filter);
    if (rc != ESP_OK) return rc;
    rc = esp_wifi_set_promiscuous_rx_cb(espradio_promisc_rx_cb);
    if (rc != ESP_OK) return rc;
    return esp_wifi_set_promiscuous(true);
}

esp_err_t espradio_sniff_end(void) {
    return esp_wifi_set_promiscuous(false);
}

uint32_t espradio_sniff_count(void) {
    return espradio_sniff_packets;
}
