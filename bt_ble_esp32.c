//go:build esp32

/* Classic ESP32 chip layer for bt_ble.c. See bt_ble.h for the interface.
 * The ESP32 is Xtensa, thus the interrupt handling follows bt_ble_esp32s3.c
 * and not bt_ble_esp32c3.c. The addresses and the blob are different. */

#include "espradio.h"
#include "bt_ble.h"
#include "soc/interrupts.h"
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

/* Free level 1 lines that are also level triggered, which the BT peripheral
 * needs. Line 12 is already the WiFi line. Lines 9 and 18 are the spares.
 * See esp-idf components/esp_hw_support/port/esp32/esp_intr_alloc.c int_desc. */
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

/* WDEV_RND_REG. See ESP-IDF components/soc/esp32/register/soc/wdev_reg.h. */
uint32_t espradio_bt_hw_rand(void) {
    return *(volatile uint32_t *)0x3FF75144u;
}

/* The classic ESP32 config structure has no hw_target_code field. This value
 * is never read on this chip. */
uint32_t espradio_bt_hw_target_code(void) { return 0u; }

/* TinyGo runs the classic ESP32 at 240 MHz. */
uint32_t espradio_bt_cpu_ticks_per_us(void) { return 240u; }

/* Deferred interrupt servicing */

/* One writer in the interrupt context and one reader on the scheduler
 * goroutine. A lost race costs one more pass, thus volatile is sufficient. */
static volatile uint32_t s_isr_pending_5;
static volatile uint32_t s_isr_pending_8;

/* The blob asks for the software interrupt through the OSI table. There is no
 * hardware line behind it, so the tick runs the handler instead. */
static volatile uint32_t s_isr_pending_7;

void espradio_bt_sw_intr_raise(void) { s_isr_pending_7 = 1; }

/* Diagnostic counts of the interrupts that occurred and that the tick ran. */
static volatile uint32_t s_isr_fired_5, s_isr_fired_8;
static volatile uint32_t s_isr_served_5, s_isr_served_8, s_isr_served_7;

extern void espradio_bt_ints_off(uint32_t mask);
extern void espradio_bt_unmask(void);

/* The Go interrupt handlers in radio_esp32.go call these. They must not call
 * the blob, because they run on the goroutine that was interrupted and the
 * windowed call chains of the blob overflow that stack. */
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
    if (s_isr_pending_7) {
        s_isr_pending_7 = 0;
        if (espradio_bt_run_isr(7)) {
            s_isr_served_7++;
        }
    }
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

/* BR/EDR symbols that are not in the ESP32 ROM.
 *
 * The blob holds ROM patch trampolines such as lc_lmp_rsp_to_ind_handler_hack
 * that refer to these. ESP-IDF esp32.rom.ld does not give an address for them.
 * BLE only never dispatches BR/EDR link control, ACL or page scan, so the
 * bodies stay empty. Remove these if BR/EDR support is added. */
void lc_lmp_rsp_to_ind_handler(void) {}
void ld_acl_afh_apply(void) {}
void ld_acl_afh_switch_off_cbk(void) {}
void ld_acl_afh_switch_on_cbk(void) {}
void ld_page_em_init(void) {}
void ld_page_frm_cbk(void) {}
void ld_sco_resched_cbk(void) {}

/* Diagnostics */

/* PRO CPU interrupt matrix map registers. Source N is at base + 4*N.
 * See ESP-IDF components/soc/esp32/register/soc/dport_reg.h,
 * DPORT_PRO_MAC_INTR_MAP_REG. */
#define DPORT_PRO_INTR_MAP(n)  (*(volatile uint32_t *)(0x3FF00104u + 4u * (n)))

void espradio_bt_chip_debug_after_init(void) {
    uint32_t intenable;
    __asm__ volatile ("rsr %0, intenable" : "=r"(intenable));
    BLE_DBG("  pwr_state=%lu intenable=0x%08lx\n",
            (unsigned long)btdm_pwr_state, (unsigned long)intenable);
    /* Sources 5 BT_BB, 7 RWBT, 8 RWBLE must point at the two CPU lines. */
    BLE_DBG("  intmap: bt_bb=%lu rwbt=%lu rwble=%lu\n",
            (unsigned long)DPORT_PRO_INTR_MAP(ETS_BT_BB_INTR_SOURCE),
            (unsigned long)DPORT_PRO_INTR_MAP(ETS_RWBT_INTR_SOURCE),
            (unsigned long)DPORT_PRO_INTR_MAP(ETS_RWBLE_INTR_SOURCE));
}

void espradio_bt_chip_debug_tick(void) {
    static uint32_t s_tick_count;
    if (++s_tick_count % 400 != 0) { /* 2 s at the 5 ms tick */
        return;
    }

    /* The fired and served counts must increase together. If served stays
     * constant, the tick does not reach the blob. */
    BLE_DBG("  isr: fired 5=%lu 8=%lu  served 5=%lu 7=%lu 8=%lu  wake gives=%lu nosem=%lu\n",
            (unsigned long)s_isr_fired_5, (unsigned long)s_isr_fired_8,
            (unsigned long)s_isr_served_5, (unsigned long)s_isr_served_7,
            (unsigned long)s_isr_served_8,
            (unsigned long)espradio_bt_wake_gives(),
            (unsigned long)espradio_bt_wake_nosem());
}
