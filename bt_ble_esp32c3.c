//go:build esp32c3

/* ESP32-C3 chip layer for bt_ble.c. See bt_ble.h for the interface.
 * Most of this file is diagnostics that compile to nothing when
 * ESPRADIO_BLE_DEBUG is 0. */

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

/* Chip hooks */

/* Critical-section state for espradio_bt_cs_enter/exit. */
static uint32_t s_bt_int_nesting;
static uint32_t s_bt_int_saved_mie;

/* Latches for the BLE core event-reporting watch in espradio_bt_chip_debug_tick. */
static volatile uint32_t s_fifo_seen_count;
static volatile uint32_t s_stat_seen_count;
static volatile uint32_t s_stat_seen_bits;

/* Save and restore mstatus.MIE and count the nesting. Only the innermost exit
 * enables the interrupts again. */
void espradio_bt_cs_enter(void) {
    uint32_t mstatus;
    /* Atomically clear mstatus.MIE (bit 3) and return the previous value. */
    __asm__ volatile ("csrrci %0, mstatus, 8" : "=r"(mstatus) :: "memory");
    if (s_bt_int_nesting == 0) {
        s_bt_int_saved_mie = mstatus & 0x8u;
    }
    s_bt_int_nesting++;
}

void espradio_bt_cs_exit(void) {
    if (s_bt_int_nesting == 0) {
        return; /* The counts are not balanced. Do not enable too early. */
    }
    if (--s_bt_int_nesting == 0 && s_bt_int_saved_mie) {
        __asm__ volatile ("csrsi mstatus, 8" ::: "memory");
    }
}

uint32_t espradio_bt_cs_depth(void) { return s_bt_int_nesting; }

uint32_t espradio_bt_hw_rand(void) {
    return *(volatile uint32_t *)0x600260B0u; /* RNG_DATA_REG */
}

uint32_t espradio_bt_hw_target_code(void) { return 0x01010000u; } /* ESP32-C3 */

/* TinyGo runs the C3 at 160 MHz. */
uint32_t espradio_bt_cpu_ticks_per_us(void) { return 160u; }

/* This chip runs the blob ISRs in the hardware handler, thus nothing is left. */
void espradio_bt_chip_service_isrs(void) {}

#define BT_CPU_INT_PRI_REG_5 (*(volatile uint32_t *)(0x600C2000u + 0x114u + 28u * 4u))
#define BT_CPU_INT_PRI_REG_8 (*(volatile uint32_t *)(0x600C2000u + 0x114u + 30u * 4u))

/* Called from Go ISR handler for CPU interrupt assigned to RWBT+BT_BB */
void espradio_bt_isr_dispatch_5(void) {
    espradio_bt_run_isr(5);
    /* Set the priority again. The TinyGo trap handler restores threshold 5. */
    BT_CPU_INT_PRI_REG_5 = 7u;
}

static volatile uint32_t s_bt_isr8_count;

/* Trace ring. A printf in the interrupt context changes the measured timing. */
#define BT_ISR_TRACE_N 16
static volatile uint32_t s_isr_trace_stat[BT_ISR_TRACE_N];
static volatile uint32_t s_isr_trace_clk[BT_ISR_TRACE_N];
static volatile uint32_t s_isr_trace_head;

/* Called from Go ISR handler for CPU interrupt assigned to RWBLE */
void espradio_bt_isr_dispatch_8(void) {
    uint32_t n = s_bt_isr8_count++;
    if (n < BT_ISR_TRACE_N) {
        /* Raw BLE core interrupt status, sampled before the blob ISR acks it. */
        s_isr_trace_stat[n] = *(volatile uint32_t *)0x60031010u;
        s_isr_trace_clk[n]  = *(volatile uint32_t *)0x60031060u;
        s_isr_trace_head = n + 1;
    }
    espradio_bt_run_isr(8);
    /* Set the priority again. */
    BT_CPU_INT_PRI_REG_8 = 7u;
}

void espradio_bt_dump_probe_hits(void);

/* Drain the ISR trace into the log. Called from the Go diagnostic goroutine. */
void espradio_bt_dump_isr_trace(void) {
    static uint32_t printed;
    while (printed < s_isr_trace_head) {
        BLE_DBG("  isr8[%lu] stat=0x%08lx dat60=0x%08lx\n", (unsigned long)printed,
                (unsigned long)s_isr_trace_stat[printed],
                (unsigned long)s_isr_trace_clk[printed]);
        printed++;
    }
    espradio_bt_dump_probe_hits();
}

/* schedOnce() calls this to run the BLE scheduler when no interrupt occurs. */
void espradio_call_bt_isr(void) {
    espradio_bt_run_isr(8);
    espradio_bt_run_isr(5);
}

uint32_t espradio_bt_isr8_count(void) { return s_bt_isr8_count; }

/* Diagnostic read of CPU_INT_ENABLE. */
uint32_t espradio_bt_intc_enable(void) {
    return *(volatile uint32_t *)(0x600C2000u + 0x104u); /* CPU_INT_ENABLE */
}
/* Probe on ip_funcs[0x6b0], the arbiter insert that r_lld_scan_start calls.
 * It can return 0 on paths that do not link the element. */
#define BLE_REG(off) (*(volatile uint32_t *)(0x60031000u + (off)))

typedef char (*sch_arb_insert_fn)(void *elt);
static sch_arb_insert_fn s_orig_sch_arb_insert;
static volatile uint32_t s_arb_insert_calls;
static volatile int32_t  s_arb_insert_last_ret = -1;

extern uint32_t r_lld_read_clock(void);

static char bt_sch_arb_insert_probe(void *elt) {
    uint32_t *e = (uint32_t *)elt;
    uint32_t clk_before = r_lld_read_clock();
    uint32_t ts = e ? e[1] : 0;
    uint32_t off = e ? e[2] : 0;
    uint32_t dur = e ? e[4] : 0;

    char ret = s_orig_sch_arb_insert ? s_orig_sch_arb_insert(elt) : (char)0xFF;

    s_arb_insert_calls++;
    s_arb_insert_last_ret = (int32_t)(unsigned char)ret;
    BLE_DBG("ARB_INS #%lu elt=%p ts=%lu off=%lu dur=%lu clk=%lu ret=%d head=%p tgt=%lu cntl=0x%08lx\n",
            (unsigned long)s_arb_insert_calls, elt, (unsigned long)ts, (unsigned long)off,
            (unsigned long)dur, (unsigned long)clk_before, (int)ret,
            (void *)*(volatile uint32_t *)0x3fcdfbacu,
            (unsigned long)BLE_REG(0xec), (unsigned long)BLE_REG(0x0c));
    return ret;
}

/* Probes that only increment a counter, because they run in the interrupt
 * context. The callee ignores the extra arguments of the trampoline. */
typedef uint32_t (*bt_probe_fn)(uint32_t, uint32_t, uint32_t, uint32_t);

enum {
    PROBE_SCAN_EVT_START = 0, /* ip[0x3ec] r_lld_scan_evt_start_cbk_eco */
    PROBE_PROG_BLE_PUSH,      /* ip[0x7a8] r_sch_prog_ble_push_hack    */
    PROBE_PROG_END_ISR,       /* ip[0x6c0] r_sch_prog_end_isr_hack     */
    PROBE_SCAN_FRM_EOF,       /* ip[0x3f4] r_lld_scan_frm_eof_isr_eco  */
    PROBE_SCAN_FRM_RX,        /* ip[0x408] r_lld_scan_process_pkt_rx_hack */
    PROBE_ADV_REP,            /* ip[0x424] r_lld_scan_process_pkt_rx_adv_rep_hack */
    PROBE_COUNT
};

static const uint16_t s_probe_off[PROBE_COUNT] = {
    0x3ec, 0x7a8, 0x6c0, 0x3f4, 0x408, 0x424,
};
static const char *const s_probe_name[PROBE_COUNT] = {
    "scan_evt_start", "prog_ble_push", "prog_end_isr",
    "scan_frm_eof", "scan_pkt_rx", "adv_rep",
};

static bt_probe_fn s_probe_orig[PROBE_COUNT];
static volatile uint32_t s_probe_hits[PROBE_COUNT];

#define BT_DEFINE_PROBE(idx)                                                   \
    static uint32_t bt_probe_##idx(uint32_t a, uint32_t b, uint32_t c,         \
                                   uint32_t d) {                               \
        s_probe_hits[idx]++;                                                   \
        if (s_probe_orig[idx] == NULL) return 0;                               \
        return s_probe_orig[idx](a, b, c, d);                                  \
    }

BT_DEFINE_PROBE(0)
BT_DEFINE_PROBE(1)
BT_DEFINE_PROBE(2)
BT_DEFINE_PROBE(3)
BT_DEFINE_PROBE(4)
BT_DEFINE_PROBE(5)

static bt_probe_fn const s_probe_tramp[PROBE_COUNT] = {
    bt_probe_0, bt_probe_1, bt_probe_2, bt_probe_3, bt_probe_4, bt_probe_5,
};

/* Called from Go after espradio_ble_init() completes. */
void espradio_bt_install_probes(void) {
    uint32_t ip = *(volatile uint32_t *)0x3fcdff8cu; /* r_ip_funcs_p */
    if (ip == 0) {
        BLE_DBG("probes: r_ip_funcs_p is NULL\n");
        return;
    }
    volatile uint32_t *slot = (volatile uint32_t *)(ip + 0x6b0);
    if (s_orig_sch_arb_insert == NULL) {
        s_orig_sch_arb_insert = (sch_arb_insert_fn)(uintptr_t)*slot;
        *slot = (uint32_t)(uintptr_t)&bt_sch_arb_insert_probe;
        BLE_DBG("probes: sch_arb_insert %p -> %p\n",
                (void *)s_orig_sch_arb_insert, (void *)&bt_sch_arb_insert_probe);
    }
    for (int i = 0; i < PROBE_COUNT; i++) {
        if (s_probe_orig[i] != NULL) continue;
        volatile uint32_t *p = (volatile uint32_t *)(ip + s_probe_off[i]);
        s_probe_orig[i] = (bt_probe_fn)(uintptr_t)*p;
        *p = (uint32_t)(uintptr_t)s_probe_tramp[i];
        BLE_DBG("probes: ip[0x%03x] %-15s %p\n", s_probe_off[i], s_probe_name[i],
                (void *)s_probe_orig[i]);
    }
}

/* Do the BT RF phase match calibration again after the controller starts.
 * A pass here shows that btdm_controller_enable ran the calibration too early. */
extern void r_cali_phase_match_p(void);

void espradio_bt_recali_phase_match(void) {
    /* Measure the ROM delay. It uses a cached ticks per microsecond value that
     * is wrong if nothing called ets_update_cpu_frequency. */
    extern void ets_delay_us(uint32_t us);
    /* Compare the OSI time, the mcycle counter at 160 ticks per microsecond
     * and the BLE clock at 312.5 us per tick. */
    uint64_t t0 = espradio_time_us_now();
    uint32_t b0 = r_lld_read_clock();
    ets_delay_us(100000); /* 100ms == 320 BLE ticks, big enough to be unambiguous */
    uint32_t b1 = r_lld_read_clock();
    uint64_t t1 = espradio_time_us_now();
    BLE_DBG("  ets_delay_us(100000): time_us=%lu bleclk=%lu (expect ~100000 / ~320)\n",
            (unsigned long)(uint32_t)(t1 - t0), (unsigned long)(b1 - b0));

    uint32_t before = BLE_REG(0xf8);
    r_cali_phase_match_p();
    uint32_t after = BLE_REG(0xf8);
    BLE_DBG("  recali: 0xf8 %08lx -> %08lx  locked=%d (hi=%lu lo=%lu)\n",
            (unsigned long)before, (unsigned long)after,
            (after & 0x1000u) ? 1 : 0,
            (unsigned long)((after >> 8) & 0x3u),
            (unsigned long)((after >> 4) & 0x3u));
}

void espradio_bt_dump_probe_hits(void) {
    /* Compare the OSI time with the BLE clock across the 2 s interval.
     * A correct pair is approximately 2000000 us and 6400 ticks. */
    {
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
    }

    BLE_DBG("  wake: sem_gives=%lu nosem=%lu\n",
            (unsigned long)espradio_bt_wake_gives(),
            (unsigned long)espradio_bt_wake_nosem());
    BLE_DBG("  probes:");
    for (int i = 0; i < PROBE_COUNT; i++) {
        BLE_DBG(" %s=%lu", s_probe_name[i], (unsigned long)s_probe_hits[i]);
    }
    BLE_DBG("\n");

    /* A leaked critical section keeps mstatus.MIE at 0 and stops all
     * interrupts. A count that is not 0, or MIE at 0, shows this fault. */
    uint32_t mstatus;
    __asm__ volatile ("csrr %0, mstatus" : "=r"(mstatus));
    /* The interrupt occurs only when its priority is more than CPU_INT_THRESH.
     * See ESP32-C3 TRM v1.3 section 1.5.2. */
#define INTC_REG(off) (*(volatile uint32_t *)(0x600C2000u + (off)))
    /* Ask the controller itself whether it considers the radio active, rather
     * than inferring it from the raw btdm_pwr_state word. */
    extern bool btdm_power_state_active(void);
    extern int  btdm_get_power_state(void);
    BLE_DBG("  pwr: active=%d state=%d rf0xf8=0x%08lx bb0x1050=0x%08lx bb0x1868=0x%08lx\n",
            (int)btdm_power_state_active(), (int)btdm_get_power_state(),
            (unsigned long)BLE_REG(0xf8),
            (unsigned long)*(volatile uint32_t *)0x60011050u,
            (unsigned long)*(volatile uint32_t *)0x60011868u);

    BLE_DBG("  cs: mie=%lu nest=%lu saved=%lu | rwblecntl=0x%08lx dat2d8=0x%08lx dat100=0x%08lx\n"
            "  intc: thresh=%lu pri28=%lu pri30=%lu eip=0x%08lx en=0x%08lx type=0x%08lx\n",
            (unsigned long)((mstatus >> 3) & 1u),
            (unsigned long)s_bt_int_nesting, (unsigned long)s_bt_int_saved_mie,
            (unsigned long)BLE_REG(0x00), (unsigned long)BLE_REG(0x2d8),
            (unsigned long)BLE_REG(0x100),
            (unsigned long)INTC_REG(0x194),
            (unsigned long)INTC_REG(0x114 + 28 * 4),
            (unsigned long)INTC_REG(0x114 + 30 * 4),
            (unsigned long)INTC_REG(0x110),
            (unsigned long)INTC_REG(0x104),
            (unsigned long)INTC_REG(0x108));
}

uint32_t espradio_bt_intc_pending(void) {
    uint32_t rwble_map = *(volatile uint32_t *)(0x600C2000u + 8u * 4u);
    uint32_t scan_env = *(volatile uint32_t *)0x3fcdffacu; /* lld_scan_env pointer */
    uint32_t pwr_state = *(volatile uint32_t *)0x3fcdff18u; /* btdm_pwr_state */

    /* The first two words of lld_scan_env are the activity structs for the 1M
     * PHY and the coded PHY. A value that is not 0 shows a scheduled scan. */
    uint32_t evt0 = 0, evt1 = 0;
    if (scan_env) {
        evt0 = *(volatile uint32_t *)(scan_env + 0);
        evt1 = *(volatile uint32_t *)(scan_env + 4);
    }

    /* The PHY mask is at byte 2 of the parameter block of the activity slot.
     * The slot is 0x44 bytes and slot[0x40] holds the state. */
    uint32_t phy_mask = 0xFF, act_state = 0xFF;
    uint32_t llm_env = *(volatile uint32_t *)0x3fcdff98u;
    if (llm_env) {
        uint32_t act_table = *(volatile uint32_t *)(llm_env + 8);
        if (act_table) {
            for (int i = 0; i < 6; i++) {
                uint32_t slot = act_table + (uint32_t)i * 0x44u;
                uint8_t st = *(volatile uint8_t *)(slot + 0x40);
                if (st == 6) {
                    uint32_t param = *(volatile uint32_t *)slot;
                    act_state = st;
                    phy_mask = param ? *(volatile uint8_t *)(param + 2) : 0xFEu;
                    break;
                }
            }
        }
    }

    /* BLE core registers. +0x0c enables the interrupts, +0x10 gives the status,
     * +0xec holds the CLKN target and +0xf0 the half microsecond target. */
    BLE_DBG("  diag: rwble->cpu%lu scan_env=%p evt=[%p,%p] phy=0x%02lx st=%lu pwr=%lu\n"
            "        cntl=0x%08lx stat=0x%08lx tgt=%lu fine=%lu clk=%lu arb_ins=%lu/ret=%ld\n"
            "        seen: fifo=%lu statN=%lu statBits=0x%08lx\n",
            (unsigned long)rwble_map, (void *)scan_env, (void *)evt0, (void *)evt1,
            (unsigned long)phy_mask, (unsigned long)act_state, (unsigned long)pwr_state,
            (unsigned long)BLE_REG(0x0c), (unsigned long)BLE_REG(0x10),
            (unsigned long)BLE_REG(0xec), (unsigned long)BLE_REG(0xf0),
            (unsigned long)r_lld_read_clock(),
            (unsigned long)s_arb_insert_calls, (long)s_arb_insert_last_ret,
            (unsigned long)s_fifo_seen_count, (unsigned long)s_stat_seen_count,
            (unsigned long)s_stat_seen_bits);
    return (rwble_map << 24) | (evt0 != 0 ? 0x200 : 0) | (scan_env != 0 ? 0x100 : 0) |
           (pwr_state & 0xFF);
}

/* Chip diagnostics */

void espradio_bt_chip_debug_after_init(void) {
    BLE_DBG("  intcntl=0x%08lx\n", (unsigned long)BLE_REG(0x0c));
}

void espradio_bt_chip_debug_tick(void) {
    /* r_lld_core_init selects the FIFO mode, thus the event status comes from
     * +0x2d8 and not from the status register at +0x10. */
    uint32_t fifo = *(volatile uint32_t *)0x600312d8u;
    uint32_t stat = *(volatile uint32_t *)0x60031010u;
    if (((fifo >> 5) & 0x1fu) != 0) s_fifo_seen_count++;
    if (stat != 0) {
        s_stat_seen_count++;
        s_stat_seen_bits |= stat;
    }

    /* Debug: check function pointer tables + sch_arb */
    static uint32_t s_tick_count;
    if (++s_tick_count % 400 == 0) { /* every 2s */
        uint32_t arb_first = *(volatile uint32_t *)0x3fcdfbacu;
        uint32_t ke_evt = *(volatile uint32_t *)0x3fcdfd90u;
        extern uint32_t r_lld_read_clock(void);
        uint32_t clk = r_lld_read_clock();
        /* Check critical function pointer table entries */
        uint32_t plf_p = *(volatile uint32_t *)0x3fcdff80u; /* r_plf_funcs_p */
        uint32_t ip_p = *(volatile uint32_t *)0x3fcdff8cu;  /* r_ip_funcs_p */
        uint32_t emi_fn = plf_p ? *(volatile uint32_t *)(plf_p + 0xbc) : 0; /* plf[47] = emi_get_mem */
        uint32_t arb_ins = ip_p ? *(volatile uint32_t *)(ip_p + 0x6b0) : 0; /* ip[0x1ac] = sch_arb_insert */
        BLE_DBG("sched: arb=%p clk=%lu emi=%p arb_ins=%p\n",
                (void *)arb_first, (unsigned long)clk,
                (void *)emi_fn, (void *)arb_ins);
    }
}
