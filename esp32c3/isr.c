//go:build esp32c3

#include <stdint.h>
#include "espradio.h"

/* ---- Interrupt controller registers (ESP32-C3) ---- */

#define ESPRADIO_INTC_BASE            0x600C2000u
#define ESPRADIO_INTC_ENABLE_REG      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x104u))
#define ESPRADIO_INTC_TYPE_REG        (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x108u))
#define ESPRADIO_INTC_CLEAR_REG       (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x10Cu))
#define ESPRADIO_INTC_PRI_REG(n)      (*(volatile uint32_t *)(ESPRADIO_INTC_BASE + 0x114u + (uint32_t)(n) * 4u))

/* Route an interrupt source to a CPU-local interrupt number
 * using the ESP32-C3 interrupt matrix. Priority is currently
 * ignored and configured by ROM/IDF defaults.
 */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    (void)intr_prio;
    if (cpu_no < 0) {
        cpu_no = 0;
    }
    intr_matrix_set((uint32_t)cpu_no, intr_source, intr_num);
}

/* Disconnect an interrupt source from its CPU-local interrupt. */
void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_num;
    intr_matrix_set(0, intr_source, 0);
}

/* ROM-level ISR mask/unmask functions for ESP32-C3.
 * We keep these declarations and calls here so that the
 * generic ISR code only depends on espradio_ints_on/off
 * and does not need to know about chip-specific ROM APIs.
 */
extern void ets_isr_unmask(uint32_t mask);
extern void ets_isr_mask(uint32_t mask);

void espradio_ints_on(uint32_t mask) {
    ets_isr_unmask(mask);
}

void espradio_ints_off(uint32_t mask) {
    ets_isr_mask(mask);
}

