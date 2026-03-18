#include "espradio.h"
#include <string.h>

extern esp_err_t esp_wifi_internal_reg_rxcb(wifi_interface_t ifx, esp_err_t (*fn)(void *, uint16_t, void *));
extern int esp_wifi_internal_tx(wifi_interface_t wifi_if, void *buffer, uint16_t len);
extern void esp_wifi_internal_free_rx_buffer(void *buffer);
extern esp_err_t esp_wifi_get_mac(wifi_interface_t ifx, uint8_t mac[6]);

#define ESPRADIO_NETIF_RXRING_SIZE  8
#define ESPRADIO_NETIF_FRAME_MAX   1600

typedef struct {
    uint8_t  data[ESPRADIO_NETIF_FRAME_MAX];
    uint16_t len;
} espradio_rx_frame_t;

static espradio_rx_frame_t s_rx_ring[ESPRADIO_NETIF_RXRING_SIZE];
static volatile uint32_t   s_rx_head;
static volatile uint32_t   s_rx_tail;
static volatile uint32_t   s_rx_cb_count;
static volatile uint32_t   s_rx_cb_drop;

static esp_err_t espradio_sta_rxcb(void *buffer, uint16_t len, void *eb) {
    s_rx_cb_count++;
    uint32_t next = (s_rx_head + 1) % ESPRADIO_NETIF_RXRING_SIZE;
    if (next == s_rx_tail) {
        s_rx_cb_drop++;
        esp_wifi_internal_free_rx_buffer(eb);
        return 0;
    }
    uint16_t copy_len = len;
    if (copy_len > ESPRADIO_NETIF_FRAME_MAX) copy_len = ESPRADIO_NETIF_FRAME_MAX;
    memcpy(s_rx_ring[s_rx_head].data, buffer, copy_len);
    s_rx_ring[s_rx_head].len = copy_len;
    s_rx_head = next;
    esp_wifi_internal_free_rx_buffer(eb);
    return 0;
}

static wifi_interface_t s_active_if = WIFI_IF_STA;

esp_err_t espradio_netif_start_rx(int ap_mode) {
    s_active_if = ap_mode ? WIFI_IF_AP : WIFI_IF_STA;
    s_rx_head = 0;
    s_rx_tail = 0;
    return esp_wifi_internal_reg_rxcb(s_active_if, espradio_sta_rxcb);
}

int espradio_netif_rx_available(void) {
    return s_rx_head != s_rx_tail;
}

uint16_t espradio_netif_rx_pop(void *dst, uint16_t dst_len) {
    if (s_rx_head == s_rx_tail) return 0;
    uint16_t len = s_rx_ring[s_rx_tail].len;
    if (len > dst_len) len = dst_len;
    memcpy(dst, s_rx_ring[s_rx_tail].data, len);
    s_rx_tail = (s_rx_tail + 1) % ESPRADIO_NETIF_RXRING_SIZE;
    return len;
}

int espradio_netif_tx(void *buf, uint16_t len) {
    return esp_wifi_internal_tx(s_active_if, buf, len);
}

esp_err_t espradio_netif_get_mac(uint8_t mac[6]) {
    return esp_wifi_get_mac(s_active_if, mac);
}

uint32_t espradio_netif_rx_cb_count(void) { return s_rx_cb_count; }
uint32_t espradio_netif_rx_cb_drop(void)  { return s_rx_cb_drop; }
