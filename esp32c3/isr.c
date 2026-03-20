//go:build esp32c3

#include <stdint.h>
#include "espradio.h"
#include "soc/interrupts.h"

/* ---- Interrupt controller registers (ESP32-C3) ---- */

#define ESPRADIO_INTC_BASE            0x600C2000u
#define ESPRADIO_INTC_ENABLE_REG      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x104u))
#define ESPRADIO_INTC_TYPE_REG        (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x108u))
#define ESPRADIO_INTC_CLEAR_REG       (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x10Cu))
#define ESPRADIO_INTC_PRI_REG(n)      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x114u + (uint32_t)(n) * 4u))

/* Interrupt matrix base: each 32-bit register at offset 4*source routes that
 * peripheral source to a CPU interrupt number (0 = disabled, 1-31 = active). */
#define ESPRADIO_INTMTX_BASE          0x600C2000u
#define ESPRADIO_INTMTX_MAP(source)   (*(volatile uint32_t *)(ESPRADIO_INTMTX_BASE + (uint32_t)(source) * 4u))

/* The CPU interrupt number used for all WiFi peripheral sources.
 * TinyGo registers its handler on this interrupt via interrupt.New(). */
#define ESPRADIO_WIFI_CPU_INT  1u

/* Pre-wire WiFi peripheral interrupt sources to the WiFi CPU interrupt.
 * Must be called before esp_wifi_init so routing is in place before the
 * blob enables the peripheral-side interrupts.
 * Matches the approach in esp-wifi (Rust): routing is done once during
 * our controlled init, and the blob's set_intr calls are no-ops. */
void espradio_prewire_wifi_interrupts(void) {
    intr_matrix_set(0, ETS_WIFI_MAC_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);
    intr_matrix_set(0, ETS_WIFI_MAC_NMI_SOURCE,  ESPRADIO_WIFI_CPU_INT);
    intr_matrix_set(0, ETS_WIFI_PWR_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);
    intr_matrix_set(0, ETS_WIFI_BB_INTR_SOURCE,  ESPRADIO_WIFI_CPU_INT);
}

/* No-op: the blob calls set_intr to route peripheral sources to CPU
 * interrupts, but on RISC-V (ESP32-C3) the routing is already configured
 * by espradio_prewire_wifi_interrupts(). Letting the blob call
 * intr_matrix_set at arbitrary times interferes with TinyGo's interrupt
 * controller state.  The Rust esp-wifi does the same (no-op set_intr). */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    (void)cpu_no;
    (void)intr_source;
    (void)intr_num;
    (void)intr_prio;
}

/* No-op: the Rust esp-wifi also no-ops clear_intr. */
void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_source;
    (void)intr_num;
}

/* Enable/disable CPU interrupts by directly manipulating the
 * CPU_INT_ENABLE register instead of calling ROM functions
 * (ets_isr_unmask / ets_isr_mask) which may have side effects
 * that conflict with TinyGo's interrupt controller setup. */
void espradio_ints_on(uint32_t mask) {
    ESPRADIO_INTC_ENABLE_REG |= mask;
}

void espradio_ints_off(uint32_t mask) {
    ESPRADIO_INTC_ENABLE_REG &= ~mask;
}

/* Switch CPU interrupt 1 from edge to level type.
 * Must be called AFTER esp_wifi_init() so the blob's ISR handlers are
 * registered and can acknowledge the peripheral when a level interrupt
 * fires.  Without a handler to service the peripheral, a level-asserted
 * line would cause infinite re-entry.
 *
 * Sequence: disable → clear latched edge → switch to level → fence → re-enable.
 */
void espradio_wifi_int_to_level(void) {
    ESPRADIO_INTC_ENABLE_REG &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_INTC_CLEAR_REG  |=  (1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_INTC_CLEAR_REG  &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_INTC_TYPE_REG   &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    __asm__ volatile ("fence" ::: "memory");
    ESPRADIO_INTC_ENABLE_REG |=  (1u << ESPRADIO_WIFI_CPU_INT);
}

