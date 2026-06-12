//go:build esp32s3

#include <stdint.h>
#include "espradio.h"
#include "soc/interrupts.h"

/* ---- Xtensa interrupt controller (ESP32-S3) ---- */

/* On Xtensa the interrupt enable/disable is done via special registers
 * (INTENABLE) using RSR/WSR instructions.  The interrupt matrix routes
 * peripheral sources to CPU interrupt lines. */

/* The CPU interrupt number used for all WiFi peripheral sources.
 * On Xtensa, TinyGo's handleInterrupt() only dispatches lines 6-30.
 * Interrupt 12 is ExternLevel at level 1 (level-triggered, suitable for
 * WiFi MAC which holds its interrupt line high until acknowledged). */
#define ESPRADIO_WIFI_CPU_INT  12u

/* TinyGo routes ETS_GPIO_INTR_SOURCE (16) to this CPU interrupt (cpuInterruptFromPin
 * in machine_esp32s3.go).  Restored after every schedOnce() so that blob ROM
 * calls (e.g. direct intr_matrix_set) cannot permanently steal the GPIO source. */
#define ESPRADIO_GPIO_CPU_INT  10u

/* Direct volatile pointer to INTERRUPT_CORE0.GPIO_INTERRUPT_PRO_MAP (offset
 * 0x40).  Writing cpu_int routes ETS_GPIO_INTR_SOURCE to that CPU interrupt.
 * Writing 0 disconnects the GPIO source from all CPU interrupts.
 * Base 0x600C2000, source 16 → offset 16*4 = 0x40 */
#define ESPRADIO_GPIO_MAP_REG  (*(volatile uint32_t *)(0x600C2040u))

/* Pre-wire WiFi peripheral interrupt sources to the WiFi CPU interrupt.
 * Must be called before esp_wifi_init so routing is in place before the
 * blob enables the peripheral-side interrupts. */
void espradio_prewire_wifi_interrupts(void) {
    /* Map ALL WiFi-related peripheral sources to our CPU interrupt */
    intr_matrix_set(0, ETS_WIFI_MAC_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);     /* src 0 */
    intr_matrix_set(0, ETS_WIFI_MAC_NMI_SOURCE,  ESPRADIO_WIFI_CPU_INT);     /* src 1 - NMI */
    intr_matrix_set(0, ETS_WIFI_PWR_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);     /* src 2 */
    intr_matrix_set(0, ETS_WIFI_BB_INTR_SOURCE,  ESPRADIO_WIFI_CPU_INT);     /* src 3 */
}

extern void espradio_mark_wifi_isr_slot(int32_t n);

/* No-op: the blob calls set_intr to route peripheral sources to CPU interrupts,
 * but on Xtensa (ESP32-S3) the routing is already configured by
 * espradio_prewire_wifi_interrupts().  Letting the blob re-route arbitrary
 * sources via intr_matrix_set at arbitrary times is dangerous: if the blob
 * passes a source that TinyGo owns (e.g. ETS_GPIO_INTR_SOURCE = 16) we would
 * overwrite the GPIO → CPU-int-10 mapping that TinyGo set up, silently routing
 * GPIO events into wifiISRHandler and breaking all pin-change interrupts.
 * The Rust esp-wifi and the ESP32-C3 path in espradio both use the same no-op
 * strategy.  Record the blob's requested intr_num as a WiFi ISR slot so that
 * espradio_call_wifi_isr() still calls the correct blob handler. */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    (void)cpu_no;
    (void)intr_source;
    (void)intr_prio;
    espradio_mark_wifi_isr_slot((int32_t)intr_num);
}

/* No-op: same as set_intr. */
void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_source;
    (void)intr_num;
}

/* Enable/disable CPU interrupts using Xtensa INTENABLE special register.
 *
 * IMPORTANT: We deliberately ignore the blob's mask and only operate on
 * ESPRADIO_WIFI_CPU_INT (bit 12).  The blob passes its own original CPU
 * interrupt number (e.g. 0 or 1), which may coincide with CPU interrupts
 * that TinyGo has allocated for other purposes (bit 10 = GPIO, bit 9 =
 * timer alarm).  If we forwarded the blob's mask, espradio_ints_off would
 * clear those bits from INTENABLE, permanently disabling user interrupts
 * such as GPIO PinFalling callbacks (issue #40).
 *
 * The blob calls these for its own critical-section protection.  Since all
 * blob ISR handlers run in goroutine context (never from real ISR context),
 * the blob's critical sections are already serialised by TinyGo's
 * cooperative scheduler; ignoring the mask is safe. */
void espradio_ints_on(uint32_t mask) {
    (void)mask;
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    val |= (1u << ESPRADIO_WIFI_CPU_INT);
    __asm__ volatile ("wsr %0, intenable; rsync" :: "r"(val));
}

void espradio_ints_off(uint32_t mask) {
    (void)mask;
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    val &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    __asm__ volatile ("wsr %0, intenable; rsync" :: "r"(val));
}

/* On Xtensa, interrupt 1 is already level-triggered (type is determined
 * by hardware, not software-configurable like on RISC-V).
 * This function is a no-op for ESP32-S3. */
void espradio_wifi_int_to_level(void) {
    /* nothing to do — Xtensa interrupt types are fixed by hardware */
}

/* On Xtensa, interrupt priorities are fixed by hardware (interrupt 1 is
 * level 1).  TinyGo's interrupt.Enable() handles enabling it.
 * This is a no-op for ESP32-S3. */
void espradio_wifi_int_raise_priority(void) {
    /* nothing to do — Xtensa interrupt priorities are fixed */
}

/* INTENABLE snapshot taken at the start of schedOnce(), before any blob code
 * runs.  espradio_wifi_unmask() ORs this back into INTENABLE so that bits
 * cleared by blob ROM calls (e.g. ets_isr_mask) during processing are
 * restored — in particular bit 10 (GPIO) and bit 9 (timer alarm) which the
 * WiFi blob may clear when it uses those CPU interrupt numbers internally. */
static volatile uint32_t s_intenable_snapshot;

void espradio_snapshot_intenable(void) {
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    s_intenable_snapshot = val;
}

/* Lower PS.INTLEVEL to 0, allowing level-1 (GPIO, WiFi) interrupts to fire.
 *
 * The TinyGo GC's tinygo_scanCurrentStack does "rsil a4, 3" to flush the
 * Xtensa register windows.  If a cooperative goroutine yield occurs during
 * the recursive window-spill loop, the goroutine is suspended and later
 * resumed with PS.INTLEVEL=3 still active — permanently blocking all
 * level-1 interrupts (GPIO at CPU int 10, WiFi at CPU int 12) for that
 * goroutine until it voluntarily lowers PS.INTLEVEL again.
 *
 * We call this at the end of espradio_wifi_unmask() (and thus at the end
 * of every schedOnce() cycle) to ensure that after blob processing, the
 * schedTicker goroutine runs with PS.INTLEVEL=0. */
void espradio_lower_intlevel(void) {
    uint32_t ps;
    __asm__ volatile ("rsr %0, ps" : "=r"(ps));
    ps &= ~0x0Fu;               /* clear INTLEVEL bits [3:0] */
    __asm__ volatile ("wsr %0, ps; rsync" :: "r"(ps));
}

/* On Xtensa, level-triggered interrupts auto-clear when the peripheral
 * de-asserts.  We still mask/unmask to prevent re-entry while the
 * bottom-half runs. */
void espradio_wifi_isr_post_mask(void) {
    espradio_ints_off(1u << ESPRADIO_WIFI_CPU_INT);
}

void espradio_wifi_unmask(void) {
    /* Restore any TinyGo-owned INTENABLE bits that blob code may have cleared
     * (e.g. via ROM ets_isr_mask), then ensure the WiFi CPU interrupt is on. */
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    val |= s_intenable_snapshot | (1u << ESPRADIO_WIFI_CPU_INT);
    __asm__ volatile ("wsr %0, intenable; rsync" :: "r"(val));

    /* Re-route GPIO source → TinyGo's CPU interrupt in case blob ROM code
     * (direct intr_matrix_set calls inside the binary) corrupted it during
     * schedOnce() processing.  intr_matrix_set(cpu_no, source, cpu_int). */
    intr_matrix_set(0, ETS_GPIO_INTR_SOURCE, ESPRADIO_GPIO_CPU_INT);

    /* Force a new rising edge at CPU int 10 by briefly disconnecting the GPIO
     * source then reconnecting it.
     *
     * WHY: CPU interrupt 10 is edge-triggered.  On Xtensa LX7 the edge latch
     * only captures when INTENABLE[n]=1 at the moment the edge arrives.  If
     * ets_isr_mask cleared INTENABLE[10] while a button edge was in-flight,
     * the edge was missed: GPIO_STATUS is set (peripheral saw the edge) but
     * INTERRUPT[10] is 0 (CPU latch did not capture it).
     *
     * FIX: disconnect GPIO source (matrix output → LOW, edge 1→0 at int 10
     * input), read back the register to flush the write pipeline across the
     * APB bus, then reconnect (matrix output → HIGH, edge 0→1 captured by
     * the latch because INTENABLE[10]=1 now).  The readback + memw ensures
     * the LOW has propagated before we write HIGH — without it, back-to-back
     * writes coalesce in the write buffer and no real LOW pulse is generated.
     *
     * A spurious trigger when GPIO_STATUS=0 is harmless: the handler reads 0
     * and calls no callbacks. */
    if (s_intenable_snapshot & (1u << ESPRADIO_GPIO_CPU_INT)) {
        ESPRADIO_GPIO_MAP_REG = 0u;                      /* disconnect → int 10 input LOW  */
        (void)ESPRADIO_GPIO_MAP_REG;                     /* read back: flush write pipeline */
        __asm__ volatile ("memw" ::: "memory");          /* Xtensa memory-wait fence        */
        ESPRADIO_GPIO_MAP_REG = ESPRADIO_GPIO_CPU_INT;   /* reconnect → rising edge latched */
        __asm__ volatile ("memw" ::: "memory");
    }

    /* Ensure PS.INTLEVEL=0 so pending level-1 interrupts (GPIO, WiFi) can
     * actually be taken by the CPU.  The TinyGo GC's tinygo_scanCurrentStack
     * uses "rsil 3" and a goroutine yield during the window-spill loop can
     * leave the schedTicker goroutine permanently at INTLEVEL=3, silencing
     * all level-1 interrupts until explicitly lowered here. */
    espradio_lower_intlevel();
}
