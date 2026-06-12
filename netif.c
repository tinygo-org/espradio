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

/* net80211_funcs relocation: same DMA corruption issue as pp_wdev_funcs.
 * The blob allocates this table on the heap; we relocate to static .bss.
 * net80211_funcs_init writes ~44 entries; 128 provides safe headroom. */
#define NET80211_FUNCS_MAX_ENTRIES  128
static uint32_t s_net80211_funcs_save[NET80211_FUNCS_MAX_ENTRIES];

/* g_phyFuns relocation: the PHY function table lives at a fixed DRAM address
 * (0x3fcef3d4, size 0x298 = 166 words) that can be corrupted at runtime —
 * entries are overwritten with arena allocation addresses, causing
 * InstructionFetchError when the PHY calibration timer reads temperature.
 * Relocate to static .bss after init and redirect g_phyFuns. */
#define PHY_FUNCS_TABLE_WORDS  166
static uint32_t s_phyFuns_save[PHY_FUNCS_TABLE_WORDS];
extern void *g_phyFuns;

/* ppCheckTxConnTrafficIdle is called only by PM timer callbacks (pm_dream,
 * pm_go_to_wake, pm_send_probe_stop, pm_update_params, etc.).  It walks
 * TX frame descriptors in the lmac/pTxRx pools, which live in SRAM2 and
 * may be in an inconsistent state under cooperative scheduling (the blob
 * expects ppTask to run preemptively and finalise frame descriptors before
 * PM callbacks access them).  With WIFI_PS_NONE the return value is
 * irrelevant — wrap it to always return 0 ("idle") and avoid the crash. */
int __wrap_ppCheckTxConnTrafficIdle(void) { return 0; }

/* ROM-fixed pointer variables in the 0x3fcef9xx region that are critical for
 * TX operations.  WiFi DMA can corrupt these the same way it corrupts
 * pp_wdev_funcs.  We snapshot them after the blob finishes init and restore
 * before every schedOnce / TX to prevent stale-DMA-induced crashes. */
extern volatile uint32_t *pTxRx;       /* 0x3fcef954 – set by pp_attach */
extern volatile uint32_t *our_tx_eb;   /* 0x3fcef948 */
extern volatile uint32_t *our_wait_eb; /* 0x3fcef94c */
extern volatile uint32_t *lmacConfMib_ptr; /* 0x3fcef950 */
extern wifi_osi_funcs_t *g_osi_funcs_p;   /* 0x3fcef940 */

static uint32_t s_saved_pTxRx;
static uint32_t s_saved_our_tx_eb;
static uint32_t s_saved_our_wait_eb;
static uint32_t s_saved_lmacConfMib_ptr;
static uint32_t s_saved_g_osi_funcs_p;
static int      s_rom_ptrs_saved;

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
    
    /* Missing initialization steps required before esp_wifi_start() */
    esp_wifi_set_mode(WIFI_MODE_NULL);
    esp_wifi_set_tx_done_cb(espradio_tx_done_noop);
    esp_wifi_internal_reg_rxcb(WIFI_IF_STA, espradio_sta_rxcb);
    esp_wifi_internal_reg_rxcb(WIFI_IF_AP, espradio_sta_rxcb);
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

    /* Relocate net80211_funcs from the heap to static .bss. */
    {
        extern uint32_t *net80211_funcs;
        if (net80211_funcs) {
            uint32_t *heap_buf = net80211_funcs;
            for (int i = 0; i < NET80211_FUNCS_MAX_ENTRIES; i++)
                s_net80211_funcs_save[i] = heap_buf[i];
            net80211_funcs = s_net80211_funcs_save;
        }
    }

    /* Relocate g_phyFuns table from fixed DRAM to static .bss. */
    if (g_phyFuns) {
        uint32_t *rom_table = (uint32_t *)g_phyFuns;
        for (int i = 0; i < PHY_FUNCS_TABLE_WORDS; i++)
            s_phyFuns_save[i] = rom_table[i];
        g_phyFuns = s_phyFuns_save;
    }
}

/* Snapshot the critical ROM pointers after the blob has fully initialised
 * them (pp_attach sets pTxRx, lmacInit sets lmacConfMib_ptr, etc.).
 * Called from Go after pumping schedOnce enough times for ppTask to
 * process the START command. */
void espradio_save_rom_ptrs(void) {
    s_saved_pTxRx          = (uint32_t)(uintptr_t)pTxRx;
    s_saved_our_tx_eb      = (uint32_t)(uintptr_t)our_tx_eb;
    s_saved_our_wait_eb    = (uint32_t)(uintptr_t)our_wait_eb;
    s_saved_lmacConfMib_ptr = (uint32_t)(uintptr_t)lmacConfMib_ptr;
    s_saved_g_osi_funcs_p  = (uint32_t)(uintptr_t)g_osi_funcs_p;
    s_rom_ptrs_saved = 1;
}

/* Restore the ROM pointers from snapshot.  Called from schedOnce (Go side)
 * and before every TX to undo any DMA corruption in the ROM data area. */
void espradio_restore_rom_ptrs(void) {
    if (!s_rom_ptrs_saved) return;
    if ((uint32_t)(uintptr_t)pTxRx != s_saved_pTxRx)
        pTxRx = (volatile uint32_t *)(uintptr_t)s_saved_pTxRx;
    if ((uint32_t)(uintptr_t)our_tx_eb != s_saved_our_tx_eb)
        our_tx_eb = (volatile uint32_t *)(uintptr_t)s_saved_our_tx_eb;
    if ((uint32_t)(uintptr_t)our_wait_eb != s_saved_our_wait_eb)
        our_wait_eb = (volatile uint32_t *)(uintptr_t)s_saved_our_wait_eb;
    if ((uint32_t)(uintptr_t)lmacConfMib_ptr != s_saved_lmacConfMib_ptr)
        lmacConfMib_ptr = (volatile uint32_t *)(uintptr_t)s_saved_lmacConfMib_ptr;
    if ((uint32_t)(uintptr_t)g_osi_funcs_p != s_saved_g_osi_funcs_p)
        g_osi_funcs_p = (wifi_osi_funcs_t *)(uintptr_t)s_saved_g_osi_funcs_p;
    /* esp_phy_enable resets g_phyFuns to the fixed DRAM address on every call.
     * Redirect it back to our static copy. */
    if (g_phyFuns != s_phyFuns_save && s_phyFuns_save[0] != 0)
        g_phyFuns = s_phyFuns_save;
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
static volatile int s_sta_connected;

void espradio_netif_set_connected(int connected) {
    s_sta_connected = connected;
}

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
    if (s_active_if == WIFI_IF_STA && !s_sta_connected) return ESP_ERR_WIFI_NOT_CONNECT;
    espradio_restore_rom_ptrs();
    return esp_wifi_internal_tx(s_active_if, buf, len);
}

esp_err_t espradio_netif_get_mac(uint8_t mac[6]) {
    return esp_wifi_get_mac(s_active_if, mac);
}

uint32_t espradio_netif_rx_cb_count(void) { return s_rx_cb_count; }
uint32_t espradio_netif_rx_cb_drop(void)  { return s_rx_cb_drop; }
