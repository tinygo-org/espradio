//go:build esp32c6

/* BLE controller driver for the ESP32-C6 NimBLE controller in libble_app.a.
 * Follows esp-hal esp-radio/src/ble/npl.rs and os_adapter_esp32c6.rs. */

#include "espradio.h"
#include "esp_bt.h"
#include "nimble/nimble_npl.h"
#include <stdint.h>
#include <stdbool.h>
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

/* Values from esp-hal esp-radio/src/ble/npl.rs. */
#define EXT_FUNCS_VERSION       0x20250825u
#define EXT_FUNCS_MAGIC         0xA5A5A5A5u
#define BLE_HCI_TRANS_BUF_CMD   3
#define ACL_DATA_MBUF_LEADINGSPACE 4
#define EVENTQ_LEN              32

#define BT_MAC_INTR_SOURCE      4
#define LP_TIMER_INTR_SOURCE    7

/* Must match btCPUIntMAC and btCPUIntTimer in radio_esp32c6.go. */
#define BT_CPU_INT_MAC          28u
#define BT_CPU_INT_TIMER        30u

#define PLIC_BASE               0x20001000u
#define PLIC_ENABLE_REG         (*(volatile uint32_t *)(PLIC_BASE + 0x00u))
#define PLIC_TYPE_REG           (*(volatile uint32_t *)(PLIC_BASE + 0x04u))
#define PLIC_PRI_REG(n)         (*(volatile uint32_t *)(PLIC_BASE + 0x10u + (uint32_t)(n) * 4u))

extern void *espradio_malloc(size_t size);
extern void  espradio_free(void *p);
extern void *espradio_queue_create_internal(uint32_t len, uint32_t item_size);
extern void  espradio_queue_delete_internal(void *queue);
extern int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
extern int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw);
extern int32_t espradio_queue_recv(void *queue, void *item, uint32_t block_time_tick);
extern uint32_t espradio_queue_len(void *queue);
extern void  espradio_task_yield_go(void);
extern void  espradio_task_delay(uint32_t tick);
extern int32_t espradio_task_ms_to_tick(uint32_t ms);
extern void *espradio_task_get_current_task(void);
extern int32_t espradio_task_create_pinned_to_core(void *func, const char *name,
    uint32_t stack_depth, void *param, uint32_t prio, void *handle, uint32_t core_id);
extern void  espradio_task_delete(void *handle);
extern uint64_t espradio_time_us_now(void);
extern bool  espradio_in_hw_isr(void);
extern void  espradio_enter_hw_isr(void);
extern void  espradio_exit_hw_isr(void);
extern int   espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
extern void  espradio_hal_enable_bt_clocks_go(void);
extern unsigned int espradio_hal_chip_minor_version_go(void);
extern uint32_t esp_random(void);
extern int   espradio_vhci_ring_push(const uint8_t *data, int len);

extern void espradio_timer_setfn_ext(void *ptimer, void *fn, void *arg);
extern void espradio_timer_arm_ext(void *ptimer, uint32_t ms, bool repeat);
extern void espradio_timer_disarm_ext(void *ptimer);
extern void espradio_timer_done_ext(void *ptimer);
extern bool espradio_timer_is_active_ext(void *ptimer);

/* Blob entry points (libble_app.a, libbtbb.a, libphy.a). */
struct os_mbuf;
extern int  esp_register_ext_funcs(const void *funcs);
extern int  esp_register_npl_funcs(const void *funcs);
extern int  r_ble_get_npl_element_info(esp_bt_controller_config_t *cfg, void *info);
extern void bt_bb_v2_init_cmplx(uint8_t value);
extern void coex_pti_v2(void);
extern int  esp_ble_register_bb_funcs(void);
extern int  r_ble_controller_init(esp_bt_controller_config_t *cfg);
extern int  r_ble_controller_enable(uint8_t mode);
extern int  base_stack_initEnv(void);
extern int  adv_stack_initEnv(void);
extern int  extAdv_stack_initEnv(void);
extern int  scan_stack_initEnv(void);
extern int  conn_stack_initEnv(void);
extern int  sync_stack_initEnv(void);
extern int  base_stack_enable(void);
extern int  adv_stack_enable(void);
extern int  extAdv_stack_enable(void);
extern int  scan_stack_enable(void);
extern int  sync_stack_enable(void);
extern int  r_esp_ble_msys_init(uint16_t size1, uint16_t size2, uint16_t cnt1,
                                uint16_t cnt2, uint8_t from_heap);
extern int  r_esp_ble_ll_set_public_addr(const uint8_t *addr);
extern void r_ble_hci_trans_init(uint8_t mode);
extern void r_ble_hci_trans_cfg_hs(int (*evt_cb)(uint8_t *, void *), void *evt_arg,
                                   int (*acl_cb)(struct os_mbuf *, void *), void *acl_arg);
extern uint8_t *r_ble_hci_trans_buf_alloc(int type);
extern void r_ble_hci_trans_buf_free(uint8_t *buf);
extern int  r_ble_hci_trans_hs_cmd_tx(uint8_t *cmd);
extern int  r_ble_hci_trans_hs_acl_tx(struct os_mbuf *om);
extern struct os_mbuf *r_os_msys_get_pkthdr(uint16_t dsize, uint16_t user_hdr_len);
extern int  r_os_mbuf_append(struct os_mbuf *om, const void *src, uint16_t len);
extern int  r_os_mbuf_free_chain(struct os_mbuf *om);
extern void esp_phy_enable(esp_phy_modem_t modem);

/* NimBLE os_mbuf layout (esp-hal npl.rs OsMbuf). */
struct espradio_os_mbuf {
    uint8_t *om_data;
    uint8_t om_flags;
    uint8_t om_pkthdr_len;
    uint16_t om_len;
    void *om_omp;
    struct espradio_os_mbuf *om_next;
};

/* ─── Critical sections ─── */

static volatile uint32_t s_cs_depth;

static uint32_t npl_hw_enter_critical(void) {
    uint32_t mstatus;
    __asm__ volatile ("csrrci %0, mstatus, 8" : "=r"(mstatus) :: "memory");
    s_cs_depth++;
    return mstatus & 8u;
}

static void npl_hw_exit_critical(uint32_t mask) {
    if (s_cs_depth) s_cs_depth--;
    if (mask) {
        __asm__ volatile ("csrsi mstatus, 8" ::: "memory");
    }
}

static uint8_t npl_hw_is_in_critical(void) {
    return s_cs_depth != 0;
}

static void npl_hw_set_isr(int no, uint32_t mask) {
    (void)no; (void)mask;
}

/* ─── Events and event queues ─── */

typedef struct {
    ble_npl_event_fn *fn;
    void *arg;
    volatile bool queued;
} npl_event_t;

static npl_event_t *event_of(struct ble_npl_event *ev) {
    return (npl_event_t *)(uintptr_t)ev->dummy;
}

static void npl_event_init(struct ble_npl_event *ev, ble_npl_event_fn *fn, void *arg) {
    if (ev->dummy != 0) return;
    npl_event_t *e = espradio_malloc(sizeof(npl_event_t));
    if (!e) return;
    e->fn = fn;
    e->arg = arg;
    e->queued = false;
    ev->dummy = (int)(uintptr_t)e;
}

static void npl_event_deinit(struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    if (e) espradio_free(e);
    ev->dummy = 0;
}

static void npl_event_reset(struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    if (e) e->queued = false;
}

static bool npl_event_is_queued(struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    return e && e->queued;
}

static void *npl_event_get_arg(struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    return e ? e->arg : NULL;
}

static void npl_event_set_arg(struct ble_npl_event *ev, void *arg) {
    npl_event_t *e = event_of(ev);
    if (e) e->arg = arg;
}

static void npl_event_run(struct ble_npl_event *ev) {
    npl_event_t *e = ev ? event_of(ev) : NULL;
    if (e && e->fn) e->fn(ev);
}

static void *queue_of(struct ble_npl_eventq *evq) {
    return (void *)(uintptr_t)evq->dummy;
}

static void npl_eventq_init(struct ble_npl_eventq *evq) {
    evq->dummy = (int)(uintptr_t)espradio_queue_create_internal(EVENTQ_LEN, sizeof(void *));
}

static void npl_eventq_deinit(struct ble_npl_eventq *evq) {
    espradio_queue_delete_internal(queue_of(evq));
    evq->dummy = 0;
}

/* In a hardware ISR the queue lock may be held by the interrupted goroutine,
 * so use the ISR ring that schedOnce() drains into the queue. */
static void npl_eventq_put(struct ble_npl_eventq *evq, struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    if (!e || e->queued) return;
    e->queued = true;
    if (espradio_in_hw_isr()) {
        espradio_queue_send_from_isr(queue_of(evq), &ev, NULL);
    } else {
        espradio_queue_send(queue_of(evq), &ev, 0);
    }
}

static struct ble_npl_event *npl_eventq_get(struct ble_npl_eventq *evq, ble_npl_time_t tmo) {
    struct ble_npl_event *ev = NULL;
    void *q = queue_of(evq);
    uint64_t deadline = espradio_time_us_now() + (uint64_t)tmo * 1000u;
    for (;;) {
        if (espradio_queue_recv(q, &ev, 0) == 1) {
            npl_event_t *e = event_of(ev);
            if (e) e->queued = false;
            return ev;
        }
        if (tmo == 0) return NULL;
        if (tmo != BLE_NPL_TIME_FOREVER && espradio_time_us_now() >= deadline) return NULL;
    }
}

static void npl_eventq_remove(struct ble_npl_eventq *evq, struct ble_npl_event *ev) {
    npl_event_t *e = event_of(ev);
    if (!e || !e->queued) return;
    void *q = queue_of(evq);
    uint32_t n = espradio_queue_len(q);
    for (uint32_t i = 0; i < n; i++) {
        struct ble_npl_event *item = NULL;
        if (espradio_queue_recv(q, &item, 0) != 1) break;
        if (item != ev) espradio_queue_send(q, &item, 0);
    }
    e->queued = false;
}

static bool npl_eventq_is_empty(struct ble_npl_eventq *evq) {
    return espradio_queue_len(queue_of(evq)) == 0;
}

/* ─── Mutexes and semaphores ─── */

typedef struct {
    void *owner;
    volatile uint32_t depth;
} npl_mutex_t;

typedef struct {
    volatile uint16_t count;
} npl_sem_t;

static bool wait_expired(uint64_t deadline, ble_npl_time_t tmo) {
    if (tmo == 0) return true;
    if (tmo == BLE_NPL_TIME_FOREVER) return false;
    return espradio_time_us_now() >= deadline;
}

static ble_npl_error_t npl_mutex_init(struct ble_npl_mutex *mu) {
    npl_mutex_t *m = espradio_malloc(sizeof(npl_mutex_t));
    if (!m) return BLE_NPL_ENOMEM;
    m->owner = NULL;
    m->depth = 0;
    mu->dummy = (int)(uintptr_t)m;
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_mutex_deinit(struct ble_npl_mutex *mu) {
    espradio_free((void *)(uintptr_t)mu->dummy);
    mu->dummy = 0;
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_mutex_pend(struct ble_npl_mutex *mu, ble_npl_time_t tmo) {
    npl_mutex_t *m = (npl_mutex_t *)(uintptr_t)mu->dummy;
    if (!m) return BLE_NPL_INVALID_PARAM;
    void *me = espradio_task_get_current_task();
    uint64_t deadline = espradio_time_us_now() + (uint64_t)tmo * 1000u;
    for (;;) {
        uint32_t cs = npl_hw_enter_critical();
        if (m->depth == 0 || m->owner == me) {
            m->owner = me;
            m->depth++;
            npl_hw_exit_critical(cs);
            return BLE_NPL_OK;
        }
        npl_hw_exit_critical(cs);
        if (wait_expired(deadline, tmo)) return BLE_NPL_TIMEOUT;
        espradio_task_yield_go();
    }
}

static ble_npl_error_t npl_mutex_release(struct ble_npl_mutex *mu) {
    npl_mutex_t *m = (npl_mutex_t *)(uintptr_t)mu->dummy;
    if (!m || m->depth == 0) return BLE_NPL_INVALID_PARAM;
    uint32_t cs = npl_hw_enter_critical();
    if (--m->depth == 0) m->owner = NULL;
    npl_hw_exit_critical(cs);
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_sem_init(struct ble_npl_sem *sem, uint16_t tokens) {
    npl_sem_t *s = espradio_malloc(sizeof(npl_sem_t));
    if (!s) return BLE_NPL_ENOMEM;
    s->count = tokens;
    sem->dummy = (int)(uintptr_t)s;
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_sem_deinit(struct ble_npl_sem *sem) {
    espradio_free((void *)(uintptr_t)sem->dummy);
    sem->dummy = 0;
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_sem_pend(struct ble_npl_sem *sem, ble_npl_time_t tmo) {
    npl_sem_t *s = (npl_sem_t *)(uintptr_t)sem->dummy;
    if (!s) return BLE_NPL_INVALID_PARAM;
    uint64_t deadline = espradio_time_us_now() + (uint64_t)tmo * 1000u;
    for (;;) {
        uint32_t cs = npl_hw_enter_critical();
        if (s->count > 0) {
            s->count--;
            npl_hw_exit_critical(cs);
            return BLE_NPL_OK;
        }
        npl_hw_exit_critical(cs);
        if (wait_expired(deadline, tmo)) return BLE_NPL_TIMEOUT;
        espradio_task_yield_go();
    }
}

static ble_npl_error_t npl_sem_release(struct ble_npl_sem *sem) {
    npl_sem_t *s = (npl_sem_t *)(uintptr_t)sem->dummy;
    if (!s) return BLE_NPL_INVALID_PARAM;
    uint32_t cs = npl_hw_enter_critical();
    s->count++;
    npl_hw_exit_critical(cs);
    return BLE_NPL_OK;
}

static uint16_t npl_sem_get_count(struct ble_npl_sem *sem) {
    npl_sem_t *s = (npl_sem_t *)(uintptr_t)sem->dummy;
    return s ? s->count : 0;
}

/* ─── Time ─── */

/* NPL ticks are milliseconds, as in esp-hal esp-radio/src/time.rs. */
static ble_npl_time_t npl_time_get(void) {
    return (ble_npl_time_t)(espradio_time_us_now() / 1000u);
}

static ble_npl_error_t npl_time_ms_to_ticks(uint32_t ms, ble_npl_time_t *out) {
    *out = ms;
    return BLE_NPL_OK;
}

static ble_npl_error_t npl_time_ticks_to_ms(ble_npl_time_t ticks, uint32_t *out) {
    *out = ticks;
    return BLE_NPL_OK;
}

static ble_npl_time_t npl_time_ms_to_ticks32(uint32_t ms) { return ms; }
static uint32_t npl_time_ticks_to_ms32(ble_npl_time_t ticks) { return ticks; }

static void npl_time_delay(ble_npl_time_t ticks) {
    espradio_task_delay((uint32_t)espradio_task_ms_to_tick(ticks));
}

static uint32_t npl_get_time_forever(void) { return BLE_NPL_TIME_FOREVER; }

/* ─── Callouts ─── */

typedef struct {
    struct ets_timer timer;
    struct ble_npl_eventq *evq;
    struct ble_npl_event ev;
    ble_npl_time_t expiry;
} npl_callout_t;

static npl_callout_t *callout_of(struct ble_npl_callout *co) {
    return (npl_callout_t *)(uintptr_t)co->dummy;
}

static void callout_timer_cb(void *arg) {
    npl_callout_t *c = callout_of((struct ble_npl_callout *)arg);
    if (!c) return;
    if (c->evq) {
        npl_eventq_put(c->evq, &c->ev);
    } else {
        npl_event_run(&c->ev);
    }
}

static int npl_callout_init(struct ble_npl_callout *co, struct ble_npl_eventq *evq,
                            ble_npl_event_fn *fn, void *arg) {
    if (co->dummy == 0) {
        npl_callout_t *c = espradio_malloc(sizeof(npl_callout_t));
        if (!c) return -1;
        memset(c, 0, sizeof(*c));
        co->dummy = (int)(uintptr_t)c;
        npl_event_init(&c->ev, fn, arg);
        espradio_timer_setfn_ext(&c->timer, (void *)callout_timer_cb, co);
    }
    npl_callout_t *c = callout_of(co);
    c->evq = evq;
    return 0;
}

static ble_npl_error_t npl_callout_reset(struct ble_npl_callout *co, ble_npl_time_t ticks) {
    npl_callout_t *c = callout_of(co);
    if (!c) return BLE_NPL_INVALID_PARAM;
    c->expiry = npl_time_get() + ticks;
    espradio_timer_arm_ext(&c->timer, ticks, false);
    return BLE_NPL_OK;
}

static void npl_callout_stop(struct ble_npl_callout *co) {
    npl_callout_t *c = callout_of(co);
    if (c) espradio_timer_disarm_ext(&c->timer);
}

static void npl_callout_deinit(struct ble_npl_callout *co) {
    npl_callout_t *c = callout_of(co);
    if (!c) return;
    espradio_timer_disarm_ext(&c->timer);
    espradio_timer_done_ext(&c->timer);
    npl_event_deinit(&c->ev);
    espradio_free(c);
    co->dummy = 0;
}

static void npl_callout_mem_reset(struct ble_npl_callout *co) {
    npl_callout_stop(co);
}

static bool npl_callout_is_active(struct ble_npl_callout *co) {
    npl_callout_t *c = callout_of(co);
    return c && espradio_timer_is_active_ext(&c->timer);
}

static ble_npl_time_t npl_callout_get_ticks(struct ble_npl_callout *co) {
    npl_callout_t *c = callout_of(co);
    return c ? c->expiry : 0;
}

static uint32_t npl_callout_remaining_ticks(struct ble_npl_callout *co, ble_npl_time_t now) {
    npl_callout_t *c = callout_of(co);
    if (!c || !npl_callout_is_active(co)) return 0;
    return (int32_t)(c->expiry - now) > 0 ? c->expiry - now : 0;
}

static void npl_callout_set_arg(struct ble_npl_callout *co, void *arg) {
    npl_callout_t *c = callout_of(co);
    if (c) npl_event_set_arg(&c->ev, arg);
}

/* ─── Tasks ─── */

static bool npl_os_started(void) { return true; }

static void *npl_get_current_task_id(void) {
    return espradio_task_get_current_task();
}

/* Layout of npl_funcs_t from esp-hal esp-radio/src/ble/npl.rs. */
static const struct {
    bool (*os_started)(void);
    void *(*get_current_task_id)(void);
    void (*eventq_init)(struct ble_npl_eventq *);
    void (*eventq_deinit)(struct ble_npl_eventq *);
    struct ble_npl_event *(*eventq_get)(struct ble_npl_eventq *, ble_npl_time_t);
    void (*eventq_put)(struct ble_npl_eventq *, struct ble_npl_event *);
    void (*eventq_remove)(struct ble_npl_eventq *, struct ble_npl_event *);
    void (*event_run)(struct ble_npl_event *);
    bool (*eventq_is_empty)(struct ble_npl_eventq *);
    void (*event_init)(struct ble_npl_event *, ble_npl_event_fn *, void *);
    void (*event_deinit)(struct ble_npl_event *);
    void (*event_reset)(struct ble_npl_event *);
    bool (*event_is_queued)(struct ble_npl_event *);
    void *(*event_get_arg)(struct ble_npl_event *);
    void (*event_set_arg)(struct ble_npl_event *, void *);
    ble_npl_error_t (*mutex_init)(struct ble_npl_mutex *);
    ble_npl_error_t (*mutex_deinit)(struct ble_npl_mutex *);
    ble_npl_error_t (*mutex_pend)(struct ble_npl_mutex *, ble_npl_time_t);
    ble_npl_error_t (*mutex_release)(struct ble_npl_mutex *);
    ble_npl_error_t (*sem_init)(struct ble_npl_sem *, uint16_t);
    ble_npl_error_t (*sem_deinit)(struct ble_npl_sem *);
    ble_npl_error_t (*sem_pend)(struct ble_npl_sem *, ble_npl_time_t);
    ble_npl_error_t (*sem_release)(struct ble_npl_sem *);
    uint16_t (*sem_get_count)(struct ble_npl_sem *);
    int (*callout_init)(struct ble_npl_callout *, struct ble_npl_eventq *, ble_npl_event_fn *, void *);
    ble_npl_error_t (*callout_reset)(struct ble_npl_callout *, ble_npl_time_t);
    void (*callout_stop)(struct ble_npl_callout *);
    void (*callout_deinit)(struct ble_npl_callout *);
    void (*callout_mem_reset)(struct ble_npl_callout *);
    bool (*callout_is_active)(struct ble_npl_callout *);
    ble_npl_time_t (*callout_get_ticks)(struct ble_npl_callout *);
    uint32_t (*callout_remaining_ticks)(struct ble_npl_callout *, ble_npl_time_t);
    void (*callout_set_arg)(struct ble_npl_callout *, void *);
    uint32_t (*time_get)(void);
    ble_npl_error_t (*time_ms_to_ticks)(uint32_t, ble_npl_time_t *);
    ble_npl_error_t (*time_ticks_to_ms)(ble_npl_time_t, uint32_t *);
    ble_npl_time_t (*time_ms_to_ticks32)(uint32_t);
    uint32_t (*time_ticks_to_ms32)(ble_npl_time_t);
    void (*time_delay)(ble_npl_time_t);
    void (*hw_set_isr)(int, uint32_t);
    uint32_t (*hw_enter_critical)(void);
    void (*hw_exit_critical)(uint32_t);
    uint32_t (*get_time_forever)(void);
    uint8_t (*hw_is_in_critical)(void);
} s_npl_funcs = {
    npl_os_started, npl_get_current_task_id,
    npl_eventq_init, npl_eventq_deinit, npl_eventq_get, npl_eventq_put,
    npl_eventq_remove, npl_event_run, npl_eventq_is_empty,
    npl_event_init, npl_event_deinit, npl_event_reset, npl_event_is_queued,
    npl_event_get_arg, npl_event_set_arg,
    npl_mutex_init, npl_mutex_deinit, npl_mutex_pend, npl_mutex_release,
    npl_sem_init, npl_sem_deinit, npl_sem_pend, npl_sem_release, npl_sem_get_count,
    npl_callout_init, npl_callout_reset, npl_callout_stop, npl_callout_deinit,
    npl_callout_mem_reset, npl_callout_is_active, npl_callout_get_ticks,
    npl_callout_remaining_ticks, npl_callout_set_arg,
    npl_time_get, npl_time_ms_to_ticks, npl_time_ticks_to_ms,
    npl_time_ms_to_ticks32, npl_time_ticks_to_ms32, npl_time_delay,
    npl_hw_set_isr, npl_hw_enter_critical, npl_hw_exit_critical,
    npl_get_time_forever, npl_hw_is_in_critical,
};

/* ─── External functions ─── */

typedef void (*bt_isr_fn_t)(void *arg);

static bt_isr_fn_t s_isr_mac_fn;
static void       *s_isr_mac_arg;
static bt_isr_fn_t s_isr_timer_fn;
static void       *s_isr_timer_arg;

static int ext_intr_alloc(int source, int flags, void *handler, void *arg, void **ret_handle) {
    (void)flags;
    BLE_DBG("ble: intr_alloc source=%d\n", source);
    if (source == BT_MAC_INTR_SOURCE) {
        s_isr_mac_arg = arg;
        s_isr_mac_fn = (bt_isr_fn_t)handler;
    } else if (source == LP_TIMER_INTR_SOURCE) {
        s_isr_timer_arg = arg;
        s_isr_timer_fn = (bt_isr_fn_t)handler;
    } else {
        return -1;
    }
    if (ret_handle) *ret_handle = (void *)(uintptr_t)source;
    return 0;
}

static int ext_intr_free(void **ret_handle) {
    (void)ret_handle;
    return 0;
}

static void *ext_malloc(uint32_t size) { return espradio_malloc(size); }
static void  ext_free(void *p) { espradio_free(p); }

static int ext_task_create(void *func, const char *name, uint32_t stack_depth, void *param,
                           uint32_t prio, void *handle, uint32_t core_id) {
    BLE_DBG("ble: task_create %s\n", name ? name : "");
    return (int)espradio_task_create_pinned_to_core(func, name, stack_depth, param,
                                                    prio, handle, core_id);
}

static void ext_task_delete(void *handle) {
    espradio_task_delete(handle);
}

static void ext_osi_assert(uint32_t ln, const char *fn, uint32_t p1, uint32_t p2) {
    printf("ble: assert %s:%lu %lu %lu\n", fn ? fn : "", (unsigned long)ln,
           (unsigned long)p1, (unsigned long)p2);
    espradio_panic("BLE assert");
}

static uint32_t ext_os_random(void) { return esp_random(); }

/* The host does the pairing crypto. */
static int ext_ecc_gen_key_pair(uint8_t *pub, uint8_t *priv) {
    (void)pub; (void)priv;
    return -1;
}

static int ext_ecc_gen_dh_key(const uint8_t *x, const uint8_t *y, const uint8_t *priv, uint8_t *out) {
    (void)x; (void)y; (void)priv; (void)out;
    return -1;
}

static void ext_reset_modem(uint8_t mdl_opts, uint8_t start) {
    (void)mdl_opts; (void)start;
}

/* Layout of ext_funcs_t for the ESP32-C6, from esp-hal esp-radio/src/ble/npl.rs. */
static const struct {
    uint32_t ext_version;
    int (*esp_intr_alloc)(int, int, void *, void *, void **);
    int (*esp_intr_free)(void **);
    void *(*malloc)(uint32_t);
    void (*free)(void *);
    int (*task_create)(void *, const char *, uint32_t, void *, uint32_t, void *, uint32_t);
    void (*task_delete)(void *);
    void (*osi_assert)(uint32_t, const char *, uint32_t, uint32_t);
    uint32_t (*os_random)(void);
    int (*ecc_gen_key_pair)(uint8_t *, uint8_t *);
    int (*ecc_gen_dh_key)(const uint8_t *, const uint8_t *, const uint8_t *, uint8_t *);
    void (*esp_reset_modem)(uint8_t, uint8_t);
    uint32_t magic;
} s_ext_funcs = {
    EXT_FUNCS_VERSION,
    ext_intr_alloc, ext_intr_free, ext_malloc, ext_free,
    ext_task_create, ext_task_delete, ext_osi_assert, ext_os_random,
    ext_ecc_gen_key_pair, ext_ecc_gen_dh_key, ext_reset_modem,
    EXT_FUNCS_MAGIC,
};

/* ─── Interrupts ─── */

static void run_isr(bt_isr_fn_t fn, void *arg) {
    if (!fn) return;
    espradio_enter_hw_isr();
    fn(arg);
    espradio_exit_hw_isr();
}

void espradio_bt_isr_dispatch_mac(void) { run_isr(s_isr_mac_fn, s_isr_mac_arg); }
void espradio_bt_isr_dispatch_timer(void) { run_isr(s_isr_timer_fn, s_isr_timer_arg); }

/* Route the BT sources to their CPU lines with level type, as ESP-IDF does
 * for the WiFi lines (components/esp_wifi/esp32c6/esp_adapter.c). */
void espradio_bt_enable_hw_interrupts(void) {
    intr_matrix_set(0, BT_MAC_INTR_SOURCE, BT_CPU_INT_MAC);
    intr_matrix_set(0, LP_TIMER_INTR_SOURCE, BT_CPU_INT_TIMER);
    PLIC_TYPE_REG &= ~((1u << BT_CPU_INT_MAC) | (1u << BT_CPU_INT_TIMER));
    PLIC_PRI_REG(BT_CPU_INT_MAC) = 6u;
    PLIC_PRI_REG(BT_CPU_INT_TIMER) = 6u;
    PLIC_ENABLE_REG |= (1u << BT_CPU_INT_MAC) | (1u << BT_CPU_INT_TIMER);
    __asm__ volatile ("fence" ::: "memory");
}

/* The controller runs as a goroutine and its timers fire from schedOnce(). */
void espradio_bt_sched_tick(void) {}

/* ─── HCI ─── */

static uint8_t s_rx_buf[4 + 512];

static int hci_rx_evt(uint8_t *hci_ev, void *arg) {
    (void)arg;
    uint8_t len = hci_ev[1];
    s_rx_buf[0] = 0x04;
    memcpy(&s_rx_buf[1], hci_ev, (size_t)len + 2u);
    espradio_vhci_ring_push(s_rx_buf, (int)len + 3);
    r_ble_hci_trans_buf_free(hci_ev);
    return 0;
}

static int hci_rx_acl(struct os_mbuf *om, void *arg) {
    (void)arg;
    int n = 1;
    s_rx_buf[0] = 0x02;
    for (struct espradio_os_mbuf *m = (struct espradio_os_mbuf *)om; m; m = m->om_next) {
        if (n + m->om_len > (int)sizeof(s_rx_buf)) break;
        memcpy(&s_rx_buf[n], m->om_data, m->om_len);
        n += m->om_len;
    }
    espradio_vhci_ring_push(s_rx_buf, n);
    r_os_mbuf_free_chain(om);
    return 0;
}

static volatile int s_ble_enabled;

/* As esp_vhci_host_send_packet() in ESP-IDF components/bt/controller/esp32c6/bt.c. */
int espradio_vhci_write(const uint8_t *data, int len) {
    if (!s_ble_enabled || len < 2) return 0;
    if (data[0] == 0x01) {
        /* The controller has one command buffer. Wait until it frees the
         * buffer of the previous command. */
        uint8_t *cmd = r_ble_hci_trans_buf_alloc(BLE_HCI_TRANS_BUF_CMD);
        uint64_t deadline = espradio_time_us_now() + 1000000u;
        while (!cmd && espradio_time_us_now() < deadline) {
            espradio_task_yield_go();
            cmd = r_ble_hci_trans_buf_alloc(BLE_HCI_TRANS_BUF_CMD);
        }
        if (!cmd) return 0;
        memcpy(cmd, data + 1, (size_t)len - 1u);
        r_ble_hci_trans_hs_cmd_tx(cmd);
    } else if (data[0] == 0x02) {
        struct os_mbuf *om = r_os_msys_get_pkthdr((uint16_t)len, ACL_DATA_MBUF_LEADINGSPACE);
        if (!om) return 0;
        if (r_os_mbuf_append(om, data + 1, (uint16_t)(len - 1)) != 0) {
            r_os_mbuf_free_chain(om);
            return 0;
        }
        r_ble_hci_trans_hs_acl_tx(om);
    }
    return len;
}

/* ─── Controller start ─── */

static void ble_config_init(esp_bt_controller_config_t *cfg) {
    /* Same values as create_ble_config() in esp-hal os_adapter_esp32c6.rs. */
    memset(cfg, 0, sizeof(*cfg));
    cfg->config_version = CONFIG_VERSION;
    cfg->ble_ll_resolv_list_size = 4;
    cfg->ble_hci_evt_hi_buf_count = 30;
    cfg->ble_hci_evt_lo_buf_count = 8;
    cfg->ble_ll_sync_list_cnt = 5;
    cfg->ble_ll_sync_cnt = 0;
    cfg->ble_ll_rsp_dup_list_count = 20;
    cfg->ble_ll_adv_dup_list_count = 20;
    cfg->ble_ll_tx_pwr_dbm = 9;
    cfg->rtc_freq = 32000;
    cfg->ble_ll_sca = 60;
    cfg->ble_ll_scan_phy_number = 2;
    cfg->ble_ll_conn_def_auth_pyld_tmo = 3000;
    cfg->ble_ll_jitter_usecs = 16;
    cfg->ble_ll_sched_max_adv_pdu_usecs = 376;
    cfg->ble_ll_sched_direct_adv_max_usecs = 502;
    cfg->ble_ll_sched_adv_max_usecs = 852;
    cfg->ble_scan_rsp_data_max_len = 31;
    cfg->ble_ll_cfg_num_hci_cmd_pkts = 1;
    cfg->ble_ll_ctrl_proc_timeout_ms = 40000;
    cfg->nimble_max_connections = 2;
    cfg->ble_whitelist_size = 12;
    cfg->ble_acl_buf_size = 255;
    cfg->ble_acl_buf_count = 24;
    cfg->ble_hci_evt_buf_size = 70;
    cfg->ble_multi_adv_instances = 1;
    cfg->ble_ext_adv_max_size = 31;
    cfg->controller_task_stack_size = 4096;
    cfg->controller_task_prio = 253;
    cfg->controller_run_cpu = 0;
    cfg->cca_rssi_thresh = 256 - 65;
    cfg->ble_scan_classify_filter_enable = 1;
    cfg->main_xtal_freq = 40;
    cfg->version_num = espradio_hal_chip_minor_version_go();
    cfg->cpu_freq_mhz = 160;
    cfg->csa2_select = 1;
    cfg->scan_backoff_upperlimitmax = 256;
    cfg->vhci_enabled = 1;
    cfg->ch39_txpwr = 9;
    cfg->adv_rsv_cnt = 1;
    cfg->conn_rsv_cnt = 2;
    cfg->priority_level_cfg = (1u << 4) | (1u << 2);
    cfg->config_magic = CONFIG_MAGIC;
}

#define BLE_CHECK(call) do { int _rc = (call); if (_rc != 0) { \
    BLE_DBG("ble: %s -> %d\n", #call, _rc); return -__LINE__; } } while (0)

/* Same order as ble_init() in esp-hal esp-radio/src/ble/npl.rs. */
int espradio_ble_init(void) {
    static esp_bt_controller_config_t cfg;
    static uint16_t npl_info[5];

    if (s_ble_enabled) return 0;
    ble_config_init(&cfg);

    BLE_CHECK(esp_register_ext_funcs(&s_ext_funcs));
    espradio_hal_enable_bt_clocks_go();
    BLE_CHECK(esp_register_npl_funcs(&s_npl_funcs));
    BLE_CHECK(r_ble_get_npl_element_info(&cfg, npl_info));

    esp_phy_enable(PHY_MODEM_BT);
    bt_bb_v2_init_cmplx(1);
    coex_pti_v2();
    BLE_CHECK(esp_ble_register_bb_funcs());

    BLE_CHECK(r_ble_controller_init(&cfg));

    BLE_CHECK(base_stack_initEnv());
    BLE_CHECK(adv_stack_initEnv());
    BLE_CHECK(extAdv_stack_initEnv());
    BLE_CHECK(scan_stack_initEnv());
    BLE_CHECK(conn_stack_initEnv());
    BLE_CHECK(sync_stack_initEnv());
    BLE_CHECK(r_esp_ble_msys_init(256, 320, 12, 24, 1));
    BLE_CHECK(base_stack_enable());
    BLE_CHECK(adv_stack_enable());
    BLE_CHECK(extAdv_stack_enable());
    BLE_CHECK(scan_stack_enable());
    BLE_CHECK(sync_stack_enable());

    uint8_t mac[6], rev[6];
    espradio_hal_read_mac_go(mac, 2);
    for (int i = 0; i < 6; i++) rev[i] = mac[5 - i];
    r_esp_ble_ll_set_public_addr(rev);

    r_ble_hci_trans_init(0);
    r_ble_hci_trans_cfg_hs(hci_rx_evt, NULL, hci_rx_acl, NULL);

    BLE_CHECK(r_ble_controller_enable(1));
    s_ble_enabled = 1;
    BLE_DBG("ble: init ok\n");
    return 0;
}

/* Referenced by libble_app.a. Restart is not supported here. */
void esp_restart(void) {
    espradio_panic("esp_restart");
}

extern void gpio_matrix_out(uint32_t gpio, uint32_t signal_idx, bool out_inv, bool oen_inv);

void esp_rom_gpio_connect_out_signal(uint32_t gpio, uint32_t signal_idx, bool out_inv, bool oen_inv) {
    gpio_matrix_out(gpio, signal_idx, out_inv, oen_inv);
}

/* libble_app.a uses abs, which the TinyGo picolibc build does not include. */
__attribute__((weak)) int abs(int x) {
    return x < 0 ? -x : x;
}
