//go:build esp32s3

/* ESP32-S3 chip layer for bt_ble.c. See bt_ble.h for the interface.
 * The interrupt delivery, the critical section and the addresses are different
 * from the ESP32-C3. The blob is the same. */

#include "espradio.h"
#include "bt_ble.h"
#include <stdint.h>
#include <stdio.h>

#ifndef ESPRADIO_BLE_DEBUG
#define ESPRADIO_BLE_DEBUG 0
#endif

#if ESPRADIO_BLE_DEBUG
#define BLE_DBG(...) printf(__VA_ARGS__)
#else
#define BLE_DBG(...) ((void)0)
#endif

extern uint64_t espradio_time_us_now(void);
extern uint32_t r_lld_read_clock(void);

/* Free level 1 lines that are also level triggered, which the BT peripheral
 * needs. See esp-idf components/esp_hw_support/port/esp32s3/cpu.c intr_desc_table. */
#define BT_CPU_INT_5  13u /* RWBT + BT_BB sources */
#define BT_CPU_INT_8  17u /* RWBLE source */

/* Critical section */

static uint32_t s_bt_int_nesting;
static uint32_t s_bt_int_saved_level;

/* Restore only the INTLEVEL field. The saved PS also holds EXCM, WOE, CALLINC
 * and OWB, and a stale EXCM makes the next retw an illegal instruction.
 * See Xtensa ISA Reference Manual section 4.7.4 for the PS fields. */
void espradio_bt_cs_enter(void) {
    uint32_t ps;
    __asm__ volatile ("rsil %0, 15" : "=r"(ps) :: "memory");
    if (s_bt_int_nesting == 0) {
        s_bt_int_saved_level = ps & 0x0Fu;
    }
    s_bt_int_nesting++;
}

void espradio_bt_cs_exit(void) {
    if (s_bt_int_nesting == 0) {
        return; /* The counts are not balanced. Do not enable too early. */
    }
    if (--s_bt_int_nesting == 0) {
        uint32_t ps;
        __asm__ volatile ("rsr %0, ps" : "=r"(ps));
        ps = (ps & ~0x0Fu) | s_bt_int_saved_level;
        __asm__ volatile ("wsr %0, ps; rsync" :: "r"(ps) : "memory");
    }
}

uint32_t espradio_bt_cs_depth(void) { return s_bt_int_nesting; }

/* Chip constants */

/* WDEV_RND_REG. See ESP32-S3 TRM v1.6 section 29.4 table 29-2. */
uint32_t espradio_bt_hw_rand(void) {
    return *(volatile uint32_t *)0x6003507Cu;
}

/* BLE_HW_TARGET_CODE_CHIP_ECO0 for the ESP32-S3. See esp-idf esp_bt.h. */
uint32_t espradio_bt_hw_target_code(void) { return 0x02010000u; }

/* TinyGo runs the ESP32-S3 at 240 MHz. */
uint32_t espradio_bt_cpu_ticks_per_us(void) { return 240u; }

/* Deferred interrupt servicing */

/* One writer in the interrupt context and one reader on the scheduler
 * goroutine. A lost race costs one more pass, thus volatile is sufficient. */
static volatile uint32_t s_isr_pending_5;
static volatile uint32_t s_isr_pending_8;

/* Diagnostic counts of the interrupts that occurred and that the tick ran. */
static volatile uint32_t s_isr_fired_5, s_isr_fired_8;
static volatile uint32_t s_isr_served_5, s_isr_served_8;

extern void espradio_bt_ints_off(uint32_t mask);
extern void espradio_bt_unmask(void);

/* The Go interrupt handlers in radio_esp32s3.go call these. They must not
 * call the blob, because they run on the goroutine that was interrupted. */
void espradio_bt_isr_latch_5(void) {
    s_isr_fired_5++;
    s_isr_pending_5 = 1;
    espradio_bt_ints_off(1u << BT_CPU_INT_5);
}

void espradio_bt_isr_latch_8(void) {
    s_isr_fired_8++;
    s_isr_pending_8 = 1;
    espradio_bt_ints_off(1u << BT_CPU_INT_8);
}

/* Run the latched ISRs on the scheduler goroutine stack. The unmask must be
 * explicit, because schedOnce() restores the INTENABLE value from the pass start. */
void espradio_bt_chip_service_isrs(void) {
    if (s_isr_pending_8) {
        s_isr_pending_8 = 0;
        if (espradio_bt_run_isr(8)) {
            s_isr_served_8++;
        }
    }
    if (s_isr_pending_5) {
        s_isr_pending_5 = 0;
        if (espradio_bt_run_isr(5)) {
            s_isr_served_5++;
        }
    }
    espradio_bt_unmask();
}

/* Diagnostics */

/* The ESP32-C3 probes use addresses that have no known ESP32-S3 equivalent.
 * Only the measurements that need no unknown address are given here. */

void espradio_bt_chip_debug_after_init(void) {
    BLE_DBG("  pwr_state=%lu bleclk=%lu\n",
            (unsigned long)btdm_pwr_state, (unsigned long)r_lld_read_clock());
}

void espradio_bt_chip_debug_tick(void) {
    static uint32_t s_tick_count;
    if (++s_tick_count % 400 != 0) { /* 2 s at the 5 ms tick */
        return;
    }

    /* Compare the OSI time with the BLE clock. One BLE tick is 312.5 us, thus
     * a correct 2 s interval gives approximately 6400 ticks. */
    static uint64_t p_us;
    static uint32_t p_blk;
    uint64_t us = espradio_time_us_now();
    uint32_t blk = r_lld_read_clock();
    if (p_us) {
        uint32_t d_us = (uint32_t)(us - p_us);
        uint32_t d_blk = blk - p_blk;
        BLE_DBG("  clocks: d_time_us=%lu d_bleclk=%lu (ble implies %luus)\n",
                (unsigned long)d_us, (unsigned long)d_blk,
                (unsigned long)(d_blk * 3125u / 10u));
    }
    p_us = us;
    p_blk = blk;

    /* The fired and served counts must increase together. If served stays
     * constant, the tick does not reach the blob. */
    BLE_DBG("  isr: fired 5=%lu 8=%lu  served 5=%lu 8=%lu  wake gives=%lu nosem=%lu\n",
            (unsigned long)s_isr_fired_5, (unsigned long)s_isr_fired_8,
            (unsigned long)s_isr_served_5, (unsigned long)s_isr_served_8,
            (unsigned long)espradio_bt_wake_gives(),
            (unsigned long)espradio_bt_wake_nosem());
}
