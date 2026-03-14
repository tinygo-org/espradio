//go:build esp32c3

#include "include.h"
#include <string.h>

#ifndef ESPRADIO_WIFI_AP_DEBUG
#define ESPRADIO_WIFI_AP_DEBUG 0
#endif

static esp_err_t espradio_ap_set_country_eu(void) {
    wifi_country_t c;
    esp_err_t rc = esp_wifi_get_country(&c);
    if (rc != ESP_OK) return rc;
    c.cc[0] = 'E'; c.cc[1] = 'U'; c.cc[2] = ' ';
    c.schan = 1; c.nchan = 13;
    c.policy = WIFI_COUNTRY_POLICY_MANUAL;
    return esp_wifi_set_country(&c);
}

esp_err_t espradio_start_ap_impl(const char* ssid, size_t ssid_len,
    const char* password, size_t pwd_len, uint8_t channel, int auth_open) {
    if (esp_wifi_set_mode(WIFI_MODE_AP) != ESP_OK) return ESP_FAIL;
    wifi_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    if (ssid_len > 32) ssid_len = 32;
    memcpy(cfg.ap.ssid, ssid, ssid_len);
    cfg.ap.ssid_len = (uint8_t)ssid_len;
    cfg.ap.channel = (channel == 0) ? 6 : channel;
    cfg.ap.ssid_hidden = 0;
    cfg.ap.max_connection = 4;
    cfg.ap.beacon_interval = 100;
    if (auth_open) {
        cfg.ap.authmode = WIFI_AUTH_OPEN;
    } else {
        cfg.ap.authmode = WIFI_AUTH_WPA2_PSK;
        if (pwd_len > 64) pwd_len = 64;
        memcpy(cfg.ap.password, password, pwd_len);
    }
    esp_err_t rc = esp_wifi_set_config(WIFI_IF_AP, &cfg);
    if (rc != ESP_OK) return rc;
    rc = esp_wifi_start();
    if (rc != ESP_OK) return rc;
    return espradio_ap_set_country_eu();
}
