#include <stdint.h>
#include <stdbool.h>

/* ---- ISR fn/arg storage ---- */

static void (*s_isr_fn[32])(void *);
static void *s_isr_arg[32];

void espradio_set_isr(int32_t n, void *f, void *arg) {
    if (n >= 0 && n < 32) {
        s_isr_fn[n] = (void (*)(void *))f;
        s_isr_arg[n] = arg;
    }
}

/* ---- ISR context flag ---- */

static volatile uint32_t s_in_isr = 0;

void espradio_call_saved_isr(int32_t n) {
    s_in_isr = 1;
    __asm__ volatile ("fence" ::: "memory");
    if (n >= 0 && n < 32 && s_isr_fn[n]) {
        s_isr_fn[n](s_isr_arg[n]);
    }
    __asm__ volatile ("fence" ::: "memory");
    s_in_isr = 0;
}

bool espradio_is_from_isr(void) {
    return s_in_isr != 0;
}

void espradio_task_yield_from_isr(void) {
    /* no-op: unsafe to call Go scheduler from ISR context */
}

/* ---- ISR ring buffer ---- */

#define ESPRADIO_ISR_RING_SIZE 64
#define ESPRADIO_ISR_ITEM_SIZE 8

static volatile uint32_t s_isr_ring_head;
static volatile uint32_t s_isr_ring_tail;
static volatile uint32_t s_isr_ring_drops;
static void             *s_isr_ring_queue[ESPRADIO_ISR_RING_SIZE];
static uint8_t           s_isr_ring_items[ESPRADIO_ISR_RING_SIZE][ESPRADIO_ISR_ITEM_SIZE];

int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw) {
    if (hptw) {
        *(uint32_t *)hptw = 1;
    }
    uint32_t head = s_isr_ring_head;
    uint32_t next = (head + 1u) % ESPRADIO_ISR_RING_SIZE;
    if (next == s_isr_ring_tail) {
        s_isr_ring_drops++;
        return 0;
    }
    s_isr_ring_queue[head] = queue;
    if (item) {
        __builtin_memcpy(s_isr_ring_items[head], item, ESPRADIO_ISR_ITEM_SIZE);
    } else {
        __builtin_memset(s_isr_ring_items[head], 0, ESPRADIO_ISR_ITEM_SIZE);
    }
    __asm__ volatile ("fence" ::: "memory");
    s_isr_ring_head = next;
    return 1;
}

uint32_t espradio_isr_ring_head(void)  { return s_isr_ring_head; }
uint32_t espradio_isr_ring_tail(void)  { return s_isr_ring_tail; }
void     espradio_isr_ring_advance_tail(void) {
    __asm__ volatile ("fence" ::: "memory");
    s_isr_ring_tail = (s_isr_ring_tail + 1u) % ESPRADIO_ISR_RING_SIZE;
}
void    *espradio_isr_ring_entry_queue(uint32_t idx) { return s_isr_ring_queue[idx]; }
void    *espradio_isr_ring_entry_item(uint32_t idx)  { return s_isr_ring_items[idx]; }
uint32_t espradio_isr_ring_drops(void) { return s_isr_ring_drops; }
