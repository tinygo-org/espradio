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

/* DMA-corruption workaround tables: relocate heap-allocated function-pointer
 * tables to static .bss where DMA cannot reach.  Only needed on ESP32-C3/S3;
 * on the original ESP32 the blob manages these tables itself and the entry
 * counts differ, so these are compiled out to save ~1.9 KB BSS. */
#define PP_WDEV_FUNCS_ENTRIES  196
#define NET80211_FUNCS_MAX_ENTRIES  128
#define PHY_FUNCS_TABLE_WORDS  166
#if !CONFIG_IDF_TARGET_ESP32
static uint32_t s_pp_wdev_save[PP_WDEV_FUNCS_ENTRIES];
static uint32_t s_net80211_funcs_save[NET80211_FUNCS_MAX_ENTRIES];
static uint32_t s_phyFuns_save[PHY_FUNCS_TABLE_WORDS];
#endif
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

/* The blob calls this once it is done with a transmitted frame, which is also
 * when it releases the TX buffer.  Latching it is what lets a future TX retry
 * wait on evidence the blob ran, rather than on a capacity query that reads true
 * immediately and proves nothing. */
static volatile uint32_t s_tx_done_cb_count;

static void espradio_tx_done_noop(uint8_t ifidx, uint8_t *data,
                                  uint16_t *data_len, bool txStatus) {
    (void)ifidx; (void)data; (void)data_len; (void)txStatus;
    s_tx_done_cb_count++;
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

#if !CONFIG_IDF_TARGET_ESP32
    /* The following table relocations are DMA-corruption workarounds calibrated
     * for the ESP32-S3/C3 blobs (fixed entry counts and the g_phyFuns table
     * size/location).  On the original ESP32 the blob's table layouts differ,
     * so copying S3-sized tables over-reads adjacent heap memory and captures
     * garbage pointers — ppTask then jumps into the arena (InstructionFetchError
     * at a DRAM address).  Skip them for ESP32; the blob manages these tables
     * itself. */

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
#endif /* !CONFIG_IDF_TARGET_ESP32 */
}

/* ROM pointer snapshot / restore.
 *
 * These five pointers live at fixed addresses the blob sets during START
 * processing (pp_attach sets pTxRx, lmacInit sets lmacConfMib_ptr, ...) and that
 * WiFi DMA can corrupt afterwards, so they are snapshotted once and rewritten on
 * every scheduler pass and before every TX.
 *
 * The snapshot used to be unconditional, taken after a fixed number of scheduler
 * passes on the assumption that the blob was done by then.  Measured on hardware,
 * it is not: on ESP32-C3 and ESP32-S3 at least one pointer is still NULL after
 * all 40 passes (ESP32 has them after one).  The snapshot then captured that NULL
 * and the restore faithfully wrote it back over whatever the blob had since set,
 * tens of thousands of times a second.
 *
 * So each pointer is now latched independently, and only once it is non-NULL.
 * NULL is never captured and never restored.  That makes the pump length a
 * performance question rather than a correctness one: a short pump just means a
 * pointer gets latched by a later call instead of the first.  s_rom_ptrs_valid
 * records which have been latched, so "which pointer is late" is answerable
 * rather than inferred. */
#define ROM_PTR_TXRX       (1u << 0)
#define ROM_PTR_TX_EB      (1u << 1)
#define ROM_PTR_WAIT_EB    (1u << 2)
#define ROM_PTR_LMAC_MIB   (1u << 3)
#define ROM_PTR_OSI_FUNCS  (1u << 4)
#define ROM_PTR_ALL        (ROM_PTR_TXRX | ROM_PTR_TX_EB | ROM_PTR_WAIT_EB | \
                            ROM_PTR_LMAC_MIB | ROM_PTR_OSI_FUNCS)

static volatile uint32_t s_rom_ptrs_valid;

/* True once every pointer has been latched. */
int espradio_rom_ptrs_ready(void) {
    return (s_rom_ptrs_valid & ROM_PTR_ALL) == ROM_PTR_ALL;
}

/* Bitmask of pointers still not latched, using the ROM_PTR_* bits above.  Zero
 * means all are latched. */
uint32_t espradio_rom_ptrs_missing(void) {
    return (~s_rom_ptrs_valid) & ROM_PTR_ALL;
}

/* Counts calls that could not latch everything, i.e. the blob had not finished. */
static volatile uint32_t s_rom_ptrs_saved_unready;

uint32_t espradio_rom_ptrs_saved_unready(void) { return s_rom_ptrs_saved_unready; }

/* Latch one pointer if it is non-NULL and not already latched. */
static void rom_ptr_latch(uint32_t *slot, uint32_t bit, uint32_t current) {
    if (s_rom_ptrs_valid & bit) return;
    if (current == 0) return;
    *slot = current;
    s_rom_ptrs_valid |= bit;
}

/* Safe to call repeatedly: each pointer latches the first time it is valid. */
void espradio_save_rom_ptrs(void) {
    rom_ptr_latch(&s_saved_pTxRx, ROM_PTR_TXRX,
                  (uint32_t)(uintptr_t)pTxRx);
    rom_ptr_latch(&s_saved_our_tx_eb, ROM_PTR_TX_EB,
                  (uint32_t)(uintptr_t)our_tx_eb);
    rom_ptr_latch(&s_saved_our_wait_eb, ROM_PTR_WAIT_EB,
                  (uint32_t)(uintptr_t)our_wait_eb);
    rom_ptr_latch(&s_saved_lmacConfMib_ptr, ROM_PTR_LMAC_MIB,
                  (uint32_t)(uintptr_t)lmacConfMib_ptr);
    rom_ptr_latch(&s_saved_g_osi_funcs_p, ROM_PTR_OSI_FUNCS,
                  (uint32_t)(uintptr_t)g_osi_funcs_p);

    if (!espradio_rom_ptrs_ready()) s_rom_ptrs_saved_unready++;
    if (s_rom_ptrs_valid) s_rom_ptrs_saved = 1;
}

/* Restore the ROM pointers from snapshot.  Called from schedOnce (Go side)
 * and before every TX to undo any DMA corruption in the ROM data area.
 *
 * Only latched pointers are restored, so a pointer the blob has not set yet is
 * left alone rather than being pinned to NULL. */
void espradio_restore_rom_ptrs(void) {
    if (!s_rom_ptrs_saved) return;
    if ((s_rom_ptrs_valid & ROM_PTR_TXRX) &&
        (uint32_t)(uintptr_t)pTxRx != s_saved_pTxRx)
        pTxRx = (volatile uint32_t *)(uintptr_t)s_saved_pTxRx;
    if ((s_rom_ptrs_valid & ROM_PTR_TX_EB) &&
        (uint32_t)(uintptr_t)our_tx_eb != s_saved_our_tx_eb)
        our_tx_eb = (volatile uint32_t *)(uintptr_t)s_saved_our_tx_eb;
    if ((s_rom_ptrs_valid & ROM_PTR_WAIT_EB) &&
        (uint32_t)(uintptr_t)our_wait_eb != s_saved_our_wait_eb)
        our_wait_eb = (volatile uint32_t *)(uintptr_t)s_saved_our_wait_eb;
    if ((s_rom_ptrs_valid & ROM_PTR_LMAC_MIB) &&
        (uint32_t)(uintptr_t)lmacConfMib_ptr != s_saved_lmacConfMib_ptr)
        lmacConfMib_ptr = (volatile uint32_t *)(uintptr_t)s_saved_lmacConfMib_ptr;
    if ((s_rom_ptrs_valid & ROM_PTR_OSI_FUNCS) &&
        (uint32_t)(uintptr_t)g_osi_funcs_p != s_saved_g_osi_funcs_p)
        g_osi_funcs_p = (wifi_osi_funcs_t *)(uintptr_t)s_saved_g_osi_funcs_p;
    /* esp_phy_enable resets g_phyFuns to the fixed DRAM address on every call.
     * Redirect it back to our static copy (C3/S3 only). */
#if !CONFIG_IDF_TARGET_ESP32
    if (g_phyFuns != s_phyFuns_save && s_phyFuns_save[0] != 0)
        g_phyFuns = s_phyFuns_save;
#endif
}

#define ESPRADIO_NETIF_RXRING_SIZE  6
#define ESPRADIO_NETIF_FRAME_MAX   1600

typedef struct {
    uint8_t  data[ESPRADIO_NETIF_FRAME_MAX];
    uint16_t len;
} espradio_rx_frame_t;

/* On ESP32, place the RX ring in the dedicated DRAM1 region (SRAM1 pool 7/6)
 * to free ~9.6 KB of SRAM2 for the Go GC heap.  The linker script's .rxring
 * section sits before the arena in DRAM1.  On other targets the ring stays in
 * normal .bss. */
#if CONFIG_IDF_TARGET_ESP32
static espradio_rx_frame_t s_rx_ring[ESPRADIO_NETIF_RXRING_SIZE] __attribute__((section(".rxring")));
#else
static espradio_rx_frame_t s_rx_ring[ESPRADIO_NETIF_RXRING_SIZE];
#endif
static volatile uint32_t   s_rx_head;
static volatile uint32_t   s_rx_tail;
static volatile uint32_t   s_rx_cb_count;
static volatile uint32_t   s_rx_cb_drop;

/* Producer side of the RX ring.  On ESP32-C3 this runs in real interrupt context
 * (the hardware handler calls the blob ISR directly), so the head is published
 * only after a barrier -- the same discipline as the ISR ring in isr.c, which the
 * original code here was missing.
 *
 * The two counters stay plain increments and are therefore APPROXIMATE: RV32IMC
 * has no atomic extension, so making them exact would mean a critical section per
 * received frame.  They are diagnostics; an occasional lost increment when the
 * consumer is interrupted mid-read-modify-write is an acceptable trade for not
 * touching the interrupt mask on every RX.  The ring indices, which must be
 * exact, are single-writer on each side and need no read-modify-write. */
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
    /* Frame and length must be visible before the consumer can see the slot. */
    ESPRADIO_MEMORY_BARRIER();
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
    /* Do NOT reset head/tail here.  The rxcb is registered at init
     * (espradio_netif_init_netstack_cb) and may already be filling the ring, in
     * ISR context on the C3; zeroing both indices underneath a live producer
     * either loses queued frames or resurrects stale ones.  Draining the ring is
     * the consumer's job and it is already safe to start mid-stream. */
    return esp_wifi_internal_reg_rxcb(s_active_if, espradio_sta_rxcb);
}

int espradio_netif_rx_available(void) {
    return s_rx_head != s_rx_tail;
}

/* Frames dropped because they did not fit the caller's buffer. */
static volatile uint32_t s_rx_oversize;

uint32_t espradio_netif_rx_oversize(void) { return s_rx_oversize; }

uint16_t espradio_netif_rx_pop(void *dst, uint16_t dst_len) {
    if (s_rx_head == s_rx_tail) return 0;
    /* Pair with the producer's publish barrier before reading the slot. */
    ESPRADIO_MEMORY_BARRIER();
    uint16_t len = s_rx_ring[s_rx_tail].len;

    /* Drop the whole frame rather than truncating it.  The ring holds up to
     * ESPRADIO_NETIF_FRAME_MAX (1600) while the usual consumer buffer is 1522, so
     * truncation is reachable -- and a short frame is not a smaller version of the
     * original, it is a corrupt one that the upper stack will mis-parse.
     * TestVHCIRingOverflowTruncatesPacket documented this same bug class for the
     * VHCI ring and named this as the fix.  Consuming the slot is deliberate: the
     * frame will never fit, so leaving it would wedge the ring. */
    if (len > dst_len) {
        s_rx_oversize++;
        ESPRADIO_MEMORY_BARRIER();
        s_rx_tail = (s_rx_tail + 1) % ESPRADIO_NETIF_RXRING_SIZE;
        return 0;
    }

    memcpy(dst, s_rx_ring[s_rx_tail].data, len);
    ESPRADIO_MEMORY_BARRIER();
    s_rx_tail = (s_rx_tail + 1) % ESPRADIO_NETIF_RXRING_SIZE;
    return len;
}

/* TX accounting.  A failed esp_wifi_internal_tx is a lost frame: the caller has
 * already dequeued it from the upper stack's egress buffer, so there is nothing
 * left to retry with.  ESP_ERR_NO_MEM (blob TX buffers exhausted) is counted
 * separately from the rest because it is the recoverable case. */
static volatile uint32_t s_tx_attempts;
static volatile uint32_t s_tx_fail_nomem;
static volatile uint32_t s_tx_fail_other;
static volatile uint32_t s_tx_not_connected;

/* Retry budget for a NO_MEM rejection.  Bounded for the same reason bt_pump_hci
 * is: the signal we wait on is only promised on a transition, so an unbounded
 * wait can hang.  Small, because the caller is on the data path. */
#define ESPRADIO_TX_RETRIES     4
#define ESPRADIO_TX_PUMP_PASSES 4

static volatile uint32_t s_tx_retries;
static volatile uint32_t s_tx_busy_waits;
static volatile int      s_tx_busy;

/* Hand one frame to the blob, and do not give up on the first NO_MEM.
 *
 * Modelled on espradio_vhci_write, which had to learn the same four things:
 *
 *   - A rejection is not final.  NO_MEM means the blob's TX buffers are all in
 *     flight; it releases one when it finishes a frame, and it only does that if
 *     it gets to run.  The caller cannot retry for us -- by the time it sees the
 *     error the frame has already been dequeued from the upper stack's egress
 *     buffer and is gone -- so the retry has to happen here.
 *
 *   - Pump, do not merely yield.  Reaching the blob's TX completion path takes a
 *     scheduler pass, which is what espradio_pump_sched_once does.
 *
 *   - Wait on evidence the blob ran, not on a capacity query.  s_tx_done_cb_count
 *     is bumped by the blob's TX-done callback, which fires when it releases a
 *     buffer.  Note this is NOT an acknowledgement of our frame: measured, it
 *     fires about three times more often than our own TX calls, because the blob
 *     transmits management frames we never handed it.  "A buffer was freed" is
 *     exactly the signal a NO_MEM retry needs, though, so that is fine -- what it
 *     must not be used for is confirming delivery.
 *
 *   - Serialise writers.  The pump yields, so without a gate a second sender
 *     could enter and interleave with this one. */
int espradio_netif_tx(void *buf, uint16_t len) {
    if (s_active_if == WIFI_IF_STA && !s_sta_connected) {
        s_tx_not_connected++;
        return ESP_ERR_WIFI_NOT_CONNECT;
    }

    while (s_tx_busy) {
        s_tx_busy_waits++;
        espradio_task_yield_go();
    }
    s_tx_busy = 1;

    espradio_restore_rom_ptrs();
    s_tx_attempts++;

    int ret = esp_wifi_internal_tx(s_active_if, buf, len);
    for (int i = 0; ret == ESP_ERR_NO_MEM && i < ESPRADIO_TX_RETRIES; i++) {
        uint32_t seen = s_tx_done_cb_count;
        s_tx_retries++;

        /* Drive the blob until it reports releasing a buffer, bounded within the
         * retry as well: the callback is only promised on a transition, so a
         * buffer may come free without it and a hard wait would stall. */
        for (int p = 0; p < ESPRADIO_TX_PUMP_PASSES; p++) {
            espradio_pump_sched_once();
            if (s_tx_done_cb_count != seen) break;
        }

        espradio_restore_rom_ptrs();
        ret = esp_wifi_internal_tx(s_active_if, buf, len);
    }

    if (ret != ESP_OK) {
        if (ret == ESP_ERR_NO_MEM) {
            s_tx_fail_nomem++;
        } else {
            s_tx_fail_other++;
        }
    }

    s_tx_busy = 0;
    return ret;
}

void espradio_netif_tx_stats(uint32_t *attempts, uint32_t *fail_nomem,
                             uint32_t *fail_other, uint32_t *not_connected,
                             uint32_t *tx_done, uint32_t *retries,
                             uint32_t *busy_waits) {
    if (attempts)      *attempts      = s_tx_attempts;
    if (fail_nomem)    *fail_nomem    = s_tx_fail_nomem;
    if (fail_other)    *fail_other    = s_tx_fail_other;
    if (not_connected) *not_connected = s_tx_not_connected;
    if (tx_done)       *tx_done       = s_tx_done_cb_count;
    if (retries)       *retries       = s_tx_retries;
    if (busy_waits)    *busy_waits    = s_tx_busy_waits;
}

esp_err_t espradio_netif_get_mac(uint8_t mac[6]) {
    return esp_wifi_get_mac(s_active_if, mac);
}

uint32_t espradio_netif_rx_cb_count(void) { return s_rx_cb_count; }
uint32_t espradio_netif_rx_cb_drop(void)  { return s_rx_cb_drop; }
