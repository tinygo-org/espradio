#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>

extern void intr_matrix_set(uint32_t cpu_no, uint32_t model_num, uint32_t intr_num);

#ifndef ESPRADIO_ISR_DEBUG
#define ESPRADIO_ISR_DEBUG 0
#endif

/* ---- ISR fn/arg storage ---- */

static void (*s_isr_fn[32])(void *);
static void *s_isr_arg[32];

void espradio_set_isr(int32_t n, void *f, void *arg) {
    if (n >= 0 && n < 32) {
        s_isr_fn[n] = (void (*)(void *))f;
        s_isr_arg[n] = arg;
    }
#if ESPRADIO_ISR_DEBUG
    printf("osi: set_isr n=%ld f=%p arg=%p\n", (long)n, f, arg);
#endif
}

/* ---- ISR context flag ---- */

static volatile uint32_t s_in_isr = 0;

void espradio_call_saved_isr(int32_t n) {
    s_in_isr = 1;
    __asm__ volatile ("fence" ::: "memory");
#if ESPRADIO_ISR_DEBUG    
    printf("ISR: n=%ld fn=%p arg=%p\n", (long)n,
           (n >= 0 && n < 32) ? (void *)s_isr_fn[n] : NULL,
           (n >= 0 && n < 32) ? s_isr_arg[n] : NULL);
#endif
    if (n >= 0 && n < 32 && s_isr_fn[n]) {
        s_isr_fn[n](s_isr_arg[n]);
    }
#if ESPRADIO_ISR_DEBUG     
    printf("ISR: done n=%ld\n", (long)n);
#endif
    __asm__ volatile ("fence" ::: "memory");
    s_in_isr = 0;
}

bool espradio_is_from_isr(void) {
    return s_in_isr != 0;
}

/* ---- Interrupt controller registers (ESP32-C3) ---- */

#define ESPRADIO_INTC_BASE            0x600C2000u
#define ESPRADIO_INTC_ENABLE_REG      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x104u))
#define ESPRADIO_INTC_TYPE_REG        (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x108u))
#define ESPRADIO_INTC_CLEAR_REG       (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x10Cu))
#define ESPRADIO_INTC_PRI_REG(n)      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x114u + (uint32_t)(n) * 4u))

void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    (void)intr_prio;
    if (cpu_no < 0) {
        cpu_no = 0;
    }
    intr_matrix_set((uint32_t)cpu_no, intr_source, intr_num);
#if ESPRADIO_ISR_DEBUG
    printf("osi: set_intr cpu=%ld src=%lu num=%lu prio=%ld\n",
           (long)cpu_no, (unsigned long)intr_source, (unsigned long)intr_num, (long)intr_prio);
#endif
}

void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_num;
    intr_matrix_set(0, intr_source, 0);
#if ESPRADIO_ISR_DEBUG
    printf("osi: clear_intr src=%lu num=%lu\n",
           (unsigned long)intr_source, (unsigned long)intr_num);
#endif
}

extern void ets_isr_unmask(uint32_t mask);
extern void ets_isr_mask(uint32_t mask);

void espradio_ints_on(uint32_t mask) {
    ets_isr_unmask(mask);
#if ESPRADIO_ISR_DEBUG
    printf("osi: ints_on mask=0x%08lx\n", (unsigned long)mask);
#endif
}

void espradio_ints_off(uint32_t mask) {
    ets_isr_mask(mask);
#if ESPRADIO_ISR_DEBUG
    printf("osi: ints_off mask=0x%08lx\n", (unsigned long)mask);
#endif
}

void espradio_task_yield_from_isr(void) {
    /* no-op: unsafe to call Go scheduler from ISR context */
}

/* ---- ISR ring buffer ---- */

#define ESPRADIO_ISR_RING_SIZE 16
#define ESPRADIO_ISR_ITEM_SIZE 8

static volatile uint32_t s_isr_ring_head;
static volatile uint32_t s_isr_ring_tail;
static void             *s_isr_ring_queue[ESPRADIO_ISR_RING_SIZE];
static uint8_t           s_isr_ring_items[ESPRADIO_ISR_RING_SIZE][ESPRADIO_ISR_ITEM_SIZE];

int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw) {
    if (hptw) {
        *(uint32_t *)hptw = 1;
    }
    uint32_t head = s_isr_ring_head;
    uint32_t next = (head + 1u) % ESPRADIO_ISR_RING_SIZE;
    if (next == s_isr_ring_tail) {
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
