//go:build esp32 || esp32c3 || esp32s3

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

/* Wake the BT controller task and count the result. The mechanism is not the
 * same on each chip, thus the chip layer does the work. */
static volatile uint32_t s_task_wake_count;
static volatile uint32_t s_task_wake_nosem;

static void bt_isr_wake_task(void) {
    if (espradio_bt_chip_wake_task()) {
        s_task_wake_count++;
    } else {
        s_task_wake_nosem++;
    }
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
        espradio_bt_chip_ke_task_schedule();
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

/* OSI function table.
 * The layout and the version are not the same on each chip, so the table lives
 * in a header that this file includes. The table uses the static primitives
 * above, which is why it is a header and not a separate translation unit. */
#ifdef CONFIG_IDF_TARGET_ESP32
#include "bt_ble_osi_esp32.h"
#else
#include "bt_ble_osi_esp32c3_s3.h"
#endif

/* BLE Controller Initialization */

/* The chip layer owns the clock, the config and the controller start, because
 * the registers and the blob entry points are not the same on each chip. */

extern int btdm_osi_funcs_register(const void *osi_funcs);

int espradio_ble_init(void) {
    BLE_DBG("espradio_ble_init: start\n");

    espradio_bt_chip_clocks_up();

    /* The blob rejects a table with the wrong magic or version. */
    int res = btdm_osi_funcs_register(espradio_bt_osi_table());
    if (res != 0) {
        BLE_DBG("  btdm_osi_funcs_register FAILED: %d\n", res);
        return -1;
    }
    BLE_DBG("  osi_funcs registered\n");

    res = espradio_bt_chip_controller_bringup();
    if (res != 0) {
        BLE_DBG("  controller bringup FAILED: %d\n", res);
        return -2;
    }

    res = API_vhci_host_register_callback(&s_vhci_cbs);
    if (res != 0) {
        BLE_DBG("  vhci_register FAILED: %d\n", res);
        return -3;
    }
    BLE_DBG("  vhci registered\n");

    espradio_bt_chip_debug_after_init();
    BLE_DBG("espradio_ble_init: OK\n");
    return 0;
}
