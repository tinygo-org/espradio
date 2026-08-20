#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>

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
    /* Write crash signature to RTC STORE registers (survive across reset) */
    volatile uint32_t *store0 = (volatile uint32_t *)0x60008050;
    volatile uint32_t *store1 = (volatile uint32_t *)0x60008054;
    volatile uint32_t *store2 = (volatile uint32_t *)0x60008058;
    *store0 = 0x55570000 | (cause & 0xFFFF);  /* 0x5557 = user exception marker */
    *store1 = epc;
    *store2 = excvaddr;

    printf("\n*** USER EXCEPTION ***\n");
    printf("  EXCCAUSE = %lu (%s)\n", (unsigned long)cause, exccause_name(cause));
    printf("  EPC1     = 0x%08lx\n", (unsigned long)epc);
    printf("  EXCVADDR = 0x%08lx\n", (unsigned long)excvaddr);
    /* The vector captures these at the time of the fault. A read here gives
     * the values for this function. */
    if (frame) {
        printf("  WINDOWBASE = %lu  WINDOWSTART = 0x%04lx  (at fault)\n",
               (unsigned long)frame[19], (unsigned long)frame[20]);
    }
    /* Dump saved registers from the exception frame.
     * Layout:  0:a0  4:a1(orig)  8:a2  12:a3  16:a4  20:a5
     *         24:a6  28:a7  32:a8  36:a9  40:a10 44:a11
     *         48:a12 52:a13 56:a14 60:a15  64:SAR 68:EPC1 72:PS */
    if (frame) {
        printf("  Saved registers:\n");
        static const char *rn[] = {"a0","a1","a2","a3","a4","a5","a6","a7",
                                   "a8","a9","a10","a11","a12","a13","a14","a15"};
        for (int i = 0; i < 16; i++) {
            printf("    %-3s = 0x%08lx\n", rn[i], (unsigned long)frame[i]);
        }
        printf("    SAR = 0x%08lx  PS = 0x%08lx\n",
               (unsigned long)frame[16], (unsigned long)frame[18]);
    }
    fflush(stdout);
    printf("*** resetting ***\n");
    fflush(stdout);
    /* Trigger software system reset (preserves RTC STORE) */
    volatile uint32_t *options0 = (volatile uint32_t *)0x60008000;
    *options0 |= (1u << 31);
    for (;;) {
        __asm__ volatile ("waiti 0");
    }
}
#endif

/* ---- ISR fn/arg storage ---- */

/* On ESP32, place WiFi-only tables in DRAM1 (.wifibss) to free SRAM2 for
 * the Go GC heap.  On other targets they stay in normal .bss. */
#if CONFIG_IDF_TARGET_ESP32
#define WIFIBSS __attribute__((section(".wifibss")))
#else
#define WIFIBSS
#endif

static void (*s_isr_fn[32])(void *) WIFIBSS;
static void *s_isr_arg[32] WIFIBSS;

/* Zero the handler tables before anything can register into them.
 *
 * On ESP32 these live in .wifibss, a custom DRAM1 section, and the runtime zeroes
 * .bss only -- so they start as whatever was in that RAM.  Measured: the installed
 * bitmask read 0xffffffff on ESP32 against 1 on the S3, i.e. all 32 entries looked
 * like valid handlers.  That makes the "if (s_isr_fn[i])" guard in
 * espradio_call_wifi_isr() useless there: any slot marked in s_wifi_isr_slots
 * without a matching espradio_set_isr would be called through a garbage pointer.
 *
 * Nothing hits that today because only slot 0 is ever marked and it is always
 * written first, so this is a latch on a trap rather than a fix for a live fault. */
void espradio_isr_tables_init(void) {
    for (int i = 0; i < 32; i++) {
        s_isr_fn[i] = NULL;
        s_isr_arg[i] = NULL;
    }
}

/* Bitmask of ISR slots registered via espradio_set_intr (WiFi sources only). */
static uint32_t s_wifi_isr_slots;

void espradio_mark_wifi_isr_slot(int32_t n) {
    if (n >= 0 && n < 32) {
        s_wifi_isr_slots |= (1u << n);
    }
}

/* Which slots the blob registered.  The prewiring points several peripheral
 * sources at one CPU interrupt line, so if the blob registers fewer handlers than
 * there are routed sources, the unhandled ones can assert with nothing to ack
 * them -- and the line is level-triggered. */
uint32_t espradio_wifi_isr_slots(void) { return s_wifi_isr_slots; }

/* Blob ISR handler invocations, summed across slots.  Compared against the
 * scheduler pass count this says whether the handlers are running at all. */
static volatile uint32_t s_wifi_isr_handler_calls;

uint32_t espradio_wifi_isr_handler_calls(void) { return s_wifi_isr_handler_calls; }

/* Which slots actually hold a handler.
 *
 * This is deliberately a different question from espradio_wifi_isr_slots():
 * handlers arrive via espradio_set_isr (the blob's ets_isr_attach), which writes
 * s_isr_fn[] and nothing else, whereas the slots mask is written only by
 * espradio_set_intr.  espradio_call_wifi_isr() iterates the slots mask, so a
 * handler installed at a slot the mask does not cover is never called -- and
 * whatever source it belongs to is therefore never acked. */
uint32_t espradio_wifi_isr_installed(void) {
    uint32_t mask = 0;
    for (int i = 0; i < 32; i++) {
        if (s_isr_fn[i]) mask |= (1u << i);
    }
    return mask;
}

void espradio_set_isr(int32_t n, void *f, void *arg) {
    if (n >= 0 && n < 32) {
        s_isr_fn[n] = (void (*)(void *))f;
        s_isr_arg[n] = arg;
    }
}

/* ---- ISR context flags ---- */

/* s_in_isr means "the blob's ISR body is running", which is what the blob's own
 * _is_from_isr() is asking about: whether to use its from-ISR queue APIs.  It is
 * NOT the same question as "am I in hardware interrupt context", because on
 * ESP32-S3 and ESP32 the blob ISR is invoked from the scheduler goroutine, not
 * from a trap handler.
 *
 * s_in_hw_isr answers that second question, and only the per-target Go interrupt
 * handlers set it.  Anything that must not yield -- a spin that would otherwise
 * call runtime.Gosched() -- has to test this one; testing s_in_isr would refuse
 * to yield on S3/ESP32 in ordinary goroutine context and deadlock instead. */
static volatile uint32_t s_in_isr = 0;
static volatile uint32_t s_in_hw_isr = 0;

void espradio_enter_hw_isr(void) { s_in_hw_isr++; }
void espradio_exit_hw_isr(void)  { if (s_in_hw_isr) s_in_hw_isr--; }
bool espradio_in_hw_isr(void)    { return s_in_hw_isr != 0; }

__attribute__((weak))
void espradio_wifi_isr_post_mask(void) {}

static volatile uint32_t s_wifi_isr_count;
void espradio_call_wifi_isr(void) {
    s_wifi_isr_count++;
    s_in_isr = 1;
    ESPRADIO_MEMORY_BARRIER();
    /* Only call ISR slots that were registered via espradio_set_intr for a
     * WiFi peripheral source.  Calling all 32 slots risks invoking blob
     * handlers at slot numbers that coincide with TinyGo's GPIO or timer
     * CPU interrupts, which can corrupt INTENABLE. */
    uint32_t slots = s_wifi_isr_slots;
    while (slots) {
        int i = __builtin_ctz(slots);
        slots &= slots - 1;
        if (s_isr_fn[i]) {
            s_wifi_isr_handler_calls++;
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
static void             *s_isr_ring_queue[ESPRADIO_ISR_RING_SIZE] WIFIBSS;
static uint8_t           s_isr_ring_items[ESPRADIO_ISR_RING_SIZE][ESPRADIO_ISR_ITEM_SIZE] WIFIBSS;

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

