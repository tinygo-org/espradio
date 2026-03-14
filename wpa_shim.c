//go:build esp32c3

#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>

#include "include.h"

/*
 * Minimal WPA callback registration for Wi-Fi scanning.
 *
 * The blob (libnet80211.a) uses a callback table (struct wpa_funcs, 108 bytes / 27 pointers)
 * registered via esp_wifi_register_wpa_cb_internal(). Without it, wpa_parse_wpa_ie is NULL
 * and beacon parsing is skipped → 0 APs in scan results.
 *
 * Full esp_supplicant_init() pulls in heavy deps (mbedtls, eloop, etc.) that crash
 * in TinyGo's runtime. Instead we register only the minimal callback needed for scanning.
 *
 * struct wpa_funcs field layout (from libwpa_supplicant.a relocations):
 *   offset  0: wpa_sta_init
 *   offset  4: wpa_sta_deinit
 *   offset  8: wpa_sta_connect
 *   offset 12: wpa_sta_connected_cb
 *   offset 16: wpa_sta_disconnected_cb
 *   offset 20: wpa_sta_rx_eapol
 *   offset 24: wpa_sta_in_4way_handshake
 *   offset 28: wpa_ap_init
 *   offset 32: wpa_ap_deinit
 *   offset 36: wpa_ap_join
 *   offset 40: wpa_ap_remove
 *   offset 44: wpa_ap_get_wpa_ie
 *   offset 48: wpa_ap_rx_eapol
 *   offset 52: wpa_ap_get_peer_spp_msg
 *   offset 56: wpa_config_parse_string
 *   offset 60: wpa_parse_wpa_ie          ← needed for scan
 *   offset 64: wpa_config_bss
 *   offset 68: wpa_michael_mic_failure
 *   ...
 *   total size: 108 bytes (27 pointers)
 */

#define WPA_FUNCS_SIZE   108

/* offsets within struct wpa_funcs (determined from libwpa_supplicant.a relocations) */
#define OFF_STA_INIT        0
#define OFF_STA_DEINIT      4
#define OFF_STA_CONNECT     8
#define OFF_STA_CONNECTED   12
#define OFF_STA_DISCONNECTED 16
#define OFF_STA_RX_EAPOL    20
#define OFF_STA_IN_4WAY     24
#define OFF_AP_INIT         28
#define OFF_AP_DEINIT       32
#define OFF_PARSE_WPA_IE    60
#define OFF_CONFIG_DONE     88

#define WPA_PROTO_WPA  1
#define WPA_PROTO_RSN  2
#define WPA_IE_TAG     0xDD
#define RSN_IE_TAG     0x30

static const uint8_t WPA_OUI[] = { 0x00, 0x50, 0xF2, 0x01 };

typedef struct {
    int proto;
    int pairwise_cipher;
    int group_cipher;
    int key_mgmt;
    int capabilities;
    const uint8_t *pmkid;
    int mgmt_group_cipher;
    int rsnxe_capa;
} wifi_wpa_ie_t;

/* --- stub callbacks the blob calls through wpa_cb --- */

static int stub_sta_init(void) {
    printf("espradio: wpa_sta_init (stub)\n");
    return 1; /* true = success */
}

static int stub_sta_deinit(void) {
    return 1;
}

static void stub_nop(void) {}
static int  stub_zero(void) { return 0; }

static int espradio_parse_wpa_ie(const uint8_t *ie, size_t ie_len, wifi_wpa_ie_t *out) {
    memset(out, 0, sizeof(*out));
    if (!ie || ie_len < 2) return -1;

    uint8_t tag = ie[0];

    if (tag == RSN_IE_TAG) {
        out->proto = WPA_PROTO_RSN;
        out->pairwise_cipher = 4; /* CCMP */
        out->group_cipher    = 4;
        out->key_mgmt        = 2; /* WPA_KEY_MGMT_PSK */
        return 0;
    }

    if (tag == WPA_IE_TAG && ie_len >= 6 && memcmp(ie + 2, WPA_OUI, 4) == 0) {
        out->proto = WPA_PROTO_WPA;
        out->pairwise_cipher = 2; /* TKIP */
        out->group_cipher    = 2;
        out->key_mgmt        = 2;
        return 0;
    }

    return -1;
}

/* --- registration --- */

extern void esp_wifi_register_wpa_cb_internal(void *wpa_cb);

static void set_cb(uint8_t *table, int offset, void *fn) {
    memcpy(table + offset, &fn, sizeof(fn));
}

static uint8_t s_wpa_cb[WPA_FUNCS_SIZE];

void espradio_wpa_register(void) {
    memset(s_wpa_cb, 0, sizeof(s_wpa_cb));

    set_cb(s_wpa_cb, OFF_STA_INIT,       (void *)stub_sta_init);
    set_cb(s_wpa_cb, OFF_STA_DEINIT,     (void *)stub_sta_deinit);
    set_cb(s_wpa_cb, OFF_STA_CONNECT,    (void *)stub_zero);
    set_cb(s_wpa_cb, OFF_STA_CONNECTED,  (void *)stub_nop);
    set_cb(s_wpa_cb, OFF_STA_DISCONNECTED,(void *)stub_nop);
    set_cb(s_wpa_cb, OFF_STA_RX_EAPOL,   (void *)stub_zero);
    set_cb(s_wpa_cb, OFF_STA_IN_4WAY,    (void *)stub_zero);
    set_cb(s_wpa_cb, OFF_AP_INIT,        (void *)stub_zero);
    set_cb(s_wpa_cb, OFF_AP_DEINIT,      (void *)stub_nop);
    set_cb(s_wpa_cb, OFF_PARSE_WPA_IE,   (void *)espradio_parse_wpa_ie);
    set_cb(s_wpa_cb, OFF_CONFIG_DONE,    (void *)stub_nop);

    esp_wifi_register_wpa_cb_internal(s_wpa_cb);
    printf("espradio: wpa_cb registered (11 callbacks)\n");
}
