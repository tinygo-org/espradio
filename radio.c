//go:build esp32c3

#include "sdkconfig.h"
#include "include.h"
#include <stdarg.h>

/*
 * Typical IDF flow (esp_wifi_init in wifi_init.c):
 *   1. esp_wifi_set_log_level()           — in Go before init
 *   2. esp_wifi_power_domain_on()         — adapter, optional
 *   3. esp_wifi_init_internal(config)     — see below
 *   4. esp_phy_modem_init()               — not present in blobs
 *   5. esp_supplicant_init()              — not called (linking WPA sources pulls many deps)
 *   6. s_wifi_inited / wifi_init_completed()
 *
 * esp_wifi_init_internal (implemented in libnet80211.a, no sources):
 *   According to esp_private/wifi.h: allocates resources for the driver — control structure,
 *   RX/TX buffers, WiFi NVS, etc. Must be called before any other WiFi API. Internally (from logs)
 *   it creates a queue, semaphores, and starts the wifi driver task (worker) that processes
 *   cmd 6 (set_log?), cmd 15 (init step). Return: ESP_OK (0) or an error code; the blob sometimes
 *   returns a DRAM address (0x3FC8xxxx) — in Go we treat that as success (isPointerLike).
 */
extern void wifi_init_completed(void);  /* libnet80211.a */

extern wifi_osi_funcs_t espradio_osi_funcs;

/* Stub for the WIFI_INIT_CONFIG_DEFAULT() macro; we actually use espradio_osi_funcs below */
wifi_osi_funcs_t g_wifi_osi_funcs = {0};

const wpa_crypto_funcs_t g_wifi_default_wpa_crypto_funcs = {
    .size = sizeof(wpa_crypto_funcs_t),
    .version = ESP_WIFI_CRYPTO_VERSION,
};

esp_err_t espradio_wifi_init(void) {
    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    cfg.osi_funcs = &espradio_osi_funcs;
    cfg.nvs_enable = 0;

    esp_err_t ret = esp_wifi_init_internal(&cfg);
    if (ret != 0 && (ret < 0x3FC00000 || ret > 0x40400000)) {
        return ret;
    }

    /* wifi_init_completed() is called from Go after a delay, when the worker has already processed cmd 15 */
    return ret;
}

void espradio_wifi_init_completed(void) {
    wifi_init_completed();
}

/* Minimal symbol expected by blobs (wifi_event_post in libnet80211.a).
 * In IDF this is ESP_EVENT_DECLARE_BASE(WIFI_EVENT), i.e. extern esp_event_base_t const WIFI_EVENT;
 * where esp_event_base_t = const char*. Here we provide the same definition without linking libesp_event. */
esp_event_base_t const WIFI_EVENT = "WIFI_EVENT";

void net80211_printf(const char *format, ...) {
    va_list args;
    va_start(args, format);
    printf("espradio net80211: ");
    vprintf(format, args);
    va_end(args);
}

void phy_printf(const char *format, ...) {
    va_list args;
    va_start(args, format);
    printf("espradio phy: ");
    vprintf(format, args);
    va_end(args);
}

void pp_printf(const char *format, ...) {
    va_list args;
    va_start(args, format);
    printf("espradio pp: ");
    vprintf(format, args);
    va_end(args);
}
