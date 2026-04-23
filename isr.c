#include <stdint.h>
#include <stdbool.h>
#include "sdkconfig.h"

/* ROM printf only — never libprintf.a (varargs broken from Clang). */
extern int ets_printf(const char *fmt, ...);

#ifdef __XTENSA__
#define ESPRADIO_MEMORY_BARRIER() __asm__ volatile ("memw" ::: "memory")
#else
#define ESPRADIO_MEMORY_BARRIER() __asm__ volatile ("fence" ::: "memory")
#endif

/* ---- User exception handler (Xtensa level-1 vector) ---- */
#ifdef __XTENSA__
static const char *exccause_name(uint32_t cause) {
    switch (cause) {
    case 0:  return "IllegalInstruction";
    case 2:  return "InstructionFetchError";
    case 3:  return "LoadStoreError";
    case 6:  return "IntegerDivideByZero";
    case 9:  return "LoadStoreAlignment";
    case 12: return "InstructionPIFDataError";
    case 13: return "LoadStorePIFDataError";
    case 14: return "InstructionPIFAddrError";
    case 15: return "LoadStorePIFAddrError";
    case 20: return "InstTLBMiss";
    case 24: return "LoadStoreTLBMiss";
    case 28: return "LoadProhibited";
    case 29: return "StoreProhibited";
    default: return "Unknown";
    }
}

void espradio_user_exception(uint32_t cause, uint32_t epc, uint32_t excvaddr, uint32_t *frame) {
    /* Write crash signature to RTC STORE registers (survive across reset).
     * ESP32 peripherals at 0x3FF4xxxx; ESP32-S3 at 0x6000xxxx. */
#if CONFIG_IDF_TARGET_ESP32
    volatile uint32_t *store0 = (volatile uint32_t *)0x3FF48050;
    volatile uint32_t *store1 = (volatile uint32_t *)0x3FF48054;
    volatile uint32_t *store2 = (volatile uint32_t *)0x3FF48058;
#else
    volatile uint32_t *store0 = (volatile uint32_t *)0x60008050;
    volatile uint32_t *store1 = (volatile uint32_t *)0x60008054;
    volatile uint32_t *store2 = (volatile uint32_t *)0x60008058;
#endif
    *store0 = 0x55570000 | (cause & 0xFFFF);  /* 0x5557 = user exception marker */
    *store1 = epc;
    *store2 = excvaddr;

    ets_printf("\n*** USER EXCEPTION ***\n");
    ets_printf("  EXCCAUSE = %d (%s)\n", (int)cause, exccause_name(cause));
    ets_printf("  EPC1     = 0x%x\n", (unsigned)epc);
    ets_printf("  EXCVADDR = 0x%x\n", (unsigned)excvaddr);
    /* Dump WindowBase and WindowStart to see register window state */
    uint32_t wb, ws;
    __asm__ volatile ("rsr %0, WINDOWBASE" : "=r"(wb));
    __asm__ volatile ("rsr %0, WINDOWSTART" : "=r"(ws));
    ets_printf("  WINDOWBASE = %d  WINDOWSTART = 0x%x\n", (int)wb, (unsigned)ws);
    /* Dump saved registers from the exception frame.
     * Layout:  0:a0  4:a1(orig)  8:a2  12:a3  16:a4  20:a5
     *         24:a6  28:a7  32:a8  36:a9  40:a10 44:a11
     *         48:a12 52:a13 56:a14 60:a15  64:SAR 68:EPC1 72:PS */
    if (frame) {
        ets_printf("  Saved registers:\n");
        static const char *rn[] = {"a0","a1","a2","a3","a4","a5","a6","a7",
                                   "a8","a9","a10","a11","a12","a13","a14","a15"};
        for (int i = 0; i < 16; i++) {
            ets_printf("    %s = 0x%x\n", rn[i], (unsigned)frame[i]);
        }
        ets_printf("    SAR = 0x%x  PS = 0x%x\n",
                   (unsigned)frame[16], (unsigned)frame[18]);
    }
    ets_printf("*** resetting ***\n");
    /* Trigger software system reset (preserves RTC STORE) */
#if CONFIG_IDF_TARGET_ESP32
    volatile uint32_t *options0 = (volatile uint32_t *)0x3FF48000;
#else
    volatile uint32_t *options0 = (volatile uint32_t *)0x60008000;
#endif
    *options0 |= (1u << 31);
    for (;;) {
        __asm__ volatile ("waiti 0");
    }
}
#endif

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

__attribute__((weak))
void espradio_wifi_isr_post_mask(void) {}

static volatile uint32_t s_wifi_isr_count;
void espradio_call_wifi_isr(void) {
    s_wifi_isr_count++;
    s_in_isr = 1;
    ESPRADIO_MEMORY_BARRIER();
    // CALL ALL ISRs from 0 to 31 just in case, to see if they are set!
    for (int i = 0; i < 32; i++) {
        if (s_isr_fn[i]) {
            s_isr_fn[i](s_isr_arg[i]);
        }
    }
    ESPRADIO_MEMORY_BARRIER();
    s_in_isr = 0;
    espradio_wifi_isr_post_mask();
}
uint32_t espradio_get_wifi_isr_count(void) { return s_wifi_isr_count; }

bool espradio_is_from_isr(void) {
    return s_in_isr != 0;
}

void espradio_task_yield_from_isr(void) {
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
    ESPRADIO_MEMORY_BARRIER();
    s_isr_ring_head = next;
    return 1;
}

uint32_t espradio_isr_ring_head(void)  { return s_isr_ring_head; }
uint32_t espradio_isr_ring_tail(void)  { return s_isr_ring_tail; }
void     espradio_isr_ring_advance_tail(void) {
    ESPRADIO_MEMORY_BARRIER();
    s_isr_ring_tail = (s_isr_ring_tail + 1u) % ESPRADIO_ISR_RING_SIZE;
}
void    *espradio_isr_ring_entry_queue(uint32_t idx) { return s_isr_ring_queue[idx]; }
void    *espradio_isr_ring_entry_item(uint32_t idx)  { return s_isr_ring_items[idx]; }
uint32_t espradio_isr_ring_drops(void) { return s_isr_ring_drops; }

