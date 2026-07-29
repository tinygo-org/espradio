//go:build esp32

#include <stdint.h>
#include "espradio.h"
#include "soc/interrupts.h"

/* ---- Xtensa interrupt controller (ESP32) ---- */

/* On Xtensa the interrupt enable/disable is done via special registers
 * (INTENABLE) using RSR/WSR instructions.  The interrupt matrix routes
 * peripheral sources to CPU interrupt lines via DPORT registers. */

/* The CPU interrupt number used for all WiFi peripheral sources.
 * On Xtensa, TinyGo's handleInterrupt() only dispatches lines 6-30.
 * Interrupt 12 is ExternLevel at level 1 (level-triggered, suitable for
 * WiFi MAC which holds its interrupt line high until acknowledged). */
#define ESPRADIO_WIFI_CPU_INT  12u

/* TinyGo routes ETS_GPIO_INTR_SOURCE (22) to this CPU interrupt (cpuInterruptFromPin
 * in machine_esp32.go).  Restored after every schedOnce() so that blob ROM
 * calls (e.g. direct intr_matrix_set) cannot permanently steal the GPIO source. */
#define ESPRADIO_GPIO_CPU_INT  10u

/* DPORT PRO_GPIO_INTERRUPT_MAP register address.
 * Base 0x3ff00000, offset 0x15C. Writing cpu_int routes ETS_GPIO_INTR_SOURCE
 * to that CPU interrupt. Writing 0 disconnects it. */
#define ESPRADIO_GPIO_MAP_REG  (*(volatile uint32_t *)(0x3ff0015cu))

/* Pre-wire WiFi peripheral interrupt sources to the WiFi CPU interrupt.
 * Must be called before esp_wifi_init so routing is in place before the
 * blob enables the peripheral-side interrupts.
 * On ESP32, intr_matrix_set is a ROM function at 0x4000681c. */
void espradio_prewire_wifi_interrupts(void) {
    /* Map all WiFi-related peripheral sources to our CPU interrupt */
    intr_matrix_set(0, ETS_WIFI_MAC_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);     /* src 0 */
    intr_matrix_set(0, ETS_WIFI_MAC_NMI_SOURCE,  ESPRADIO_WIFI_CPU_INT);     /* src 1 */
    intr_matrix_set(0, ETS_WIFI_BB_INTR_SOURCE,  ESPRADIO_WIFI_CPU_INT);     /* src 2 */
}

extern void espradio_mark_wifi_isr_slot(int32_t n);

/* No-op: the blob calls set_intr to route peripheral sources to CPU interrupts,
 * but on Xtensa (ESP32) the routing is already configured by
 * espradio_prewire_wifi_interrupts().  Record the blob's requested intr_num as
 * a WiFi ISR slot so that espradio_call_wifi_isr() still calls the correct
 * blob handler. */
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
 * We deliberately ignore the blob's mask and only operate on
 * ESPRADIO_WIFI_CPU_INT (bit 12).  See esp32s3/isr.c for rationale. */
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

/* On Xtensa, interrupt types are fixed by hardware. No-op. */
void espradio_wifi_int_to_level(void) {
}

/* On Xtensa, interrupt priorities are fixed by hardware. No-op. */
void espradio_wifi_int_raise_priority(void) {
}

/* INTENABLE snapshot taken at the start of schedOnce(), before any blob code
 * runs. */
static volatile uint32_t s_intenable_snapshot;

void espradio_snapshot_intenable(void) {
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    s_intenable_snapshot = val;
}

/* Lower PS.INTLEVEL to 0, allowing level-1 interrupts to fire.
 * See esp32s3/isr.c for detailed rationale. */
void espradio_lower_intlevel(void) {
    uint32_t ps;
    __asm__ volatile ("rsr %0, ps" : "=r"(ps));
    ps &= ~0x0Fu;               /* clear INTLEVEL bits [3:0] */
    __asm__ volatile ("wsr %0, ps; rsync" :: "r"(ps));
}

/* Mask WiFi interrupt after ISR fires (prevent re-entry). */
void espradio_wifi_isr_post_mask(void) {
    espradio_ints_off(1u << ESPRADIO_WIFI_CPU_INT);
}

/* Rate limit on re-enabling the WiFi CPU interrupt -- see the long note in
 * esp32s3/isr.c.  Same closed loop here, measured at 27,183 interrupts and one
 * scheduler pass each per second on an idle radio, and safe to limit for the same
 * reason: this handler only masks and wakes the scheduler, and schedOnce() calls
 * espradio_call_wifi_isr() itself on every pass, so the MAC is serviced whether or
 * not the interrupt fired.
 *
 * Zero disables the limit, for A/B on hardware. */
static volatile uint32_t s_unmask_interval_us = 1000;
static uint64_t          s_last_unmask_us;
static volatile uint32_t s_unmask_suppressed;

void espradio_set_unmask_interval_us(uint32_t us) { s_unmask_interval_us = us; }
uint32_t espradio_unmask_interval_us(void)        { return s_unmask_interval_us; }
uint32_t espradio_unmask_suppressed(void)         { return s_unmask_suppressed; }

void espradio_wifi_unmask(void) {
    /* Restore any TinyGo-owned INTENABLE bits that blob code may have cleared
     * (e.g. via ROM ets_isr_mask), then ensure the WiFi CPU interrupt is on. */
    uint32_t interval = s_unmask_interval_us;
    int allow_wifi = 1;
    if (interval) {
        uint64_t now = espradio_time_us_now();
        if (now - s_last_unmask_us < (uint64_t)interval) {
            allow_wifi = 0;
            s_unmask_suppressed++;
        } else {
            s_last_unmask_us = now;
        }
    }

    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    /* Exclude the WiFi bit from the snapshot restore: the snapshot predates
     * schedOnce masking it, so OR-ing it back would defeat the rate limit. */
    val |= s_intenable_snapshot & ~(1u << ESPRADIO_WIFI_CPU_INT);
    if (allow_wifi) {
        val |= (1u << ESPRADIO_WIFI_CPU_INT);
    }
    __asm__ volatile ("wsr %0, intenable; rsync" :: "r"(val));

    /* Re-route GPIO source → TinyGo's CPU interrupt in case blob ROM code
     * corrupted it during schedOnce() processing. */
    intr_matrix_set(0, ETS_GPIO_INTR_SOURCE, ESPRADIO_GPIO_CPU_INT);

    /* Force a new rising edge at CPU int 10 by briefly disconnecting the GPIO
     * source then reconnecting it. See esp32s3/isr.c for full explanation. */
    if (s_intenable_snapshot & (1u << ESPRADIO_GPIO_CPU_INT)) {
        ESPRADIO_GPIO_MAP_REG = 0u;                      /* disconnect → int 10 input LOW  */
        (void)ESPRADIO_GPIO_MAP_REG;                     /* read back: flush write pipeline */
        __asm__ volatile ("memw" ::: "memory");          /* Xtensa memory-wait fence        */
        ESPRADIO_GPIO_MAP_REG = ESPRADIO_GPIO_CPU_INT;   /* reconnect → rising edge latched */
        __asm__ volatile ("memw" ::: "memory");
    }

    /* Ensure PS.INTLEVEL=0 so pending level-1 interrupts can fire. */
    espradio_lower_intlevel();
}
