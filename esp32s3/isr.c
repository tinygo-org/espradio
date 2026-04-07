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

/* No-op: the blob calls set_intr to route peripheral sources to CPU
 * interrupts, but routing is already configured by
 * espradio_prewire_wifi_interrupts(). */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    intr_matrix_set(0, intr_source, ESPRADIO_WIFI_CPU_INT);
}

/* No-op: same as set_intr. */
void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_source;
    (void)intr_num;
}

/* Enable CPU interrupts using Xtensa INTENABLE special register.
 * The blob calls ints_on with its own mask (1<<0 for WiFi MAC).
 * We translate: if the blob's mask includes a WiFi-related bit,
 * also set our actual WiFi CPU interrupt bit. */
void espradio_ints_on(uint32_t mask) {
    uint32_t actual_mask = mask | (1u << ESPRADIO_WIFI_CPU_INT);
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    val |= actual_mask;
    __asm__ volatile ("wsr %0, intenable; rsync" :: "r"(val));
}

void espradio_ints_off(uint32_t mask) {
    uint32_t val;
    __asm__ volatile ("rsr %0, intenable" : "=r"(val));
    val &= ~mask;
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

/* On Xtensa, level-triggered interrupts auto-clear when the peripheral
 * de-asserts.  We still mask/unmask to prevent re-entry while the
 * bottom-half runs. */
void espradio_wifi_isr_post_mask(void) {
    espradio_ints_off(1u << ESPRADIO_WIFI_CPU_INT);
}

void espradio_wifi_unmask(void) {
    espradio_ints_on(1u << ESPRADIO_WIFI_CPU_INT);
}
