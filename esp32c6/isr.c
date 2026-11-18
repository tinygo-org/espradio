//go:build esp32c6

#include <stdint.h>
#include "espradio.h"
#include "soc/interrupts.h"

/* PLIC machine mode registers (DR_REG_PLIC_MX_BASE in soc/reg_base.h).
 * Same layout as TinyGo runtime/interrupt/interrupt_esp32c6.go. */
#define ESPRADIO_PLIC_BASE            0x20001000u
#define ESPRADIO_PLIC_ENABLE_REG      (*(volatile uint32_t *)(ESPRADIO_PLIC_BASE + 0x00u))
#define ESPRADIO_PLIC_TYPE_REG        (*(volatile uint32_t *)(ESPRADIO_PLIC_BASE + 0x04u))
#define ESPRADIO_PLIC_CLEAR_REG       (*(volatile uint32_t *)(ESPRADIO_PLIC_BASE + 0x08u))
#define ESPRADIO_PLIC_PRI_REG(n)      (*(volatile uint32_t *)(ESPRADIO_PLIC_BASE + 0x10u + (uint32_t)(n) * 4u))

#define ESPRADIO_WIFI_CPU_INT  1u

/* Matches cpuInterruptFromPin in TinyGo machine_esp32c6.go. */
#define ESPRADIO_GPIO_CPU_INT  6u

void espradio_prewire_wifi_interrupts(void) {
    intr_matrix_set(0, ETS_WIFI_MAC_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);
    intr_matrix_set(0, ETS_WIFI_PWR_INTR_SOURCE, ESPRADIO_WIFI_CPU_INT);
}

extern void espradio_mark_wifi_isr_slot(int32_t n);

/* Routing is done once in espradio_prewire_wifi_interrupts, as in esp-hal
 * esp-radio/src/wifi/os_adapter/esp32c6.rs. */
void espradio_set_intr(int32_t cpu_no, uint32_t intr_source, uint32_t intr_num, int32_t intr_prio) {
    (void)cpu_no;
    (void)intr_source;
    (void)intr_prio;
    espradio_mark_wifi_isr_slot((int32_t)intr_num);
}

void espradio_clear_intr(uint32_t intr_source, uint32_t intr_num) {
    (void)intr_source;
    (void)intr_num;
}

void espradio_ints_on(uint32_t mask) {
    ESPRADIO_PLIC_ENABLE_REG |= mask;
}

void espradio_ints_off(uint32_t mask) {
    ESPRADIO_PLIC_ENABLE_REG &= ~mask;
}

/* The MAC holds its line until the blob ISR acks it, so use level type as in
 * ESP-IDF components/esp_wifi/esp32c6/esp_adapter.c set_intr_wrapper. */
void espradio_wifi_int_to_level(void) {
    ESPRADIO_PLIC_ENABLE_REG &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_PLIC_CLEAR_REG  |=  (1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_PLIC_CLEAR_REG  &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    ESPRADIO_PLIC_TYPE_REG   &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    __asm__ volatile ("fence" ::: "memory");
    ESPRADIO_PLIC_ENABLE_REG |=  (1u << ESPRADIO_WIFI_CPU_INT);
}

/* The PLIC delivers when priority >= threshold. TinyGo uses threshold 5, so
 * 6 keeps WiFi above the default lines as on the C3. */
void espradio_wifi_int_raise_priority(void) {
    ESPRADIO_PLIC_PRI_REG(ESPRADIO_WIFI_CPU_INT) = 6u;
    __asm__ volatile ("fence" ::: "memory");
}

static volatile uint32_t s_intenable_snapshot;

void espradio_snapshot_intenable(void) {
    s_intenable_snapshot = ESPRADIO_PLIC_ENABLE_REG;
}

void espradio_set_unmask_interval_us(uint32_t us) { (void)us; }
uint32_t espradio_unmask_interval_us(void)        { return 0; }
uint32_t espradio_unmask_suppressed(void)         { return 0; }

void espradio_lower_intlevel(void) {
}

void espradio_wifi_isr_post_mask(void) {
    if ((ESPRADIO_PLIC_TYPE_REG & (1u << ESPRADIO_WIFI_CPU_INT)) == 0) {
        ESPRADIO_PLIC_ENABLE_REG &= ~(1u << ESPRADIO_WIFI_CPU_INT);
    }
}

void espradio_wifi_unmask(void) {
    ESPRADIO_PLIC_ENABLE_REG |= s_intenable_snapshot | (1u << ESPRADIO_WIFI_CPU_INT);

    intr_matrix_set(0, ETS_GPIO_INTR_SOURCE, ESPRADIO_GPIO_CPU_INT);

    if (s_intenable_snapshot & (1u << ESPRADIO_GPIO_CPU_INT)) {
        ESPRADIO_PLIC_ENABLE_REG &= ~(1u << ESPRADIO_GPIO_CPU_INT);
        __asm__ volatile ("fence" ::: "memory");
        ESPRADIO_PLIC_ENABLE_REG |=  (1u << ESPRADIO_GPIO_CPU_INT);
    }
}
