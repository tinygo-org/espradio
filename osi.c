//go:build esp32c3

#include "include.h"
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

/* Set to 1 to enable OSI callback logs (e.g. CGO_CFLAGS=-DESPRADIO_OSI_DEBUG=1). */
#ifndef ESPRADIO_OSI_DEBUG
#define ESPRADIO_OSI_DEBUG 1
#endif

#include <stdint.h>

static volatile uintptr_t g_task_stack_bottom;
#define ESPRADIO_PHY_MODEM_WIFI 1u

void espradio_set_task_stack_bottom(uintptr_t bottom) {
    g_task_stack_bottom = bottom;
}

/* Returns remaining stack bytes for the current task (when called from WiFi task goroutine). */
unsigned long espradio_stack_remaining(void) {
    if (g_task_stack_bottom == 0) return 0;
    uintptr_t sp;
    __asm__ volatile ("mv %0, sp" : "=r"(sp));
    if (sp <= g_task_stack_bottom) return 0;
    return (unsigned long)(sp - g_task_stack_bottom);
}

__attribute__((noreturn))
void espradio_panic(char *s);
extern uint64_t espradio_time_us_now(void);
extern void esp_phy_enable(uint32_t modem);
extern void esp_phy_disable(uint32_t modem);
extern int esp_phy_update_country_info(const char *country) __attribute__((weak));
extern void phy_wifi_enable_set(uint8_t enable);
extern void espradio_hal_init_clocks_go(void);
extern void espradio_hal_disable_clocks_go(void);
extern void espradio_hal_wifi_rtc_enable_iso_go(void);
extern void espradio_hal_wifi_rtc_disable_iso_go(void);
extern void espradio_hal_reset_wifi_mac_go(void);
extern int espradio_hal_read_mac_go(uint8_t *mac, unsigned int iftype);
extern void intr_matrix_set(uint32_t cpu_no, uint32_t model_num, uint32_t intr_num);
extern void ets_isr_attach(uint32_t intr_num, void (*fn)(void *), void *arg);
extern void ets_isr_mask(uint32_t mask);
extern void ets_isr_unmask(uint32_t mask);

extern wifi_osi_funcs_t espradio_osi_funcs;
static void espradio_wifi_reset_mac(void);
void espradio_timer_pending_reset(void);

/* Подготовка памяти под Wi‑Fi: ROM_Boot_Cache_Init() предназначен для самого старта;
 * вызов из приложения переконфигурирует кэш и даёт Illegal Instruction сразу после.
 * Оставляем no-op; при необходимости искать другой способ (например, только Cache_MMU_Init с корректными аргументами). */
#define G_OSI_FUNCS_P_ADDR 0x3fcdf954

_Static_assert(offsetof(wifi_osi_funcs_t, _coex_pti_get) == 0x1a8,
               "_coex_pti_get must be at offset 0x1a8 for blob compatibility");
#define G_WDEV_LAST_DESC_RESET_PTR_ADDR 0x3ff1ee40
static uint8_t s_wdev_last_desc_reset_byte;

wifi_osi_funcs_t *g_osi_funcs_p;

void espradio_wdev_last_desc_reset_prepare(void) {
    *(volatile uint32_t *)G_WDEV_LAST_DESC_RESET_PTR_ADDR = (uint32_t)&s_wdev_last_desc_reset_byte;
    s_wdev_last_desc_reset_byte = 0;
}

void espradio_prepare_memory_for_wifi(void) {
    g_osi_funcs_p = &espradio_osi_funcs;
    *(volatile uint32_t *)G_OSI_FUNCS_P_ADDR = (uint32_t)&espradio_osi_funcs;
#if ESPRADIO_OSI_DEBUG
    printf("osi: prepare_memory_for_wifi (no-op wdev_last_desc_reset_ptr)\n");
#endif
}

void espradio_ensure_osi_ptr(void) {
    g_osi_funcs_p = &espradio_osi_funcs;
    *(volatile uint32_t *)G_OSI_FUNCS_P_ADDR = (uint32_t)&espradio_osi_funcs;
    memcpy(&g_wifi_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));
#if ESPRADIO_OSI_DEBUG
    printf("osi: ensure_osi_ptr\n");
#endif
}

esp_err_t espradio_esp_wifi_start(void) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: esp_wifi_start (write table then call blob)\n");
#endif
    *(volatile uint32_t *)G_OSI_FUNCS_P_ADDR = (uint32_t)&espradio_osi_funcs;
    g_osi_funcs_p = &espradio_osi_funcs;
    espradio_timer_pending_reset();
    return esp_wifi_start();
}

/* Simple printf backend expected by libcoexist.a. */
__attribute__((weak)) void coexist_printf(const char *format, ...) {
#if ESPRADIO_OSI_DEBUG
    va_list args;
    va_start(args, format);
    printf("coexist: ");
    vprintf(format, args);
    va_end(args);
#endif
}

/**************************************************************************
 * Name: wifi_env_is_chip
 *
 * Description:
 *   Config chip environment.
 *
 * Returned Value:
 *   True if on chip or false if on FPGA.
 *************************************************************************/
static bool espradio_env_is_chip(void) {
    return true;
}

/* ISR functions — defined in isr.c */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio);
void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num);
void espradio_set_isr(int32_t n, void *f, void *arg);
void espradio_call_saved_isr(int32_t n);
bool espradio_is_from_isr(void);
void espradio_ints_on(uint32_t mask);
void espradio_ints_off(uint32_t mask);
void espradio_task_yield_from_isr(void);
int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw);

void *espradio_spin_lock_create(void);
void espradio_yield_and_fire_pending_timers(void);
void espradio_task_yield_go(void);
uint32_t espradio_queue_len(void *ptr);

void espradio_spin_lock_delete(void *lock);

uint32_t espradio_wifi_int_disable(void *wifi_int_mux);

void espradio_wifi_int_restore(void *wifi_int_mux, uint32_t tmp);

void *espradio_semphr_create(uint32_t max, uint32_t init);

void espradio_semphr_delete(void *semphr);

int32_t espradio_semphr_take(void *semphr, uint32_t block_time_tick);

int32_t espradio_semphr_give(void *semphr);

void *espradio_wifi_thread_semphr_get(void);

void *espradio_recursive_mutex_create(void);

static void *espradio_mutex_create(void) {
    return espradio_recursive_mutex_create();
}

void espradio_mutex_delete(void *mutex);

int32_t espradio_mutex_lock(void *mutex);

int32_t espradio_mutex_unlock(void *mutex);

static void *espradio_queue_create(uint32_t queue_len, uint32_t item_size) {
    espradio_panic("todo: _queue_create");
}

static void espradio_queue_delete(void *queue) {
    espradio_panic("todo: _queue_delete");
}

int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);

static int32_t espradio_queue_send_to_back(void *queue, void *item, uint32_t block_time_tick) {
    return espradio_queue_send(queue, item, block_time_tick);
}

static int32_t espradio_queue_send_to_front(void *queue, void *item, uint32_t block_time_tick) {
    return espradio_queue_send(queue, item, block_time_tick);
}

int32_t espradio_queue_recv(void *queue, void *item, uint32_t block_time_tick);

static uint32_t espradio_queue_msg_waiting(void *queue) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: queue_msg_waiting q=%p\n", (void *)queue);
#endif
    return espradio_queue_len(queue);
}

static void *espradio_event_group_create(void) {
    espradio_panic("todo: _event_group_create");
}

static void espradio_event_group_delete(void *event) {
    espradio_panic("todo: _event_group_delete");
}

static uint32_t espradio_event_group_set_bits(void *event, uint32_t bits) {
    espradio_panic("todo: _event_group_set_bits");
}

static uint32_t espradio_event_group_clear_bits(void *event, uint32_t bits) {
    espradio_panic("todo: _event_group_clear_bits");
}

static uint32_t espradio_event_group_wait_bits(void *event, uint32_t bits_to_wait_for, int clear_on_exit, int wait_for_all_bits, uint32_t block_time_tick) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: event_group_wait_bits ev=%p bits=0x%lx\n", (void *)event, (unsigned long)bits_to_wait_for);
#endif
    (void)event;
    (void)clear_on_exit;
    (void)wait_for_all_bits;
    (void)block_time_tick;
    return bits_to_wait_for;
}

void espradio_run_task(void *task_func, void *task_handle) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: run_task fn=%p stack_left=%lu\n", (void *)task_func, (unsigned long)espradio_stack_remaining());
#endif
    void (*fn)(void *task_handle) = task_func;
    fn(task_handle);
}

int32_t espradio_task_create_pinned_to_core(void *task_func, const char *name, uint32_t stack_depth, void *param, uint32_t prio, void *task_handle, uint32_t core_id);

static int32_t espradio_task_create(void *task_func, const char *name, uint32_t stack_depth, void *param, uint32_t prio, void *task_handle) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: task_create name=%s fn=%p stack=%lu prio=%lu param=%p\n",
           name ? name : "(null)", task_func, (unsigned long)stack_depth, (unsigned long)prio, param);
    printf("CCHK: task_create called\n");
    fflush(stdout);
#endif
    return espradio_task_create_pinned_to_core(task_func, name, stack_depth, param, prio, task_handle, 0);
}

static int32_t espradio_task_create_pinned_to_core_wrap(void *task_func, const char *name, uint32_t stack_depth, void *param, uint32_t prio, void *task_handle, uint32_t core_id) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: task_create_pinned name=%s fn=%p stack=%lu prio=%lu core=%lu param=%p\n",
           name ? name : "(null)", task_func, (unsigned long)stack_depth, (unsigned long)prio, (unsigned long)core_id, param);
    printf("CCHK: task_create_pinned called\n");
    fflush(stdout);
#endif
    return espradio_task_create_pinned_to_core(task_func, name, stack_depth, param, prio, task_handle, core_id);
}

void espradio_task_delete(void *task_handle);

void espradio_task_delay(uint32_t tick);

int32_t espradio_task_ms_to_tick(uint32_t ms);

void *espradio_task_get_current_task(void);

static int32_t espradio_task_get_max_priority(void) {
    return 255;
}

static unsigned espradio_alloc_count;
static unsigned espradio_free_count;

void *espradio_arena_alloc(size_t size);
void *espradio_arena_calloc(size_t n, size_t size);
void *espradio_arena_realloc(void *ptr, size_t new_size);
void  espradio_arena_free(void *p);

static void *espradio_malloc(size_t size) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: malloc %zu\n", size);
#endif
    espradio_alloc_count++;
    return espradio_arena_alloc(size);
}

static void espradio_free(void *p) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: free %p\n", (void *)p);
#endif
    if (p) espradio_free_count++;
    espradio_arena_free(p);
}

/* Minimal esp_event implementation: queue events, dispatch from run_once (called from Go).
 * Matches IDF semantics so the driver sees _event_post return 0 and does not take the error path. */
typedef struct event_item {
    char *base;
    int32_t id;
    void *data;
    size_t data_size;
    struct event_item *next;
} event_item_t;
typedef struct event_handler {
    esp_event_base_t base;
    int32_t id;
    esp_event_handler_t handler;
    void *arg;
    struct event_handler *next;
} event_handler_t;
static event_item_t *s_event_head;
static event_item_t *s_event_tail;
static event_handler_t *s_handler_head;
static volatile int s_event_loop_ready;
static int s_event_lock;
static unsigned s_event_queued;
static const char s_wifi_event_base[] = "WIFI_EVENT";
extern void espradio_event_loop_kick_go(void);
static void event_lock(void) {
    unsigned spins = 0;
    while (__sync_lock_test_and_set(&s_event_lock, 1)) {
        spins++;
#if ESPRADIO_OSI_DEBUG
        if ((spins & 0x3ff) == 0) {
            printf("osi: event_lock waiting spins=%u queued=%u\n", spins, (unsigned)s_event_queued);
        }
#endif
        espradio_task_yield_go();
    }
}
static void event_unlock(void) {
    __sync_lock_release(&s_event_lock);
}

static char *dup_str(const char *s) {
    if (!s) return NULL;
    size_t n = strlen(s) + 1;
    char *p = (char *)espradio_arena_alloc(n);
    if (p) memcpy(p, s, n);
    return p;
}

esp_err_t esp_event_loop_create_default(void) {
    s_event_head = s_event_tail = NULL;
    s_handler_head = NULL;
    s_event_loop_ready = 1;
    return 0;
}
esp_err_t esp_event_loop_delete_default(void) {
    s_event_loop_ready = 0;
    event_lock();
    while (s_event_head) {
        event_item_t *e = s_event_head;
        s_event_head = e->next;
        if (e->base != s_wifi_event_base)
            espradio_arena_free(e->base);
        espradio_arena_free(e->data);
        espradio_arena_free(e);
    }
    s_event_tail = NULL;
    while (s_handler_head) {
        event_handler_t *h = s_handler_head;
        s_handler_head = h->next;
        espradio_arena_free(h);
    }
    event_unlock();
    return 0;
}
esp_err_t esp_event_handler_register(esp_event_base_t event_base, int32_t event_id,
                                     esp_event_handler_t event_handler, void *event_handler_arg) {
    event_handler_t *h = (event_handler_t *)espradio_arena_alloc(sizeof(*h));
    if (!h) return -1;
    h->base = event_base;
    h->id = event_id;
    h->handler = event_handler;
    h->arg = event_handler_arg;
    h->next = s_handler_head;
    s_handler_head = h;
    return 0;
}

void espradio_event_loop_run_once(void) {
    if (!s_event_loop_ready) return;
#if ESPRADIO_OSI_DEBUG
    static uint32_t s_event_loop_idle_log_throttle = 0;
    if ((s_event_loop_idle_log_throttle & 0x1ffu) == 0) {
        printf("osi: event_loop_run_once enter queued=%u\n", (unsigned)s_event_queued);
    }
#endif
    event_lock();
    event_item_t *e = s_event_head;
    if (!e) {
        event_unlock();
#if ESPRADIO_OSI_DEBUG
        if ((s_event_loop_idle_log_throttle & 0x1ffu) == 0) {
            printf("osi: event_loop_run_once empty\n");
        }
        s_event_loop_idle_log_throttle++;
#endif
        return;
    }
    s_event_head = e->next;
    if (!s_event_head) s_event_tail = NULL;
    if (s_event_queued > 0) s_event_queued--;
    event_unlock();
#if ESPRADIO_OSI_DEBUG
    s_event_loop_idle_log_throttle = 0;
    printf("osi: event_loop_run_once dispatch base=%s id=%ld queued=%u\n",
           e->base ? e->base : "(null)", (long)e->id, (unsigned)s_event_queued);
#endif
    const char *base = e->base ? e->base : "(null)";
    for (event_handler_t *h = s_handler_head; h; h = h->next) {
        if ((!h->base || strcmp(h->base, base) == 0) && (h->id == ESP_EVENT_ANY_ID || h->id == e->id) && h->handler)
            h->handler(h->arg, (esp_event_base_t)base, e->id, e->data);
    }
    if (e->base != s_wifi_event_base)
        espradio_arena_free(e->base);
    espradio_arena_free(e->data);
    espradio_arena_free(e);
}

static void espradio_dummy_event_cb(void *arg, esp_event_base_t base, int32_t id, void *data) {
    (void)arg;
#if ESPRADIO_OSI_DEBUG
    printf("osi: event_cb base=%s id=%ld data=%p\n", base ? base : "(null)", (long)id, data);
#endif
}

void espradio_event_register_default_cb(void) {
    if (esp_event_loop_create_default() != 0) return;
    esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, espradio_dummy_event_cb, NULL);
}

/**************************************************************************
 * Name: esp_event_post (osi _event_post)
 * Queue event and return 0 so driver does not take the error path.
 * Вызывается из wifi task, в т.ч. для HOME_CHANNEL_CHANGE (41/43).
 *************************************************************************/
static int32_t espradio_event_post(const char* event_base, int32_t event_id, void* event_data, size_t event_data_size, uint32_t ticks_to_wait) {
    (void)ticks_to_wait;
#if ESPRADIO_OSI_DEBUG
    printf("CCHK: event_post called\n");
    fflush(stdout);
    uint8_t b0 = 0, b1 = 0, b2 = 0, b3 = 0, b4 = 0, b5 = 0, b6 = 0, b7 = 0;
    uint32_t scan_status = 0;
    uint8_t scan_number = 0;
    uint8_t scan_id = 0;
    if (event_data && event_data_size > 0) {
        const uint8_t *p = (const uint8_t *)event_data;
        b0 = p[0];
        if (event_data_size > 1) b1 = p[1];
        if (event_data_size > 2) b2 = p[2];
        if (event_data_size > 3) b3 = p[3];
        if (event_data_size > 4) b4 = p[4];
        if (event_data_size > 5) b5 = p[5];
        if (event_data_size > 6) b6 = p[6];
        if (event_data_size > 7) b7 = p[7];
        if (event_data_size >= 6 && event_base && strcmp(event_base, s_wifi_event_base) == 0 && event_id == 1) {
            scan_status = (uint32_t)b0 | ((uint32_t)b1 << 8) | ((uint32_t)b2 << 16) | ((uint32_t)b3 << 24);
            scan_number = b4;
            scan_id = b5;
        }
    }
    printf("osi: event_post base=%s id=%ld size=%zu data=%p bytes=[%u,%u,%u,%u,%u,%u,%u,%u] stack_left=%lu\n",
           event_base ? event_base : "(null)",
           (long)event_id,
           (size_t)event_data_size,
           event_data,
           (unsigned)b0, (unsigned)b1, (unsigned)b2, (unsigned)b3,
           (unsigned)b4, (unsigned)b5, (unsigned)b6, (unsigned)b7,
           (unsigned long)espradio_stack_remaining());
    if (event_base && strcmp(event_base, s_wifi_event_base) == 0 && event_id == 1 && event_data_size >= 6) {
        printf("osi: event_post scan_done status=%lu number=%u scan_id=%u\n",
               (unsigned long)scan_status, (unsigned)scan_number, (unsigned)scan_id);
    }
#endif
    if (!s_event_loop_ready) return 0;
    event_item_t *e = (event_item_t *)espradio_arena_alloc(sizeof(*e));
    if (!e) return -1;
    if (event_base && strcmp(event_base, s_wifi_event_base) == 0)
        e->base = (char *)s_wifi_event_base;
    else
        e->base = dup_str(event_base);
    e->id = event_id;
    e->data_size = event_data_size;
    e->data = NULL;
    if (event_data_size > 0 && event_data) {
        e->data = espradio_arena_alloc(event_data_size);
        if (e->data) memcpy(e->data, event_data, event_data_size);
    }
    e->next = NULL;
    event_lock();
    if (s_event_tail) s_event_tail->next = e;
    else s_event_head = e;
    s_event_tail = e;
    s_event_queued++;
    event_unlock();
    espradio_event_loop_kick_go();
    // Keep esp_event_post asynchronous (IDF-like): wake scheduler and yield.
    espradio_task_yield_go();
#if ESPRADIO_OSI_DEBUG
    printf("osi: event_post queued base=%s id=%ld queued=%u\n",
           event_base ? event_base : "(null)", (long)event_id, (unsigned)s_event_queued);
#endif
    return 0;
}

/**************************************************************************
 * Name: esp_get_free_heap_size
 *
 * Description:
 *   Get free heap size by byte.
 *
 * Returned Value:
 *   Free heap size.
 *************************************************************************/
static uint32_t espradio_get_free_heap_size(void) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: get_free_heap_size\n");
#endif
    return 256 * 1024;
}

static uint32_t espradio_rand(void) {
    static uint32_t s_rng = 0x9e3779b9u;
    uint32_t t = (uint32_t)espradio_time_us_now();
    s_rng ^= t + 0x85ebca6bu + (s_rng << 6) + (s_rng >> 2);
    s_rng ^= s_rng << 13;
    s_rng ^= s_rng >> 17;
    s_rng ^= s_rng << 5;
    return s_rng;
}

static void espradio_dport_access_stall_other_cpu_start_wrap(void) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: dport_start_wrap\n");
#endif
}

static void espradio_dport_access_stall_other_cpu_end_wrap(void) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: dport_end_wrap\n");
#endif
}

/* Stub: request 80 MHz APB clock for WiFi — no-op (clocks are already enabled in init). */
static void espradio_wifi_apb80m_request(void) {
}

static void espradio_wifi_apb80m_release(void) {
}

static void espradio_phy_disable(void) {
    phy_wifi_enable_set(0);
    esp_phy_disable(ESPRADIO_PHY_MODEM_WIFI);
#if ESPRADIO_OSI_DEBUG
    printf("osi: phy_disable\n");
#endif
}

static void espradio_phy_enable(void) {
    esp_phy_enable(ESPRADIO_PHY_MODEM_WIFI);
    phy_wifi_enable_set(1);
#if ESPRADIO_OSI_DEBUG
    printf("osi: phy_enable\n");
#endif
}

static int espradio_phy_update_country_info(const char* country) {
    static char s_country[4];
    int rc = 0;
    if (esp_phy_update_country_info) {
        rc = esp_phy_update_country_info(country);
    }
    if (country) {
        s_country[0] = country[0];
        s_country[1] = country[1];
        s_country[2] = country[2];
        s_country[3] = 0;
    } else {
        s_country[0] = 0;
        s_country[1] = 0;
        s_country[2] = 0;
        s_country[3] = 0;
    }
#if ESPRADIO_OSI_DEBUG
    printf("osi: phy_update_country_info country=%p iso=%s rc=%d\n",
           (void *)country, s_country, rc);
#endif
    return rc;
}

/* Stub: returns a zero MAC; production should read from eFuse. */
static int espradio_read_mac(uint8_t* mac, unsigned int type) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: read_mac type=%u\n", type);
#endif
    if (mac == NULL) {
        return -1;
    }

    int rc = espradio_hal_read_mac_go(mac, type);
#if ESPRADIO_OSI_DEBUG
    printf("osi: read_mac rc=%d -> %02x:%02x:%02x:%02x:%02x:%02x\n",
           rc, mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
#endif
    return rc;
}

#define TIMER_SLOTS 64
static struct {
    void *ptimer;
    void (*fn)(void *);
    void *arg;
    bool active;
    bool periodic;
    bool pending_setfn;
    uint64_t interval_us;
    uint64_t deadline_us;
} timer_slots[TIMER_SLOTS];
static unsigned timer_slots_used;

void espradio_timer_fire(void *ptimer);

void espradio_timer_pending_reset(void) {
    memset(timer_slots, 0, sizeof(timer_slots));
    timer_slots_used = 0;
}

static int timer_slot_find(void *ptimer) {
    for (unsigned i = 0; i < timer_slots_used; i++)
        if (timer_slots[i].ptimer == ptimer)
            return (int)i;
    return -1;
}

static int timer_slot_alloc(void *ptimer) {
    int i = timer_slot_find(ptimer);
    if (i >= 0) return i;
    if (timer_slots_used >= TIMER_SLOTS) return -1;
    i = (int)timer_slots_used++;
    timer_slots[i].ptimer = ptimer;
    return i;
}

int espradio_timer_poll_due(int max_fire);
void espradio_task_yield_go(void);
void espradio_event_loop_kick_go(void);

void espradio_timer_fire(void *ptimer);

/**************************************************************************
 * Name: timer_setfn
 *
 * Description:
 *   Set timer callback and arg; store in ets_timer (blob) and in slots.
 *
 * Input Parameters:
 *   ptimer    - Timer handle
 *   pfunction - Callback
 *   parg      - Callback argument
 * Совместимость 1:1 с esp-wifi timer_compat: timer_setfn только регистрирует callback/arg
 * и сбрасывает active-состояние; callback исполняется только через timer_arm/timer_arm_us.
 *************************************************************************/
static void espradio_timer_setfn(void *ptimer, void *pfunction, void *parg) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_setfn ptimer=%p fn=%p arg=%p\n", (void *)ptimer, (void *)pfunction, (void *)parg);
#endif
    if (ptimer) {
        struct ets_timer *t = (struct ets_timer *)ptimer;
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_setfn before ptimer=%p expire=%lu period=%lu func=%p priv=%p next=%p\n",
               (void *)ptimer,
               (unsigned long)t->expire,
               (unsigned long)t->period,
               (void *)t->func,
               t->priv,
               (void *)t->next);
#endif
        t->next = NULL;
        t->period = 0;
        t->func = (void (*)(void *))pfunction;
        t->priv = parg;
        t->expire = 0;
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_setfn after  ptimer=%p expire=%lu period=%lu func=%p priv=%p next=%p\n",
               (void *)ptimer,
               (unsigned long)t->expire,
               (unsigned long)t->period,
               (void *)t->func,
               t->priv,
               (void *)t->next);
#endif
    }
    int i = timer_slot_alloc(ptimer);
    if (i >= 0) {
        timer_slots[i].fn = (void (*)(void *))pfunction;
        timer_slots[i].arg = parg;
        timer_slots[i].active = false;
        timer_slots[i].periodic = false;
        timer_slots[i].pending_setfn = false;
        timer_slots[i].interval_us = 0;
        timer_slots[i].deadline_us = 0;
    }
    // IDF-compatible behavior: timer_setfn only registers callback/arg.
}

int espradio_fire_one_pending_timer(void) {
    return 0;
}

void espradio_fire_pending_timers(void) {
    (void)0;
}

static void espradio_timer_disarm(void *timer) {
    int i = timer_slot_find(timer);
    if (i >= 0) {
        timer_slots[i].active = false;
        timer_slots[i].pending_setfn = false;
    }
}

static void espradio_timer_done(void *ptimer) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_done ptimer=%p\n", (void *)ptimer);
#endif
    int i = timer_slot_find(ptimer);
    if (i >= 0) {
        timer_slots[i].active = false;
        timer_slots[i].periodic = false;
        timer_slots[i].fn = NULL;
        timer_slots[i].arg = NULL;
        if ((unsigned)i + 1 < timer_slots_used) {
            memmove(&timer_slots[i], &timer_slots[i + 1], (timer_slots_used - (unsigned)i - 1) * sizeof(timer_slots[0]));
        }
        timer_slots_used--;
    }
    if (ptimer) {
        struct ets_timer *t = (struct ets_timer *)ptimer;
        t->priv = NULL;
        t->func = NULL;
    }
}

/**************************************************************************
 * Name: timer_arm
 *
 * Description:
 *   Start timer (one-shot or repeat).
 *
 * Input Parameters:
 *   timer - Timer handle
 *   tmout - Timeout in ticks
 *   repeat - true if periodic
 *************************************************************************/
static void espradio_timer_arm(void *timer, uint32_t tmout, bool repeat) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_arm timer=%p tmout=%lu repeat=%d\n", (void *)timer, (unsigned long)tmout, (int)repeat);
#endif
    int i = timer_slot_find(timer);
    if (i < 0) {
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_arm not found timer=%p\n", timer);
#endif
        return;
    }
    uint64_t us = (uint64_t)tmout * 1000ULL;
    if (us == 0) us = 1;
    uint64_t now = espradio_time_us_now();
    timer_slots[i].active = true;
    timer_slots[i].periodic = repeat;
    timer_slots[i].pending_setfn = false;
    timer_slots[i].interval_us = us;
    timer_slots[i].deadline_us = now + us;
    if (tmout == 0) {
        espradio_timer_fire(timer);
        return;
    }
}

void espradio_timer_fire(void *ptimer) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_fire begin ptimer=%p\n", (void *)ptimer);
#endif
    int i = timer_slot_find(ptimer);
    void (*fn)(void *) = NULL;
    void *arg = NULL;
    if (i >= 0) {
        if (!timer_slots[i].active) {
#if ESPRADIO_OSI_DEBUG
            printf("osi: timer_fire skip inactive ptimer=%p\n", (void *)ptimer);
#endif
            return;
        }
        if (!timer_slots[i].periodic) {
            timer_slots[i].active = false;
            timer_slots[i].pending_setfn = false;
        }
    }
    if (i >= 0 && timer_slots[i].fn) {
        fn = timer_slots[i].fn;
        arg = timer_slots[i].arg;
    } else if (ptimer) {
        struct ets_timer *t = (struct ets_timer *)ptimer;
        fn = t->func;
        arg = t->priv;
    }
 #if ESPRADIO_OSI_DEBUG
    printf("osi: timer_fire resolved ptimer=%p slot=%d fn=%p arg=%p\n", (void *)ptimer, i, (void *)fn, arg);
 #endif
    if (fn) {
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_fire calling fn=%p arg=%p\n", (void *)fn, arg);
#endif
        fn(arg);
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_fire returned fn=%p arg=%p\n", (void *)fn, arg);
#endif
    }
#if ESPRADIO_OSI_DEBUG
    else
        printf("osi: timer_fire no slot and no fn in ptimer\n");
    printf("osi: timer_fire end ptimer=%p\n", (void *)ptimer);
#endif
}

static void espradio_timer_arm_us(void *ptimer, uint32_t us, bool repeat) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_arm_us ptimer=%p us=%lu repeat=%d\n", (void *)ptimer, (unsigned long)us, (int)repeat);
#endif
    int i = timer_slot_find(ptimer);
    if (i < 0) {
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_arm_us not found ptimer=%p\n", ptimer);
#endif
        return;
    }
    uint64_t usec = us;
    if (usec == 0) usec = 1;
    uint64_t now = espradio_time_us_now();
    timer_slots[i].active = true;
    timer_slots[i].periodic = repeat;
    timer_slots[i].pending_setfn = false;
    timer_slots[i].interval_us = usec;
    timer_slots[i].deadline_us = now + usec;
    if (us == 0) {
        espradio_timer_fire(ptimer);
        return;
    }
}

int espradio_timer_poll_due(int max_fire) {
    if (max_fire <= 0) {
        return 0;
    }
    int fired = 0;
    uint64_t now = espradio_time_us_now();
    for (unsigned i = 0; i < timer_slots_used; i++) {
        if (timer_slots[i].active) {
            continue;
        }
        struct ets_timer *t = (struct ets_timer *)timer_slots[i].ptimer;
        if (!t || !t->func || t->expire == 0) {
            continue;
        }
        uint64_t interval_us = (uint64_t)t->expire * 1000ULL;
        if (interval_us == 0) {
            interval_us = 1;
        }
        timer_slots[i].active = true;
        timer_slots[i].periodic = (t->period != 0);
        timer_slots[i].interval_us = interval_us;
        timer_slots[i].deadline_us = now + interval_us;
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_poll_due adopt ets_timer idx=%u ptimer=%p expire=%lu period=%lu\n",
               i, timer_slots[i].ptimer, (unsigned long)t->expire, (unsigned long)t->period);
#endif
    }
    for (int pass = 0; pass < max_fire; pass++) {
        int idx = -1;
        for (unsigned i = 0; i < timer_slots_used; i++) {
            if (timer_slots[i].active && timer_slots[i].deadline_us <= now) {
                idx = (int)i;
                break;
            }
        }
        if (idx < 0) {
            break;
        }
        void *ptimer = timer_slots[idx].ptimer;
#if ESPRADIO_OSI_DEBUG
        printf("osi: timer_poll_due fire idx=%d ptimer=%p pending=%d active=%d periodic=%d\n",
               idx, ptimer, 0, (int)timer_slots[idx].active, (int)timer_slots[idx].periodic);
#endif
        if (timer_slots[idx].active && timer_slots[idx].periodic) {
            timer_slots[idx].deadline_us = now + timer_slots[idx].interval_us;
        }
        espradio_timer_fire(ptimer);
        fired++;
        now = espradio_time_us_now();
    }
#if ESPRADIO_OSI_DEBUG
    if (fired > 0) {
        printf("osi: timer_poll_due fired=%d used=%u\n", fired, timer_slots_used);
    }
#endif
    return fired;
}

static void timer_setfn_wrapper(void *ptimer, void *pfunction, void *parg) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_setfn_wrapper ptimer=%p fn=%p arg=%p\n", ptimer, pfunction, parg);
#endif
    espradio_timer_setfn(ptimer, pfunction, parg);
}

static void timer_arm_wrapper(void *ptimer, uint32_t tmout, bool repeat) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_arm_wrapper ptimer=%p tmout=%lu repeat=%d\n", ptimer, (unsigned long)tmout, (int)repeat);
#endif
    espradio_timer_arm(ptimer, tmout, repeat);
}

static void timer_arm_us_wrapper(void *ptimer, uint32_t us, bool repeat) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_arm_us_wrapper ptimer=%p us=%lu repeat=%d\n", ptimer, (unsigned long)us, (int)repeat);
#endif
    espradio_timer_arm_us(ptimer, us, repeat);
}

static void timer_disarm_wrapper(void *ptimer) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_disarm_wrapper ptimer=%p\n", ptimer);
#endif
    espradio_timer_disarm(ptimer);
}

static void timer_done_wrapper(void *ptimer) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: timer_done_wrapper ptimer=%p\n", ptimer);
#endif
    espradio_timer_done(ptimer);
}

/* Stub: reset WiFi MAC. Блоб вызывает по osi+244 между coex_wifi_request и coex_wifi_release;
 * выставляем g_wdev_last_desc_reset_ptr чтобы следующий за release *ptr=1 не портил память. */
static void espradio_wifi_reset_mac(void) {
    espradio_wdev_last_desc_reset_prepare();
    espradio_hal_reset_wifi_mac_go();
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_reset_mac\n");
#endif
}

static void espradio_wifi_clock_enable(void) {
    espradio_hal_init_clocks_go();
}

static void espradio_wifi_clock_disable(void) {
    espradio_hal_disable_clocks_go();
}

static void espradio_wifi_rtc_enable_iso(void) {
    espradio_hal_wifi_rtc_enable_iso_go();
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_rtc_enable_iso\n");
#endif
}

static void espradio_wifi_rtc_disable_iso(void) {
    espradio_hal_wifi_rtc_disable_iso_go();
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_rtc_disable_iso\n");
#endif
}

static int64_t espradio_esp_timer_get_time(void) {
    return (int64_t)espradio_time_us_now();
}

#define ESP_ERR_NVS_BASE        0x1100
#define ESP_ERR_NVS_NOT_FOUND   (ESP_ERR_NVS_BASE + 0x02)

static int espradio_nvs_set_i8(uint32_t handle, const char* key, int8_t value) {
    (void)handle;
    (void)key;
    (void)value;
    return 0;
}

static int espradio_nvs_get_i8(uint32_t handle, const char* key, int8_t* out_value) {
    (void)handle;
    (void)key;
    (void)out_value;
    return ESP_ERR_NVS_NOT_FOUND;
}

static int espradio_nvs_set_u8(uint32_t handle, const char* key, uint8_t value) {
    (void)handle;
    (void)key;
    (void)value;
    return 0;
}

static int espradio_nvs_get_u8(uint32_t handle, const char* key, uint8_t* out_value) {
    (void)handle;
    (void)key;
    (void)out_value;
    return ESP_ERR_NVS_NOT_FOUND;
}

static int espradio_nvs_set_u16(uint32_t handle, const char* key, uint16_t value) {
    (void)handle;
    (void)key;
    (void)value;
    return 0;
}

static int espradio_nvs_get_u16(uint32_t handle, const char* key, uint16_t* out_value) {
    (void)handle;
    (void)key;
    (void)out_value;
    return ESP_ERR_NVS_NOT_FOUND;
}

static int espradio_nvs_open(const char* name, unsigned int open_mode, uint32_t *out_handle) {
    (void)name;
    (void)open_mode;
    if (!out_handle) return -1;
    *out_handle = 1;
    return 0;
}

static void espradio_nvs_close(uint32_t handle) {
    (void)handle;
}

static int espradio_nvs_commit(uint32_t handle) {
    (void)handle;
    return 0;
}

static int espradio_nvs_set_blob(uint32_t handle, const char* key, const void* value, size_t length) {
    (void)handle;
    (void)key;
    (void)value;
    (void)length;
    return 0;
}

static int espradio_nvs_get_blob(uint32_t handle, const char* key, void* out_value, size_t* length) {
    (void)handle;
    (void)key;
    (void)out_value;
    (void)length;
    return ESP_ERR_NVS_NOT_FOUND;
}

static int espradio_nvs_erase_key(uint32_t handle, const char* key) {
    (void)handle;
    (void)key;
    return 0;
}

static int espradio_get_random(uint8_t *buf, size_t len) {
    if (!buf) {
        return -1;
    }
    size_t i = 0;
    while (i < len) {
        uint32_t r = espradio_rand();
        for (unsigned j = 0; j < 4 && i < len; j++, i++) {
            buf[i] = (uint8_t)(r >> (j * 8));
        }
    }
    return 0;
}

static int espradio_get_time(void *t) {
    if (!t) {
        return -1;
    }
    struct espradio_os_time {
        int32_t sec;
        int32_t usec;
    };
    uint64_t us = espradio_time_us_now();
    struct espradio_os_time *ot = (struct espradio_os_time *)t;
    ot->sec = (int32_t)(us / 1000000ULL);
    ot->usec = (int32_t)(us % 1000000ULL);
    return 0;
}

static unsigned long espradio_random(void) {
    return (unsigned long)espradio_rand();
}

static uint32_t espradio_slowclk_cal_get(void) {
    return 28639;
}

#define LOG_MSG_MAX 384

static void espradio_log_writev(unsigned int level, const char* tag, const char* format, va_list args) {
    static char buf[LOG_MSG_MAX];
    int n = vsnprintf(buf, sizeof(buf), format, args);
    if (n > 0) {
        if ((size_t)n >= sizeof(buf)) {
            buf[sizeof(buf)-1] = '\0';
        }
        if (tag && tag[0]) {
            printf("[wifi][%u][%s] %s\n", level, tag, buf);
        } else {
            printf("[wifi][%u] %s\n", level, buf);
        }
    }
}

static void espradio_log_write(unsigned int level, const char* tag, const char* format, ...) {
    va_list args;
    va_start(args, format);
    espradio_log_writev(level, tag, format, args);
    va_end(args);
}

uint32_t espradio_log_timestamp(void);

static void * espradio_malloc_internal(size_t size) {
    espradio_alloc_count++;
    return espradio_arena_alloc(size);
}

static void * espradio_realloc_internal(void *ptr, size_t size) {
    espradio_alloc_count++;
    return espradio_arena_realloc(ptr, size);
}

static void * espradio_calloc_internal(size_t n, size_t size) {
    espradio_alloc_count++;
    return espradio_arena_calloc(n, size);
}

static void * espradio_zalloc_internal(size_t size) {
    espradio_alloc_count++;
    return espradio_arena_calloc(1, size);
}

static void * espradio_wifi_malloc(size_t size) {
    *(volatile uint32_t *)G_OSI_FUNCS_P_ADDR = (uint32_t)&espradio_osi_funcs;
    espradio_alloc_count++;
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_malloc %zu stack_left=%lu\n", size, (unsigned long)espradio_stack_remaining());
#endif
    return espradio_arena_alloc(size);
}

static void * espradio_wifi_realloc(void *ptr, size_t size) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_realloc %p %zu\n", (void *)ptr, size);
#endif
    espradio_alloc_count++;
    return espradio_arena_realloc(ptr, size);
}

static void * espradio_wifi_calloc(size_t n, size_t size) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_calloc n=%zu size=%zu\n", n, size);
#endif
    espradio_alloc_count++;
    return espradio_arena_calloc(n, size);
}

static void * espradio_wifi_zalloc(size_t size) {
#if ESPRADIO_OSI_DEBUG
    printf("osi: wifi_zalloc %zu\n", size);
#endif
    espradio_alloc_count++;
    return espradio_arena_calloc(1, size);
}

void espradio_arena_stats(uint32_t *used, uint32_t *capacity);

void espradio_alloc_stats(unsigned *out_alloc, unsigned *out_free) {
    if (out_alloc) *out_alloc = espradio_alloc_count;
    if (out_free) *out_free = espradio_free_count;
    uint32_t used, cap;
    espradio_arena_stats(&used, &cap);
    printf("osi: arena %lu / %lu bytes\n", (unsigned long)used, (unsigned long)cap);
}

void *pvPortMalloc(size_t size) {
    espradio_alloc_count++;
    return espradio_arena_alloc(size);
}
void vPortFree(void *p) {
    if (p) espradio_free_count++;
    espradio_arena_free(p);
}

void * espradio_wifi_create_queue(int queue_len, int item_size);

void espradio_wifi_delete_queue(void * queue);

int espradio_coex_init(void);
void espradio_coex_deinit(void);
int espradio_coex_enable(void);
void espradio_coex_disable(void);
uint32_t espradio_coex_status_get(void);
void espradio_coex_condition_set(uint32_t type, bool dissatisfy);
int espradio_coex_wifi_request(uint32_t event, uint32_t latency, uint32_t duration);
int espradio_coex_wifi_release(uint32_t event);
int espradio_coex_wifi_channel_set(uint8_t primary, uint8_t secondary);
int espradio_coex_event_duration_get(uint32_t event, uint32_t *duration);
int espradio_coex_pti_get(uint32_t event, uint8_t *pti);
void espradio_coex_schm_status_bit_clear(uint32_t type, uint32_t status);
void espradio_coex_schm_status_bit_set(uint32_t type, uint32_t status);
int espradio_coex_schm_interval_set(uint32_t interval);
uint32_t espradio_coex_schm_interval_get(void);
uint8_t espradio_coex_schm_curr_period_get(void);
void *espradio_coex_schm_curr_phase_get(void);
int espradio_coex_schm_process_restart(void);
int espradio_coex_schm_register_cb(int type, int (*cb)(int));
int espradio_coex_register_start_cb(int (*cb)(void));
int espradio_coex_schm_flexible_period_set(uint8_t period);
uint8_t espradio_coex_schm_flexible_period_get(void);
void *espradio_coex_schm_get_phase_by_idx(int idx);

/* Coexistence adapter (esp_coexist_adapter.h) ********************************************/

/* Adapter wrappers with logging; most of them delegate to existing OSI functions. */
static void espradio_coex_adapter_task_yield_from_isr(void) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: task_yield_from_isr\n");
#endif
}

static void *espradio_coex_adapter_semphr_create(uint32_t max, uint32_t init) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_create max=%lu init=%lu\n",
           (unsigned long)max, (unsigned long)init);
#endif
    return espradio_semphr_create(max, init);
}

static void espradio_coex_adapter_semphr_delete(void *semphr) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_delete %p\n", semphr);
#endif
    espradio_semphr_delete(semphr);
}

static int32_t espradio_coex_adapter_semphr_take_from_isr(void *semphr, void *hptw) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_take_from_isr sem=%p hptw=%p\n", semphr, hptw);
#endif
    /* Treat as non-blocking take. */
    return espradio_semphr_take(semphr, 0);
}

static int32_t espradio_coex_adapter_semphr_give_from_isr(void *semphr, void *hptw) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_give_from_isr sem=%p hptw=%p\n", semphr, hptw);
#endif
    return espradio_semphr_give(semphr);
}

static int32_t espradio_coex_adapter_semphr_take(void *semphr, uint32_t block_time_tick) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_take sem=%p block=%lu\n",
           semphr, (unsigned long)block_time_tick);
#endif
    return espradio_semphr_take(semphr, block_time_tick);
}

static int32_t espradio_coex_adapter_semphr_give(void *semphr) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: semphr_give sem=%p\n", semphr);
#endif
    return espradio_semphr_give(semphr);
}

static int espradio_coex_adapter_is_in_isr(void) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: is_in_isr\n");
#endif
    return 0;
}

static void *espradio_coex_adapter_malloc_internal(size_t size) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: malloc_internal size=%lu\n", (unsigned long)size);
#endif
    return espradio_malloc_internal(size);
}

static void espradio_coex_adapter_free(void *p) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: free %p\n", p);
#endif
    espradio_free(p);
}

static int64_t espradio_coex_adapter_esp_timer_get_time(void) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: esp_timer_get_time\n");
#endif
    /* Simple stub: time base not critical for detecting usage. */
    return 0;
}

static bool espradio_coex_adapter_env_is_chip(void) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: env_is_chip\n");
#endif
    return espradio_env_is_chip();
}

static void espradio_coex_adapter_timer_disarm(void *timer) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: timer_disarm %p\n", timer);
#endif
    espradio_timer_disarm(timer);
}

static void espradio_coex_adapter_timer_done(void *ptimer) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: timer_done %p\n", ptimer);
#endif
    espradio_timer_done(ptimer);
}

static void espradio_coex_adapter_timer_setfn(void *ptimer, void *pfunction, void *parg) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: timer_setfn ptimer=%p fn=%p arg=%p\n",
           ptimer, pfunction, parg);
#endif
    espradio_timer_setfn(ptimer, pfunction, parg);
}

static void espradio_coex_adapter_timer_arm_us(void *ptimer, uint32_t us, bool repeat) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: timer_arm_us ptimer=%p us=%lu repeat=%d\n",
           ptimer, (unsigned long)us, (int)repeat);
#endif
    espradio_timer_arm_us(ptimer, us, repeat);
}

static int espradio_coex_adapter_debug_matrix_init(int event, int signal, bool rev) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: debug_matrix_init event=%d signal=%d rev=%d\n",
           event, signal, (int)rev);
#endif
    return 0;
}

static int espradio_coex_adapter_xtal_freq_get(void) {
#if ESPRADIO_OSI_DEBUG
    printf("coex_adapter: xtal_freq_get\n");
#endif
    return 40; /* Typical crystal frequency in MHz. */
}

coex_adapter_funcs_t g_coex_adapter_funcs = {
    ._version = COEX_ADAPTER_VERSION,
    ._task_yield_from_isr = espradio_coex_adapter_task_yield_from_isr,
    ._semphr_create = espradio_coex_adapter_semphr_create,
    ._semphr_delete = espradio_coex_adapter_semphr_delete,
    ._semphr_take_from_isr = espradio_coex_adapter_semphr_take_from_isr,
    ._semphr_give_from_isr = espradio_coex_adapter_semphr_give_from_isr,
    ._semphr_take = espradio_coex_adapter_semphr_take,
    ._semphr_give = espradio_coex_adapter_semphr_give,
    ._is_in_isr = espradio_coex_adapter_is_in_isr,
    ._malloc_internal = espradio_coex_adapter_malloc_internal,
    ._free = espradio_coex_adapter_free,
    ._esp_timer_get_time = espradio_coex_adapter_esp_timer_get_time,
    ._env_is_chip = espradio_coex_adapter_env_is_chip,
    ._timer_disarm = espradio_coex_adapter_timer_disarm,
    ._timer_done = espradio_coex_adapter_timer_done,
    ._timer_setfn = espradio_coex_adapter_timer_setfn,
    ._timer_arm_us = espradio_coex_adapter_timer_arm_us,
    ._debug_matrix_init = espradio_coex_adapter_debug_matrix_init,
    ._xtal_freq_get = espradio_coex_adapter_xtal_freq_get,
    ._magic = COEX_ADAPTER_MAGIC,
};

extern esp_err_t esp_coex_adapter_register(coex_adapter_funcs_t *funcs);

void espradio_coex_adapter_init(void) {
    esp_err_t r = esp_coex_adapter_register(&g_coex_adapter_funcs);
#if ESPRADIO_OSI_DEBUG
    printf("osi: esp_coex_adapter_register -> %ld\n", (long)r);
#endif
}


wifi_osi_funcs_t espradio_osi_funcs = {
    ._version = ESP_WIFI_OS_ADAPTER_VERSION,
    ._env_is_chip = espradio_env_is_chip,
    ._set_intr = espradio_set_intr,
    ._clear_intr = espradio_clear_intr,
    ._set_isr = espradio_set_isr,
    ._ints_on = espradio_ints_on,
    ._ints_off = espradio_ints_off,
    ._is_from_isr = espradio_is_from_isr,
    ._spin_lock_create = espradio_spin_lock_create,
    ._spin_lock_delete = espradio_spin_lock_delete,
    ._wifi_int_disable = espradio_wifi_int_disable,
    ._wifi_int_restore = espradio_wifi_int_restore,
    ._task_yield_from_isr = espradio_task_yield_from_isr,
    ._semphr_create = espradio_semphr_create,
    ._semphr_delete = espradio_semphr_delete,
    ._semphr_take = espradio_semphr_take,
    ._semphr_give = espradio_semphr_give,
    ._wifi_thread_semphr_get = espradio_wifi_thread_semphr_get,
    ._mutex_create = espradio_mutex_create,
    ._recursive_mutex_create = espradio_recursive_mutex_create,
    ._mutex_delete = espradio_mutex_delete,
    ._mutex_lock = espradio_mutex_lock,
    ._mutex_unlock = espradio_mutex_unlock,
    ._queue_create = espradio_queue_create,
    ._queue_delete = espradio_queue_delete,
    ._queue_send = espradio_queue_send,
    ._queue_send_from_isr = espradio_queue_send_from_isr,
    ._queue_send_to_back = espradio_queue_send_to_back,
    ._queue_send_to_front = espradio_queue_send_to_front,
    ._queue_recv = espradio_queue_recv,
    ._queue_msg_waiting = espradio_queue_msg_waiting,
    ._event_group_create = espradio_event_group_create,
    ._event_group_delete = espradio_event_group_delete,
    ._event_group_set_bits = espradio_event_group_set_bits,
    ._event_group_clear_bits = espradio_event_group_clear_bits,
    ._event_group_wait_bits = espradio_event_group_wait_bits,
    ._task_create_pinned_to_core = espradio_task_create_pinned_to_core_wrap,
    ._task_create = espradio_task_create,
    ._task_delete = espradio_task_delete,
    ._task_delay = espradio_task_delay,
    ._task_ms_to_tick = espradio_task_ms_to_tick,
    ._task_get_current_task = espradio_task_get_current_task,
    ._task_get_max_priority = espradio_task_get_max_priority,
    ._malloc = espradio_malloc,
    ._free = espradio_free,
    ._event_post = espradio_event_post,
    ._get_free_heap_size = espradio_get_free_heap_size,
    ._rand = espradio_rand,
    ._dport_access_stall_other_cpu_start_wrap = espradio_dport_access_stall_other_cpu_start_wrap,
    ._dport_access_stall_other_cpu_end_wrap = espradio_dport_access_stall_other_cpu_end_wrap,
    ._wifi_apb80m_request = espradio_wifi_apb80m_request,
    ._wifi_apb80m_release = espradio_wifi_apb80m_release,
    ._phy_disable = espradio_phy_disable,
    ._phy_enable = espradio_phy_enable,
    ._phy_update_country_info = espradio_phy_update_country_info,
    ._read_mac = espradio_read_mac,
    ._timer_arm = timer_arm_wrapper,
    ._timer_disarm = timer_disarm_wrapper,
    ._timer_done = timer_done_wrapper,
    ._timer_setfn = timer_setfn_wrapper,
    ._timer_arm_us = timer_arm_us_wrapper,
    ._wifi_reset_mac = espradio_wifi_reset_mac,
    ._wifi_clock_enable = espradio_wifi_clock_enable,
    ._wifi_clock_disable = espradio_wifi_clock_disable,
    ._wifi_rtc_enable_iso = espradio_wifi_rtc_enable_iso,
    ._wifi_rtc_disable_iso = espradio_wifi_rtc_disable_iso,
    ._esp_timer_get_time = espradio_esp_timer_get_time,
    ._nvs_set_i8 = espradio_nvs_set_i8,
    ._nvs_get_i8 = espradio_nvs_get_i8,
    ._nvs_set_u8 = espradio_nvs_set_u8,
    ._nvs_get_u8 = espradio_nvs_get_u8,
    ._nvs_set_u16 = espradio_nvs_set_u16,
    ._nvs_get_u16 = espradio_nvs_get_u16,
    ._nvs_open = espradio_nvs_open,
    ._nvs_close = espradio_nvs_close,
    ._nvs_commit = espradio_nvs_commit,
    ._nvs_set_blob = espradio_nvs_set_blob,
    ._nvs_get_blob = espradio_nvs_get_blob,
    ._nvs_erase_key = espradio_nvs_erase_key,
    ._get_random = espradio_get_random,
    ._get_time = espradio_get_time,
    ._random = espradio_random,
    ._slowclk_cal_get = espradio_slowclk_cal_get,
    ._log_write = espradio_log_write,
    ._log_writev = espradio_log_writev,
    ._log_timestamp = espradio_log_timestamp,
    ._malloc_internal = espradio_malloc_internal,
    ._realloc_internal = espradio_realloc_internal,
    ._calloc_internal = espradio_calloc_internal,
    ._zalloc_internal = espradio_zalloc_internal,
    ._wifi_malloc = espradio_wifi_malloc,
    ._wifi_realloc = espradio_wifi_realloc,
    ._wifi_calloc = espradio_wifi_calloc,
    ._wifi_zalloc = espradio_wifi_zalloc,
    ._wifi_create_queue = espradio_wifi_create_queue,
    ._wifi_delete_queue = espradio_wifi_delete_queue,
    ._coex_init = espradio_coex_init,
    ._coex_deinit = espradio_coex_deinit,
    ._coex_enable = espradio_coex_enable,
    ._coex_disable = espradio_coex_disable,
    ._coex_status_get = espradio_coex_status_get,
    ._coex_condition_set = espradio_coex_condition_set,
    ._coex_wifi_request = espradio_coex_wifi_request,
    ._coex_wifi_release = espradio_coex_wifi_release,
    ._coex_wifi_channel_set = espradio_coex_wifi_channel_set,
    ._coex_event_duration_get = espradio_coex_event_duration_get,
    ._coex_pti_get = espradio_coex_pti_get,
    ._coex_schm_status_bit_clear = espradio_coex_schm_status_bit_clear,
    ._coex_schm_status_bit_set = espradio_coex_schm_status_bit_set,
    ._coex_schm_interval_set = espradio_coex_schm_interval_set,
    ._coex_schm_interval_get = espradio_coex_schm_interval_get,
    ._coex_schm_curr_period_get = espradio_coex_schm_curr_period_get,
    ._coex_schm_curr_phase_get = espradio_coex_schm_curr_phase_get,
    ._coex_schm_process_restart = espradio_coex_schm_process_restart,
    ._coex_schm_register_cb = espradio_coex_schm_register_cb,
    ._coex_register_start_cb = espradio_coex_register_start_cb,
    ._coex_schm_flexible_period_set = espradio_coex_schm_flexible_period_set,
    ._coex_schm_flexible_period_get = espradio_coex_schm_flexible_period_get,
    ._coex_schm_get_phase_by_idx = espradio_coex_schm_get_phase_by_idx,
    ._magic = ESP_WIFI_OS_ADAPTER_MAGIC,
};
