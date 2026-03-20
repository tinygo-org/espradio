#include "espradio.h"
#include <string.h>

extern esp_err_t esp_wifi_internal_reg_rxcb(wifi_interface_t ifx, esp_err_t (*fn)(void *, uint16_t, void *));
extern int esp_wifi_internal_tx(wifi_interface_t wifi_if, void *buffer, uint16_t len);
extern void esp_wifi_internal_free_rx_buffer(void *buffer);
extern esp_err_t esp_wifi_get_mac(wifi_interface_t ifx, uint8_t mac[6]);

/* The blob calls through s_netstack_ref / s_netstack_free function-pointer
 * variables after TX to manage upper-layer buffers.  We use copy semantics
 * (esp_wifi_internal_tx), so no-ops are correct.  Register via the blob's own
 * API so both pointers are written inside the blob. */
extern esp_err_t esp_wifi_internal_reg_netstack_buf_cb(
    void (*ref)(void *), void (*free)(void *));

static void netstack_buf_ref_noop(void *buf)  { (void)buf; }
static void netstack_buf_free_noop(void *buf)  { (void)buf; }

/* Several blob-internal function-pointer variables live at fixed DRAM
 * addresses (assigned in the linker script).  They start as zero.  If the
 * blob calls through any of them before someone writes a valid address,
 * we get pc:nil.  Write a safe no-op into every callback-shaped variable
 * that the blob might call unconditionally. */
extern void (*g_config_func)(void);
extern void (*g_net80211_tx_func)(void);
extern void (*g_timer_func)(void);
extern void (*s_michael_mic_failure_cb)(void);
extern void (*wifi_sta_rx_probe_req)(void);
extern void (*g_tx_done_cb_func)(void);
extern void (*s_encap_amsdu_func)(void);
extern void (*mesh_rxcb)(void);

static void blob_cb_noop(void) { }

/* pp_wdev_funcs relocation: the ROM ppTask dispatcher calls through a
 * heap-allocated function-pointer table (pp_wdev_funcs, 196 entries = 0x310
 * bytes).  DMA corruption can zero heap entries at runtime → pc:nil crash.
 * After esp_wifi_start() we copy the table into static .bss and redirect
 * the ROM pointer there, where DMA cannot reach. */
#define PP_WDEV_FUNCS_ENTRIES  196
static uint32_t s_pp_wdev_save[PP_WDEV_FUNCS_ENTRIES];

/* Forward declaration — defined below. */
static esp_err_t espradio_sta_rxcb(void *buffer, uint16_t len, void *eb);

/* Proper-signature TX-done callback (matches wifi_tx_done_cb_t). */
typedef void (*wifi_tx_done_cb_t)(uint8_t ifidx, uint8_t *data,
                                  uint16_t *data_len, bool txStatus);
extern esp_err_t esp_wifi_set_tx_done_cb(wifi_tx_done_cb_t cb);

static void espradio_tx_done_noop(uint8_t ifidx, uint8_t *data,
                                  uint16_t *data_len, bool txStatus) {
    (void)ifidx; (void)data; (void)data_len; (void)txStatus;
}

static void espradio_patch_blob_cb_vars(void) {
    /* Patch all callback-shaped blob variables that might still be NULL. */
    if (!g_config_func)             g_config_func = blob_cb_noop;
    if (!g_net80211_tx_func)        g_net80211_tx_func = blob_cb_noop;
    if (!g_timer_func)              g_timer_func = blob_cb_noop;
    if (!s_michael_mic_failure_cb)  s_michael_mic_failure_cb = blob_cb_noop;
    if (!wifi_sta_rx_probe_req)     wifi_sta_rx_probe_req = blob_cb_noop;
    if (!g_tx_done_cb_func)         g_tx_done_cb_func = blob_cb_noop;
    if (!s_encap_amsdu_func)        s_encap_amsdu_func = blob_cb_noop;
    if (!mesh_rxcb)                 mesh_rxcb = blob_cb_noop;
}

void espradio_netif_init_netstack_cb(void) {
    esp_wifi_internal_reg_netstack_buf_cb(netstack_buf_ref_noop,
                                          netstack_buf_free_noop);
    espradio_patch_blob_cb_vars();
}

/* Called after esp_wifi_start(): re-patch any DRAM variables that
 * the blob may have reset, register the TX-done callback via the
 * official API, and register an AP-mode RX callback. */
void espradio_post_start_cb(void) {
    espradio_patch_blob_cb_vars();
    esp_wifi_set_tx_done_cb(espradio_tx_done_noop);
    /* Disable power save — the blob's PM code (pm_tbtt_process) calls through
     * OSI function pointers in ways that can crash without a full FreeRTOS
     * environment.  Matches the Rust esp-wifi approach. */
    esp_wifi_set_ps(WIFI_PS_NONE);
    /* Register AP rxcb too (blob may call it even in STA mode). */
    esp_wifi_internal_reg_rxcb(WIFI_IF_AP, espradio_sta_rxcb);

    /* Check whether the blob moved g_osi_funcs_p away from our table. */
    extern wifi_osi_funcs_t espradio_osi_funcs;
    extern wifi_osi_funcs_t *g_osi_funcs_p;
    extern wifi_osi_funcs_t g_wifi_osi_funcs;
    extern wifi_osi_funcs_t *s_heap_osi_funcs;

    /* If blob reset g_osi_funcs_p to &g_wifi_osi_funcs, redirect to heap copy. */
    if (s_heap_osi_funcs) {
        memcpy(s_heap_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));
        memcpy(&g_wifi_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));
        g_osi_funcs_p = s_heap_osi_funcs;
    }

    /* Relocate pp_wdev_funcs from the heap (DMA-corruptible) to a static
     * .bss buffer where DMA cannot reach. */
    {
        extern volatile uint32_t *pp_wdev_funcs;
        if (pp_wdev_funcs) {
            volatile uint32_t *heap_buf = pp_wdev_funcs;
            for (int i = 0; i < PP_WDEV_FUNCS_ENTRIES; i++)
                s_pp_wdev_save[i] = heap_buf[i];
            pp_wdev_funcs = (volatile uint32_t *)s_pp_wdev_save;
        }
    }
}

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
