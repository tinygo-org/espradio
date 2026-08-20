//go:build esp32c3 || esp32s3

/* Shared BLE controller driver for the ESP32-C3 and the ESP32-S3.
 * The chip parts are in bt_ble_esp32c3.c and bt_ble_esp32s3.c. */

#include "espradio.h"
#include "esp_coexist_internal.h"
#include "esp_bt.h"
#include "bt_ble.h"
#include "btbb.h"
#include <stdint.h>
#include <string.h>
#include <stdio.h>

#ifndef ESPRADIO_BLE_DEBUG
#define ESPRADIO_BLE_DEBUG 0
#endif

#if ESPRADIO_BLE_DEBUG
#define BLE_DBG(...) printf(__VA_ARGS__)
#else
#define BLE_DBG(...) ((void)0)
#endif

/* BT Interrupt Handling */

/* The blob calls interrupt_handler_set with interrupt_no=5 (RWBT+BT_BB) and
 * interrupt_no=8 (RWBLE). We store the handler pointers and dispatch from Go
 * interrupt handlers on the actual CPU interrupts. */

typedef void (*bt_isr_fn_t)(void *arg);

static bt_isr_fn_t s_bt_isr_fn_5;
static void       *s_bt_isr_arg_5;
static bt_isr_fn_t s_bt_isr_fn_8;
static void       *s_bt_isr_arg_8;

/* Wake the BT controller task.
 *
 * btdm_controller_task does NOT block on g_rw_schd_queue — it blocks on a
 * semaphore and only drains the queue non-blocking:
 *
 *     while (1) {
 *         if (semphr_take(btdm_ol_task_env->sem, portMAX_DELAY) == 0) break;
 *         while (queue_recv(g_rw_schd_queue, &msg, 0) == 1) { ...; btdm_rw_run(st); }
 *         if (no messages) btdm_rw_run(st);
 *     }
 *
 * So pushing an item into g_rw_schd_queue (what this used to do) never wakes
 * the task at all: it blocks on the semaphore forever and the queue item is
 * simply never looked at.  Giving the semaphore is what releases it, and even
 * with an empty queue that still reaches btdm_rw_run(s_btdm_state) -> rw_schedule(),
 * which is the call that reprograms the scheduler after an event.
 *
 * btdm_ol_task_env->sem lives at offset 8; use the blob's own accessor rather
 * than hardcoding the address. */
extern void *r_btdm_vnd_ol_task_env_get(void);
extern int32_t espradio_semphr_give(void *semphr);

static volatile uint32_t s_task_wake_count;
static volatile uint32_t s_task_wake_nosem;

static void bt_isr_wake_task(void) {
    void *env = r_btdm_vnd_ol_task_env_get();
    if (env == NULL) {
        s_task_wake_nosem++;
        return;
    }
    void *sem = *(void *volatile *)((uint8_t *)env + 8);
    if (sem == NULL) {
        s_task_wake_nosem++;
        return;
    }
    espradio_semphr_give(sem);
    s_task_wake_count++;
}

/* Rate-limited heartbeat wake for the periodic tick.
 *
 * Waking the task makes its goroutine runnable, which feeds back into
 * kickSched() and collapses the 5 ms tick into a ~30 us busy loop. The ISR path
 * above stays unthrottled (a real event must be serviced immediately); this is
 * only for the periodic keep-alive, capped at ~1 kHz. */
extern uint64_t espradio_time_us_now(void);

static void bt_wake_task_throttled(void) {
    static uint64_t s_last_wake_us;
    uint64_t now = espradio_time_us_now();
    if (now - s_last_wake_us < 1000) {
        return;
    }
    s_last_wake_us = now;
    bt_isr_wake_task();
}

/* ISR context depth for the two BT dispatchers.  Read by bt_is_in_isr() further
 * down, which the blob calls to decide between its normal and from-ISR APIs.
 * Nothing assigned this before, so that decision was made on the WiFi poll's flag
 * and was false during every actual BT interrupt. */
static volatile int s_bt_in_isr;

/* Run one registered blob ISR and then wake the controller task.
 * which is 5 for RWBT and BT_BB, 8 for RWBLE. Returns 0 when none is set. */
int espradio_bt_run_isr(int which) {
    bt_isr_fn_t fn = (which == 5) ? s_bt_isr_fn_5 : s_bt_isr_fn_8;
    void       *arg = (which == 5) ? s_bt_isr_arg_5 : s_bt_isr_arg_8;
    if (fn == NULL) {
        return 0;
    }
    s_bt_in_isr++;
    fn(arg);
    s_bt_in_isr--;
    bt_isr_wake_task();
    return 1;
}

/* Diagnostic counts for the chip layer. */
uint32_t espradio_bt_wake_gives(void) { return s_task_wake_count; }
uint32_t espradio_bt_wake_nosem(void) { return s_task_wake_nosem; }

/* VHCI Ring Buffer (controller → host) */

/* The ring itself is implemented in Go (vhci_ring.go), which is what lets the
 * unit-test target reach it without the BTDM blob. */
extern int espradio_vhci_ring_push(const uint8_t *data, int len);

static volatile int s_vhci_send_available = 1;

/* Called by ROM controller when it has HCI data for the host. */
static int vhci_host_recv_cb(uint8_t *data, uint16_t len) {
    /* Decode HCI event for debug */
    if (len >= 4 && data[0] == 0x04 && data[1] == 0x0E) {
        /* Command Complete: data[3]=num_cmds, data[4:5]=opcode, data[6]=status */
        if (len >= 7) {
            uint16_t opcode = data[4] | ((uint16_t)data[5] << 8);
            BLE_DBG("vhci_rx: CmdComplete op=0x%04x status=0x%02x\n", opcode, data[6]);
        }
    } else if (len >= 3 && data[0] == 0x04 && data[1] == 0x3E) {
        /* LE Meta Event: data[3]=subevent */
        BLE_DBG("vhci_rx: LE_Meta sub=0x%02x len=%u\n", data[3], (unsigned)len);
    } else {
        BLE_DBG("vhci_rx: len=%u type=0x%02x\n", (unsigned)len, len > 0 ? data[0] : 0);
    }
    int stored = espradio_vhci_ring_push(data, (int)len);
    if (stored < (int)len) {
        BLE_DBG("vhci_rx: drop %d bytes\n", (int)len - stored);
    }
    return 0;
}

/* Called by ROM controller when it can accept a new HCI packet. */
static void vhci_host_send_available_cb(void) {
    s_vhci_send_available = 1;
}

/* The consumer side (buffered / read_byte / read) is Go-only: see vhci_ring.go. */

/* ROM VHCI API */
extern bool API_vhci_host_check_send_available(void);
extern void API_vhci_host_send_packet(const uint8_t *data, uint16_t len);
extern int API_vhci_host_register_callback(const esp_vhci_host_callback_t *callback);

static const esp_vhci_host_callback_t s_vhci_cbs = {
    .notify_host_send_available = vhci_host_send_available_cb,
    .notify_host_recv           = vhci_host_recv_cb,
};

static volatile int s_vhci_tx_busy;

/* Defined further down, next to the scheduler tick. Drives the controller so a
 * packet just handed over is actually picked up. */
static void bt_pump_hci(void);

/* Hand one HCI packet to the controller, and do not return until the
 * controller has taken it.
 *
 * The wait at the end is the important part. The controller has a single HCI
 * input slot, and under the cooperative scheduler nothing runs it between two
 * back-to-back writes -- the 5 ms tick is far away. The host stack emits
 * LE_Set_Advertising_Enable via sendWithoutResponse (which does not wait for
 * the Command Complete) and then, on the Connect -> DiscoverServices path, the
 * first ATT request ~200 us later, both out of its single scratch buffer. The
 * second write reached the slot before the controller had read the first, so
 * the Command Complete for 0x200a never arrived and the controller -- signalled
 * twice but finding the ATT request both times -- transmitted that request
 * twice, which showed up as two Number-Of-Completed-Packets events for one
 * write. The peer received a duplicated request, never answered it, and service
 * discovery timed out.
 *
 * The yields here can reschedule, so a second writer must not enter while a
 * packet is in flight. */
int espradio_vhci_write(const uint8_t *data, int len) {
    if (len <= 0) {
        return 0;
    }

    while (s_vhci_tx_busy) {
        espradio_task_yield_go();
    }
    s_vhci_tx_busy = 1;

    /* Wait until the controller can accept a packet. */
    while (!API_vhci_host_check_send_available()) {
        espradio_task_yield_go();
    }

    BLE_DBG("vhci_tx: len=%d type=0x%02x\n", len, data[0]);
    s_vhci_send_available = 0;
    API_vhci_host_send_packet((uint8_t *)data, (uint16_t)len);
    bt_pump_hci();

    s_vhci_tx_busy = 0;
    return len;
}

/* BT OSI Function Table */

/* Forward declarations for existing WiFi OSI primitives (from osi.c / radio.go) */
extern void *espradio_semphr_create(uint32_t max, uint32_t init);
extern void  espradio_semphr_delete(void *semphr);
extern int32_t espradio_semphr_take(void *semphr, uint32_t block_time_tick);
extern int32_t espradio_semphr_give(void *semphr);
extern void *espradio_recursive_mutex_create(void);
extern void  espradio_mutex_delete(void *mutex);
extern int32_t espradio_mutex_lock(void *mutex);
extern int32_t espradio_mutex_unlock(void *mutex);
extern void *espradio_arena_alloc(size_t size);
extern void  espradio_arena_free(void *p);
extern bool  espradio_is_from_isr(void);
extern void  espradio_task_yield_go(void);
extern void  espradio_run_task(void *task_func, void *param);
extern uint64_t espradio_time_us_now(void);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);

/* ─── Queue primitives (reuse from osi.c) ─── */
extern void *espradio_queue_create_internal(uint32_t len, uint32_t item_size);
extern void  espradio_queue_delete_internal(void *queue);
extern int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
extern int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw);
extern int32_t espradio_queue_recv(void *queue, void *item, uint32_t block_time_tick);
extern int32_t espradio_queue_recv_from_isr(void *queue, void *item, void *hptw);

/* ─── ISR context tracking ───
 * s_bt_in_isr is maintained by the two BT dispatchers near the top of this file. */
static int bt_is_in_isr(void) {
    return s_bt_in_isr > 0 || espradio_is_from_isr();
}

/* ─── Interrupt disable/restore ───
 * The blob relies on these being a REAL critical section, not advisory hints.
 * r_ke_task_schedule_hack wraps its ke_env queue extraction in
 * osi_funcs[0x14]/[0x18] (= these two), and the RWBLE ISR concurrently pushes
 * onto the same ke queues via ke_msg_send_from_isr.  With no-ops here the ke
 * message list gets corrupted and event delivery silently stops.
 *
 * The chip layer owns the mechanism, because it is different on each chip. */
static void bt_interrupt_disable(void) { espradio_bt_cs_enter(); }
static void bt_interrupt_restore(void) { espradio_bt_cs_exit(); }

/* ─── Interrupt alloc/handler_set ─── */
/* Called from Go (espradio_bt_enable_interrupts) after init to enable hw ints */
extern void espradio_bt_enable_hw_interrupts(void);

static void bt_interrupt_handler_set(int interrupt_no, void (*func)(void *), void *arg);

static int bt_interrupt_alloc(int cpu_no, int intr_source,
                              void (*handler)(void *), void *arg,
                              void **ret_handle) {
    (void)cpu_no;
    (void)ret_handle;
    BLE_DBG("bt_interrupt_alloc: src=%d handler=%p\n", intr_source, handler);
    /* The blob passes the ISR handler here (not via interrupt_handler_set).
     * Map peripheral source to interrupt index and store it.
     * Source 5 (BT_BB) and 7 (RWBT) → index 5; Source 8 (RWBLE) → index 8. */
    int int_no = -1;
    if (intr_source == 5 || intr_source == 7) {
        int_no = 5;
    } else if (intr_source == 8) {
        int_no = 8;
    }
    if (int_no >= 0) {
        bt_interrupt_handler_set(int_no, handler, arg);
    }
    return 0;
}

static int bt_interrupt_free(void *handle) {
    (void)handle;
    return 0;
}

static void bt_interrupt_handler_set(int interrupt_no, void (*func)(void *), void *arg) {
    BLE_DBG("bt_interrupt_handler_set: int=%d fn=%p arg=%p\n", interrupt_no, func, arg);
    switch (interrupt_no) {
    case 5:
        s_bt_isr_fn_5 = (bt_isr_fn_t)func;
        s_bt_isr_arg_5 = arg;
        break;
    case 8:
        s_bt_isr_fn_8 = (bt_isr_fn_t)func;
        s_bt_isr_arg_8 = arg;
        break;
    default:
        BLE_DBG("bt_interrupt_handler_set: unsupported int %d\n", interrupt_no);
        break;
    }
    /* Enable the hardware interrupt immediately (matching esp-hal behavior).
     * The blob expects the interrupt to be active right after handler_set. */
    espradio_bt_enable_hw_interrupts();
}

static int bt_interrupt_on(int intr_num) {
    (void)intr_num;
    return 0;
}

static int bt_interrupt_off(int intr_num) {
    (void)intr_num;
    return 0;
}

/* ─── Task management ─── */
extern int32_t espradio_task_create_pinned_to_core(void *func, const char *name,
    uint32_t stack_depth, void *param, uint32_t prio, void *handle, uint32_t core_id);

static int bt_task_create(void *func, const char *name, uint32_t stack_depth,
                          void *param, uint32_t prio, void *handle, uint32_t core_id) {
    BLE_DBG("bt_task_create: %s stack=%u\n", name ? name : "(null)", stack_depth);
    return (int)espradio_task_create_pinned_to_core(func, name, stack_depth,
                                                    param, prio, handle, core_id);
}

static void bt_task_delete(void *handle) {
    (void)handle;
    BLE_DBG("bt_task_delete: %p (no-op)\n", handle);
}

/* ─── Semaphore from ISR ─── */
static int bt_semphr_take_from_isr(void *semphr, void *hptw) {
    (void)hptw;
    return espradio_semphr_take(semphr, 0);
}

static int bt_semphr_give_from_isr(void *semphr, void *hptw) {
    (void)hptw;
    return espradio_semphr_give(semphr);
}

/* ─── Memory ─── */
/* Route through the osi.c wrappers rather than straight to the arena, so BLE
 * allocations are counted by espradio_alloc_stats().  The BT controller and the
 * WiFi blob share one arena, so BLE bypassing the counters made the reported
 * alloc/free totals describe only half the users of the pool. */
extern void *espradio_malloc(size_t size);
extern void  espradio_free(void *p);

static void *bt_malloc(uint32_t size) {
    return espradio_malloc((size_t)size);
}

static void bt_free(void *ptr) {
    espradio_free(ptr);
}

static int bt_read_efuse_mac(void *mac) {
    return espradio_hal_read_mac_go((unsigned char *)mac, 2 /* BT */);
}

/* ─── Random ─── */
static void bt_srand(uint32_t seed) { (void)seed; }
static int  bt_rand(void) {
    return (int)espradio_bt_hw_rand();
}

/* ─── Time/Clock stubs ─── */
static uint32_t bt_lpcycles_2_hus(uint32_t cycles, uint32_t err_corr) {
    (void)err_corr;
    return cycles * 2; /* placeholder: 1 LP cycle ≈ 2 half-us at 500kHz */
}

static uint32_t bt_hus_2_lpcycles(uint32_t us) {
    return us / 2;
}

static int bt_sleep_check_duration(int slot_cnt) { (void)slot_cnt; return 0; }
static void bt_sleep_enter_phase1(int lpcycles)  { (void)lpcycles; }
static void bt_sleep_enter_phase2(void)          {}
static void bt_sleep_exit_phase1(void)           {}
static void bt_sleep_exit_phase2(void)           {}
static void bt_sleep_exit_phase3(void)           {}

/* ─── Coexistence ─── */
static void bt_coex_wifi_sleep_set(int sleep) { (void)sleep; }

static int bt_coex_core_ble_conn_dyn_prio_get(int *low, int *high) {
    if (low) *low = 0;
    if (high) *high = 0;
    return 0;
}

static int bt_coex_schm_register_btdm_callback(void *callback) {
    (void)callback;
    return 0;
}

static void bt_coex_schm_status_bit_set(int typ, int status) {
    (void)typ; (void)status;
}

static void bt_coex_schm_status_bit_clear(int typ, int status) {
    (void)typ; (void)status;
}

static uint32_t bt_coex_schm_interval_get(void) { return 0; }
static uint8_t  bt_coex_schm_curr_period_get(void) { return 0; }
static void    *bt_coex_schm_curr_phase_get(void) { return NULL; }

/* ─── Wakeup ─── */
extern void btdm_wakeup_request(void);
extern void btdm_in_wakeup_requesting_set(bool set);

static volatile uint32_t s_wakeup_request_count;

static void bt_coex_bt_wakeup_request(void) {
    s_wakeup_request_count++;
    BLE_DBG("coex_bt_wakeup_request (#%lu pwr=%lu)\n",
            (unsigned long)s_wakeup_request_count,
            (unsigned long)btdm_pwr_state);
    btdm_wakeup_request();
}

static void bt_coex_bt_wakeup_request_end(void) {
    btdm_in_wakeup_requesting_set(false);
}

uint32_t espradio_bt_wakeup_count(void) { return s_wakeup_request_count; }

/* Called from schedOnce() to drive the BLE link-layer scheduler.
 * Programs COMPVAL for next scan/connection event. Safe from non-ISR context.
 * Unlike calling the ISR (which corrupts state), this just runs the scheduler. */
extern void r_rwip_schedule(void);
extern void r_ke_event_schedule(void);
extern void r_ke_task_schedule(void);

/* What the 5 ms ticker is allowed to drive.
 *   bit 0 — ke message pump (ke_event_schedule + ke_task_schedule)
 *   bit 1 — rwip_schedule (reprograms the BLE hardware timer)
 * The message pump is REQUIRED: the controller task goroutine does not run its
 * own loop under the cooperative scheduler, so without this HCI commands are
 * never dequeued and even LE Set Scan Enable never reaches lld_scan_start.
 * rwip_schedule must stay OFF — see espradio_bt_sched_tick(). */
#define BT_TICK_KE_PUMP       0x1  /* ke_event_schedule (event dispatch)  */
#define BT_TICK_RWIP_SCHEDULE 0x2  /* rwip_schedule (LL scheduler)        */
#define BT_TICK_KE_TASK       0x4  /* ke_task_schedule (message dispatch) */

/* ke_event_schedule is the dispatcher that matters: it walks ke_env.evt_field
 * and calls each pending event's callback, and ke_task_schedule is simply the
 * callback registered for event 3 (r_ke_task_init does
 * ke_event_callback_set(3, ke_task_schedule)), so message dispatch already
 * happens inside it. That is why BT_TICK_KE_TASK stays off: driving
 * ke_task_schedule from the tick as well only runs message dispatch a second
 * time. */
static int s_sched_tick_mask = BT_TICK_KE_PUMP | BT_TICK_RWIP_SCHEDULE;

void espradio_bt_set_sched_tick_mask(int mask) {
    s_sched_tick_mask = mask;
}

/* Drive the controller until it has taken the packet just handed to it, so
 * back-to-back writes cannot overwrite its single HCI input slot.
 *
 * API_vhci_host_check_send_available() is no use as the completion signal here:
 * it reports capacity and is already true immediately after a send, so polling
 * it returns at once without the controller having run. The real signal is
 * notify_host_send_available, latched in s_vhci_send_available and cleared by
 * the writer before handing the packet over.
 *
 * Reaching the controller also takes more than a yield: btdm_controller_task
 * blocks on its semaphore, so it has to be woken explicitly. Bounded, because
 * the ROM only promises the callback on a transition. */
static void bt_pump_hci(void) {
    for (int i = 0; i < 4 && !s_vhci_send_available; i++) {
        bt_isr_wake_task();
        espradio_task_yield_go();
        r_ke_event_schedule();
    }
}

void espradio_bt_sched_tick(void) {
    if (!s_bt_isr_fn_8) return; /* BLE not initialized */

    /* Run the deferred ISRs. The ESP32-C3 does nothing here. */
    espradio_bt_chip_service_isrs();

    /* Heartbeat the controller task.
     *
     * btdm_controller_task blocks on btdm_ol_task_env->sem and, once released,
     * runs btdm_rw_run(s_btdm_state) -> rw_schedule() even when the message
     * queue is empty.  In a preemptive build the RWBLE ISR supplies that wake on
     * every event; here the ISR currently fires only once, so give the semaphore
     * on each tick to keep the task's loop turning. This is the mechanism that
     * actually lets the blob reschedule its own activities, rather than us
     * calling its scheduler from the wrong context. */
    bt_wake_task_throttled();

    /* The ke message pump has to run from somewhere: the blob's controller task
     * goroutine does not spin its own loop here, so nothing else dequeues ke
     * messages and HCI commands would never be executed at all. */
    if (s_sched_tick_mask & BT_TICK_KE_PUMP) {
        r_ke_event_schedule();
    }
    if (s_sched_tick_mask & BT_TICK_KE_TASK) {
        r_ke_task_schedule();
    }

    /* r_rwip_schedule() ends in sch_arb_prog_timer(), which reprograms the BLE
     * hardware timer from the arbiter list head, and calls
     * rwip_timer_hus_set(0xffffffff) (clearing timer-enable bit 11 of BLE +0x0c)
     * when that list is empty.  Driving it from an unrelated goroutine at 200 Hz
     * is not how the blob is meant to be scheduled, so it is a suspect worth
     * keeping switchable — but measured A/B it makes no difference to the
     * current failure, and the bit-11 clear seen after the first event comes
     * from rwip_timer_hus_handler (the half-us timer is a deliberate one-shot),
     * NOT from this call.  Left enabled; do not "fix" it without evidence. */
    if (s_sched_tick_mask & BT_TICK_RWIP_SCHEDULE) {
        r_rwip_schedule();
    }

    espradio_bt_chip_debug_tick();
}

/* ─── Power ─── */
static void bt_hw_power_down(void) {}
static void bt_hw_power_up(void) {}

/* ─── Misc ─── */
extern void ets_backup_dma_copy(uint32_t reg, uint32_t mem_addr, uint32_t num, int to_rem);
extern void ets_delay_us(uint32_t us);

static void bt_ets_backup_dma_copy(uint32_t reg, uint32_t mem_addr, uint32_t num, int to_rem) {
    ets_backup_dma_copy(reg, mem_addr, num, to_rem);
}

static void bt_ets_delay_us(uint32_t us) {
    ets_delay_us(us);
}

/* ROM table ready — reset function pointer tables in ROM */
extern void ble_base_funcs_reset(void);
extern void ble_42_adv_funcs_reset(void);
extern void ble_ext_adv_funcs_reset(void);
extern void ble_dtm_funcs_reset(void);
extern void ble_scan_funcs_reset(void);
extern void ble_ext_scan_funcs_reset(void);
extern void ble_enc_funcs_reset(void);
extern void ble_init_funcs_reset(void);
extern void ble_con_funcs_reset(void);

/* Patch ALL of the ROM function-pointer tables, in the same order as ESP-IDF
 * and esp-hal.  Partial patching is not a supported configuration: these
 * tables are one ABI.  ble_base_funcs_reset() installs the flash scheduler and
 * ISR core (ip[0x6a8]=sch_arb_event_start_isr_hack, ip[0x12c]=rwble_isr_hack,
 * ip[0x6c0]=sch_prog_end_isr_hack, ip[0x7a8]=sch_prog_ble_push_hack), and those
 * only interoperate with the matching flash scan entries installed here:
 *   ble_scan_funcs_reset():     ip[0x438]=lld_scan_start_eco,
 *                               ip[0x514]=llm_scan_start_eco,
 *                               ip[0x3ec]=lld_scan_evt_start_cbk_eco,
 *                               ip[0x3fc]=lld_scan_frm_skip_isr_eco,
 *                               ip[0x408]=lld_scan_process_pkt_rx_hack,
 *                               ip[0x424]=lld_scan_process_pkt_rx_adv_rep_hack
 *   ble_ext_scan_funcs_reset(): ip[0x3f4]=lld_scan_frm_eof_isr_eco
 * ip[0x408]/[0x424] are the advertising-report RX path — leaving them at the
 * ROM versions means received advertisements never turn into HCI LE Advertising
 * Report events, even when the radio and scheduler are working. */
static void bt_rom_table_ready(void) {
    BLE_DBG("bt_rom_table_ready\n");
    ble_base_funcs_reset();
    ble_42_adv_funcs_reset();
    ble_ext_adv_funcs_reset();
    ble_dtm_funcs_reset();
    ble_scan_funcs_reset();
    ble_ext_scan_funcs_reset();
    ble_enc_funcs_reset();
    ble_init_funcs_reset();
    ble_con_funcs_reset();
}

static uint64_t bt_get_time_us(void) {
    return espradio_time_us_now();
}

static void bt_assert(void) {
    espradio_panic("BT assert");
}

/* ─── OSI Function Table (matches esp-hal os_adapter_esp32c3_s3.rs exactly) ─── */

typedef struct {
    uint32_t magic;
    uint32_t version;
    int  (*interrupt_alloc)(int, int, void (*)(void *), void *, void **);
    int  (*interrupt_free)(void *);
    void (*interrupt_handler_set)(int, void (*)(void *), void *);
    void (*interrupt_disable)(void);
    void (*interrupt_restore)(void);
    void (*task_yield)(void);
    void (*task_yield_from_isr)(void);
    void *(*semphr_create)(uint32_t, uint32_t);
    void (*semphr_delete)(void *);
    int  (*semphr_take_from_isr)(void *, void *);
    int  (*semphr_give_from_isr)(void *, void *);
    int  (*semphr_take)(void *, uint32_t);
    int  (*semphr_give)(void *);
    void *(*mutex_create)(void);
    void (*mutex_delete)(void *);
    int  (*mutex_lock)(void *);
    int  (*mutex_unlock)(void *);
    void *(*queue_create)(uint32_t, uint32_t);
    void (*queue_delete)(void *);
    int  (*queue_send)(void *, void *, uint32_t);
    int  (*queue_send_from_isr)(void *, void *, void *);
    int  (*queue_recv)(void *, void *, uint32_t);
    int  (*queue_recv_from_isr)(void *, void *, void *);
    int  (*task_create)(void *, const char *, uint32_t, void *, uint32_t, void *, uint32_t);
    void (*task_delete)(void *);
    int  (*is_in_isr)(void);
    int  (*cause_sw_intr_to_core)(int, int);  /* NULL on C3 */
    void *(*malloc)(uint32_t);
    void *(*malloc_internal)(uint32_t);
    void (*free)(void *);
    int  (*read_efuse_mac)(void *);
    void (*srand)(uint32_t);
    int  (*rand)(void);
    uint32_t (*btdm_lpcycles_2_hus)(uint32_t, uint32_t);
    uint32_t (*btdm_hus_2_lpcycles)(uint32_t);
    int  (*btdm_sleep_check_duration)(int);
    void (*btdm_sleep_enter_phase1)(int);
    void (*btdm_sleep_enter_phase2)(void);
    void (*btdm_sleep_exit_phase1)(void);
    void (*btdm_sleep_exit_phase2)(void);
    void (*btdm_sleep_exit_phase3)(void);
    void (*coex_wifi_sleep_set)(int);
    int  (*coex_core_ble_conn_dyn_prio_get)(int *, int *);
    int  (*coex_schm_register_btdm_callback)(void *);
    void (*coex_schm_status_bit_set)(int, int);
    void (*coex_schm_status_bit_clear)(int, int);
    uint32_t (*coex_schm_interval_get)(void);
    uint8_t  (*coex_schm_curr_period_get)(void);
    void    *(*coex_schm_curr_phase_get)(void);
    int  (*interrupt_on)(int);
    int  (*interrupt_off)(int);
    void (*esp_hw_power_down)(void);
    void (*esp_hw_power_up)(void);
    void (*ets_backup_dma_copy)(uint32_t, uint32_t, uint32_t, int);
    void (*ets_delay_us)(uint32_t);
    void (*btdm_rom_table_ready)(void);
    void (*coex_bt_wakeup_request)(void);
    void (*coex_bt_wakeup_request_end)(void);
    uint64_t (*get_time_us)(void);
    void (*assert)(void);
} bt_osi_funcs_t;

static const bt_osi_funcs_t s_bt_osi_funcs = {
    .magic   = 0xFADEBEAD,
    .version = 0x0001000A,
    .interrupt_alloc          = bt_interrupt_alloc,
    .interrupt_free           = bt_interrupt_free,
    .interrupt_handler_set    = bt_interrupt_handler_set,
    .interrupt_disable        = bt_interrupt_disable,
    .interrupt_restore        = bt_interrupt_restore,
    .task_yield               = espradio_task_yield_go,
    .task_yield_from_isr      = espradio_task_yield_go,
    .semphr_create            = espradio_semphr_create,
    .semphr_delete            = espradio_semphr_delete,
    .semphr_take_from_isr     = bt_semphr_take_from_isr,
    .semphr_give_from_isr     = bt_semphr_give_from_isr,
    .semphr_take              = espradio_semphr_take,
    .semphr_give              = espradio_semphr_give,
    .mutex_create             = espradio_recursive_mutex_create,
    .mutex_delete             = espradio_mutex_delete,
    .mutex_lock               = espradio_mutex_lock,
    .mutex_unlock             = espradio_mutex_unlock,
    .queue_create             = espradio_queue_create_internal,
    .queue_delete             = espradio_queue_delete_internal,
    .queue_send               = espradio_queue_send,
    .queue_send_from_isr      = espradio_queue_send_from_isr,
    .queue_recv               = espradio_queue_recv,
    .queue_recv_from_isr      = espradio_queue_recv_from_isr,
    .task_create              = bt_task_create,
    .task_delete              = bt_task_delete,
    .is_in_isr                = bt_is_in_isr,
    .cause_sw_intr_to_core    = NULL, /* not supported on RISC-V */
    .malloc                   = bt_malloc,
    .malloc_internal          = bt_malloc,
    .free                     = bt_free,
    .read_efuse_mac           = bt_read_efuse_mac,
    .srand                    = bt_srand,
    .rand                     = bt_rand,
    .btdm_lpcycles_2_hus      = bt_lpcycles_2_hus,
    .btdm_hus_2_lpcycles      = bt_hus_2_lpcycles,
    .btdm_sleep_check_duration = bt_sleep_check_duration,
    .btdm_sleep_enter_phase1  = bt_sleep_enter_phase1,
    .btdm_sleep_enter_phase2  = bt_sleep_enter_phase2,
    .btdm_sleep_exit_phase1   = bt_sleep_exit_phase1,
    .btdm_sleep_exit_phase2   = bt_sleep_exit_phase2,
    .btdm_sleep_exit_phase3   = bt_sleep_exit_phase3,
    .coex_wifi_sleep_set      = bt_coex_wifi_sleep_set,
    .coex_core_ble_conn_dyn_prio_get = bt_coex_core_ble_conn_dyn_prio_get,
    .coex_schm_register_btdm_callback = bt_coex_schm_register_btdm_callback,
    .coex_schm_status_bit_set   = bt_coex_schm_status_bit_set,
    .coex_schm_status_bit_clear = bt_coex_schm_status_bit_clear,
    .coex_schm_interval_get     = bt_coex_schm_interval_get,
    .coex_schm_curr_period_get  = bt_coex_schm_curr_period_get,
    .coex_schm_curr_phase_get   = bt_coex_schm_curr_phase_get,
    .interrupt_on             = bt_interrupt_on,
    .interrupt_off            = bt_interrupt_off,
    .esp_hw_power_down        = bt_hw_power_down,
    .esp_hw_power_up          = bt_hw_power_up,
    .ets_backup_dma_copy      = bt_ets_backup_dma_copy,
    .ets_delay_us             = bt_ets_delay_us,
    .btdm_rom_table_ready     = bt_rom_table_ready,
    .coex_bt_wakeup_request   = bt_coex_bt_wakeup_request,
    .coex_bt_wakeup_request_end = bt_coex_bt_wakeup_request_end,
    .get_time_us              = bt_get_time_us,
    .assert                   = bt_assert,
};

/* BLE Controller Initialization */

/* ROM functions from libbtdm_app.a */
extern int  btdm_osi_funcs_register(const void *osi_funcs);
extern int  btdm_controller_rom_data_init(void);
extern int  btdm_controller_init(const void *config);
extern void btdm_controller_enable(uint32_t mode);
extern void coex_pti_v2(void);

/* Config constants */
#define ESP_BT_MODE_BLE               1

/* CFG_MASK and SLAVE_CE_LEN_MIN from esp-hal defaults */
#define BT_CFG_MASK                   0x00000001
#define SLAVE_CE_LEN_MIN_DEFAULT      5

/* The blob's internal diagnostics (scan CS programming, "BLE assert %s %d",
 * PTI/coex state) are all gated behind this and default to 0 = silent.
 * Raising it is the only way to see the controller explain itself. */
extern uint32_t g_bt_plf_log_level;

int espradio_ble_init(void) {
    BLE_DBG("espradio_ble_init: start\n");

#if ESPRADIO_BLE_DEBUG
    g_bt_plf_log_level = 10;
    BLE_DBG("  g_bt_plf_log_level=%lu\n", (unsigned long)g_bt_plf_log_level);
#endif

    /* Step 0: Enable BT peripheral clock and reset.
     * The modem reset was done by initHardware() (Go side) but clock enable
     * is separate. Without the clock, btdm_controller_init fails with
     * "Funcs table create fails". */
    #define APB_CTRL_WIFI_CLK_EN_REG  (*(volatile uint32_t *)0x60026014u)
    #define APB_CTRL_WIFI_RST_EN_REG  (*(volatile uint32_t *)0x60026018u)
    /* Full modem clock enable mask (WiFi+BT+coex) — same as ESP-IDF's
     * SYSTEM_WIFI_CLK_EN value. Just BT bits (0x860) isn't enough; the BT_BB
     * CLKNCNT counter needs the full modem domain clock tree active. */
    #define MODEM_CLK_EN  0x00FB9FCFu
    /* BT reset bits: BTBB(3), BTMAC(4), RW_BTMAC(9), RW_BTMAC_REG(11), BTBB_REG(13) */
    #define BT_RST_BITS     ((1u << 3) | (1u << 4) | (1u << 9) | (1u << 11) | (1u << 13))

    /* ORDER MATTERS: clock first, THEN release reset.
     *
     * ESP-IDF brings a modem block up via periph_module_enable() ->
     * periph_ll_enable_clk_clear_rst(), which sets the clock-enable mask and only
     * then clears the reset mask.  initHardware() (Go side) pulses the BT reset
     * bits, but it runs before this function, i.e. before any modem clock is
     * enabled — so BT_BB and RW_BTMAC were being released from reset with no
     * clock running, which leaves their internal state machines undefined.  The
     * visible symptom was r_cali_phase_match_p sweeping all 16 hi/lo settings
     * without BLE +0xf8 bit 12 ever asserting ("phase match cali failed!"), i.e.
     * an uncalibrated receiver.
     *
     * Enable the clock, let it settle, then pulse the BT resets so the blocks
     * come out of reset with a running clock. */
    /* RTC_CNTL DIG_PWC bit 11 = BT_FORCE_PD, DIG_ISO bit 22 = BT_FORCE_ISO.
     * If either is still set the BT analog/RF domain stays powered down or
     * isolated even though its digital registers respond normally. */
    BLE_DBG("  clk/rst before: clk=0x%08lx rst=0x%08lx dig_pwc=0x%08lx dig_iso=0x%08lx\n",
            (unsigned long)APB_CTRL_WIFI_CLK_EN_REG,
            (unsigned long)APB_CTRL_WIFI_RST_EN_REG,
            (unsigned long)*(volatile uint32_t *)0x60008088u,  /* RTC_CNTL_DIG_PWC_REG */
            (unsigned long)*(volatile uint32_t *)0x6000808Cu); /* RTC_CNTL_DIG_ISO_REG */

    /* Clear-then-set, exactly as ESP-IDF's esp_perip_clk_init() and esp-hal's
     * init_clocks() do:
     *
     *     wifi_clk_en = (wifi_clk_en & ~WIFI_BT_SDIO_CLK) | SYSTEM_WIFI_CLK_EN
     *
     * A bare |= is NOT equivalent. Bit 12 is inside SYSTEM_WIFI_CLK_EN so it
     * comes back either way, but bit 5 is not — so the clear is the only thing
     * that ever turns bit 5 off, and the register's power-on default
     * (0xfffce030) has it on. Bit 5 is ESP-IDF's SYSTEM_WIFI_CLK_UNUSED_BIT5,
     * the internal analog I2C clock used to program the RF/BBPLL. Leaving it
     * enabled left this register at 0xffffffff instead of 0xffffffdf. */
    #define SYSTEM_WIFI_CLK_I2C_CLK_EN    (1u << 5)
    #define SYSTEM_WIFI_CLK_UNUSED_BIT12  (1u << 12)
    #define WIFI_BT_SDIO_CLK  (SYSTEM_WIFI_CLK_I2C_CLK_EN | SYSTEM_WIFI_CLK_UNUSED_BIT12)

    APB_CTRL_WIFI_CLK_EN_REG =
        (APB_CTRL_WIFI_CLK_EN_REG & ~WIFI_BT_SDIO_CLK) | MODEM_CLK_EN;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(50);

    /* Assert then release only the BT bits, so a WiFi session that is already
     * running is left alone. */
    APB_CTRL_WIFI_RST_EN_REG |= BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(10);
    APB_CTRL_WIFI_RST_EN_REG &= ~BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(50);

    BLE_DBG("  clk/rst after:  clk=0x%08lx rst=0x%08lx\n",
            (unsigned long)APB_CTRL_WIFI_CLK_EN_REG,
            (unsigned long)APB_CTRL_WIFI_RST_EN_REG);

    /* Step 0b: Tell the ROM what the CPU is actually running at.
     *
     * ROM ets_delay_us() busy-waits using a ticks-per-microsecond value cached in
     * ROM data, which is only updated by ets_update_cpu_frequency(). TinyGo raises
     * the CPU clock without calling it, so the ROM kept a stale (20 MHz) value and
     * every ets_delay_us() came back 8x early -- measured: a requested 100ms
     * elapsed in 12.5ms against the BLE native clock.
     *
     * The blob leans on ets_delay_us for hardware settling all through RF and
     * baseband bring-up. r_cali_phase_match_p in particular polls its "phase
     * locked" bit with just ets_delay_us(1) between attempts, so at 1/8 scale it
     * samples the comparator ~125ns after kicking it, never sees the bit set, and
     * burns all 16 hi/lo combinations in a few microseconds -- reporting
     * "phase match cali failed!" on hardware that never got a chance to settle. */
    extern void ets_update_cpu_frequency(uint32_t ticks_per_us);
    uint32_t ticks_per_us = espradio_bt_cpu_ticks_per_us();
    ets_update_cpu_frequency(ticks_per_us);
    BLE_DBG("  ets_update_cpu_frequency(%lu) done\n", (unsigned long)ticks_per_us);

    /* Step 1: ROM data init */
    btdm_controller_rom_data_init();
    BLE_DBG("  rom_data_init done\n");

    /* Step 2: Build config */
    esp_bt_controller_config_t cfg = {0};
    cfg.version = ESP_BT_CTRL_CONFIG_VERSION;
    cfg.controller_task_stack_size = 8192;
    cfg.controller_task_prio = 253; /* high priority */
    cfg.controller_task_run_cpu = 0;
    cfg.bluetooth_mode = ESP_BT_MODE_BLE;
    cfg.ble_max_act = 6;
    cfg.sleep_mode = 0;
    cfg.sleep_clock = 0;
    cfg.ble_st_acl_tx_buf_nb = 0;
    cfg.ble_hw_cca_check = 0;
    cfg.ble_adv_dup_filt_max = 30;
    cfg.coex_param_en = 0;
    cfg.ce_len_type = 0;
    cfg.coex_use_hooks = 0;
    cfg.hci_tl_type = 1; /* VHCI */
    cfg.hci_tl_funcs = NULL;
    cfg.txant_dft = 0;
    cfg.rxant_dft = 0;
    cfg.txpwr_dft = 9; /* +9 dBm */
    cfg.cfg_mask = BT_CFG_MASK;
    cfg.scan_duplicate_mode = 0;
    cfg.scan_duplicate_type = 0;
    cfg.mesh_adv_size = 0;
    cfg.normal_adv_size = 100;
    cfg.coex_phy_coded_tx_rx_time_limit = 0;
    cfg.hw_target_code = espradio_bt_hw_target_code();
    cfg.slave_ce_len_min = SLAVE_CE_LEN_MIN_DEFAULT;
    cfg.hw_recorrect_en = 0;
    cfg.cca_thresh = 75;
    cfg.dup_list_refresh_period = 0;
    cfg.scan_backoff_upperlimitmax = 0;
    /* Match BT_CONTROLLER_INIT_CONFIG_DEFAULT / esp-hal: BLE 5.0 features on.
     * The flash scan handlers accept the legacy commands (0x200B/0x200C) that
     * the bluetooth package sends; forcing this to 0 diverges from the only
     * configuration the blob's function tables are built for. */
    cfg.ble_50_feat_supp = 1;
    cfg.ble_cca_mode = 0;
    cfg.ble_chan_ass_en = 0;
    cfg.ble_data_lenth_zero_aux = 0;
    cfg.ble_ping_en = 0;
    cfg.ble_llcp_disc_flag = 0;
    cfg.run_in_flash = 0;
    cfg.dtm_en = 1;
    cfg.enc_en = 1;
    cfg.qa_test = 0;
    cfg.connect_en = 1;
    cfg.scan_en = 1;
    cfg.ble_aa_check = 1;
    cfg.adv_en = 1;
    cfg.magic = ESP_BT_CTRL_CONFIG_MAGIC_VAL;

    /* Step 3: Register OSI functions */
    int res = btdm_osi_funcs_register(&s_bt_osi_funcs);
    if (res != 0) {
        BLE_DBG("  btdm_osi_funcs_register FAILED: %d\n", res);
        return -1;
    }
    BLE_DBG("  osi_funcs registered\n");

    /* Step 4: Power domain + modem init (prerequisites for PHY) */
    extern void esp_wifi_bt_power_domain_on(void);
    esp_wifi_bt_power_domain_on();
    extern void esp_phy_modem_init(void);
    esp_phy_modem_init();
    BLE_DBG("  power domain + modem init done\n");

    /* Step 4c: Disable sleep at ROM level BEFORE controller_init.
     * The task starts during init and calls rw_schedule → r_rwip_sleep.
     * With rw_sleep_enable=0, the sleep path is skipped. */
    rw_sleep_enable = 0;

    /* Step 5: Enable the PHY *before* btdm_controller_init().
     *
     * This ordering is not cosmetic.  ESP-IDF's esp_bt_controller_init() for the
     * C3 (components/bt/controller/esp32c3/bt.c) runs:
     *
     *     periph_module_enable(PERIPH_BT_MODULE);
     *     periph_module_reset(PERIPH_BT_MODULE);
     *     esp_phy_enable();            <-- PHY up first
     *     btdm_controller_init(cfg);   <-- then the controller
     *     coex_pti_v2();
     *
     * btdm_controller_init() runs r_rw_rf_init() ("initialise RF LC Todd"), which
     * programs the BT RF front-end, and btdm_controller_enable() then runs
     * r_cali_phase_match_p() to calibrate BB<->RF phase alignment.  With the PHY
     * still unpowered at that point the RF is initialised against a dead PLL, and
     * the calibration sweeps all 16 hi/lo settings without BLE +0xf8 bit 12 ever
     * asserting -> "phase match cali failed!" -> uncalibrated receiver.
     *
     * (esp-hal has these two the other way round, which is where the previous
     * "matching esp-hal" comment came from; ESP-IDF is the authority here.) */
    BLE_DBG("  calling esp_phy_enable(BT)...\n");
    esp_phy_enable(2 /* PHY_MODEM_BT */);
    BLE_DBG("  phy_enable done\n");

    /* Step 6: Init controller (creates btController task goroutine) */
    res = btdm_controller_init(&cfg);
    if (res != 0) {
        BLE_DBG("  btdm_controller_init FAILED: %d\n", res);
        return -2;
    }
    BLE_DBG("  controller_init done\n");

    /* Step 6b: Tell PHY that BT is active (not WiFi-only).
     * Without this, the radio RF path isn't configured for BLE reception. */
    extern void phy_set_wifi_mode_only(bool wifi_only);
    phy_set_wifi_mode_only(false);
    BLE_DBG("  phy wifi_mode_only=false\n");

    /* Step 7: Coex PTI (C3/S3 — esp-hal does this instead of bt_bb_v2_init) */
    coex_pti_v2();
    BLE_DBG("  coex_pti_v2 done\n");

    /* Step 8: Enable BLE mode (blob calls interrupt_handler_set here) */
    btdm_controller_enable(ESP_BT_MODE_BLE);
    BLE_DBG("  controller enabled (BLE)\n");

    /* Immediately yield to let the controller task run while internal state
     * (pwr_state) may be temporarily set by controller_enable. In a preemptive
     * scheduler, the task would run immediately; we simulate this. */
    for (int i = 0; i < 50; i++) {
        espradio_task_yield_go();
    }

    /* Step 8a: Enable the BLE core's end-of-event interrupt.
     *
     * BLE +0x0c is the core interrupt-enable mask.  Tracing every write to it in
     * libbtdm_app shows each bit has exactly one owner:
     *   bit 0  wakeup/clkn     bit 9  10 ms timer    bit 12 software int
     *   bit 3  END OF EVENT    bit 10 half-slot      bit 19 CCA software int
     *   bit 7  crypt           bit 11 half-us timer
     * r_rwip_driver_init sets 0x1180 (bits 7, 8, 12) — note: NO bit 3.  The only
     * code that ever sets bit 3 is r_rwip_wakeup_end_hack (|= 0x1188), i.e. the
     * tail of a sleep->wake cycle, which also sets btdm_pwr_state = 4.
     *
     * Because we hold the controller permanently awake (sleep_mode = 0,
     * rw_sleep_enable = 0, prevent_sleep_set below), that wakeup tail never runs,
     * so bit 3 stayed masked.  The observable effect was precisely: the half-us
     * timer fires once, sch_arb_event_start_isr starts the scan event,
     * sch_prog_ble_push programs and kicks the radio — and then the completion
     * interrupt never arrives, so nothing ever reschedules the next scan window
     * and no advertising report is ever delivered.
     *
     * NOTE: setting bit 3 here was tried and had no effect, because
     * r_lld_core_init puts this core in IRQ-FIFO mode (INTCNTL = 0x640000|0x166)
     * where event status is delivered through the FIFO at +0x2d8 and these
     * classic per-source enables are not the mechanism. Left documented rather
     * than poked. btdm_pwr_state also never reaches 4 (stays 0) for the same
     * reason — the wakeup tail never runs. */
    BLE_DBG("  pwr_state=%lu\n", (unsigned long)btdm_pwr_state);
    espradio_bt_chip_debug_after_init();

    /* Step 8b: Disable modem sleep and set prevent_sleep flags. */
    extern void btdm_controller_enable_sleep(bool enable);
    btdm_controller_enable_sleep(false);
    extern void r_rwip_prevent_sleep_set(uint32_t prv_slp_bit);
    r_rwip_prevent_sleep_set(0x0F);
    BLE_DBG("  enable_sleep(false) + prevent_sleep done\n");

    /* Step 9: Register VHCI callbacks */
    res = API_vhci_host_register_callback(&s_vhci_cbs);
    if (res != 0) {
        BLE_DBG("  vhci_register FAILED: %d\n", res);
        return -3;
    }
    BLE_DBG("  vhci registered\n");

    BLE_DBG("espradio_ble_init: OK\n");
    return 0;
}
