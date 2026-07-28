//go:build esp32c3

#include "espradio.h"
#include "esp_coexist_internal.h"
#include "esp32c3/esp_bt.h"
#include "btbb.h"
#include <stdint.h>
#include <string.h>
#include <stdio.h>

#ifndef ESPRADIO_BLE_DEBUG
#define ESPRADIO_BLE_DEBUG 0
#endif

#if ESPRADIO_BLE_DEBUG
#define BLE_DBG(...) printf(__VA_ARGS__)
#else
#define BLE_DBG(...) ((void)0)
#endif

/* ═══════════════════════════════════════════════════════════════════════════
 * BT Interrupt Handling
 * ═══════════════════════════════════════════════════════════════════════════ */

/* The blob calls interrupt_handler_set with interrupt_no=5 (RWBT+BT_BB) and
 * interrupt_no=8 (RWBLE). We store the handler pointers and dispatch from Go
 * interrupt handlers on the actual CPU interrupts. */

typedef void (*bt_isr_fn_t)(void *arg);

/* Critical-section state for bt_interrupt_disable/restore (defined further down;
 * declared here so the diagnostics can report on it). */
static uint32_t s_bt_int_nesting;
static uint32_t s_bt_int_saved_mie;

/* Latches for the BLE core event-reporting watch in espradio_bt_sched_tick. */
static volatile uint32_t s_fifo_seen_count;
static volatile uint32_t s_stat_seen_count;
static volatile uint32_t s_stat_seen_bits;

static bt_isr_fn_t s_bt_isr_fn_5;
static void       *s_bt_isr_arg_5;
static bt_isr_fn_t s_bt_isr_fn_8;
static void       *s_bt_isr_arg_8;

#define BT_CPU_INT_PRI_REG_5 (*(volatile uint32_t *)(0x600C2000u + 0x114u + 28u * 4u))
#define BT_CPU_INT_PRI_REG_8 (*(volatile uint32_t *)(0x600C2000u + 0x114u + 30u * 4u))

/* Wake the BT controller task.
 *
 * btdm_controller_task does NOT block on g_rw_schd_queue — it blocks on a
 * semaphore and only drains the queue non-blocking:
 *
 *     while (1) {
 *         if (semphr_take(btdm_ol_task_env->sem, portMAX_DELAY) == 0) break;
 *         while (queue_recv(g_rw_schd_queue, &msg, 0) == 1) { ...; btdm_rw_run(st); }
 *         if (no messages) btdm_rw_run(st);
 *     }
 *
 * So pushing an item into g_rw_schd_queue (what this used to do) never wakes
 * the task at all: it blocks on the semaphore forever and the queue item is
 * simply never looked at.  Giving the semaphore is what releases it, and even
 * with an empty queue that still reaches btdm_rw_run(s_btdm_state) -> rw_schedule(),
 * which is the call that reprograms the scheduler after an event.
 *
 * btdm_ol_task_env->sem lives at offset 8; use the blob's own accessor rather
 * than hardcoding the address. */
extern void *r_btdm_vnd_ol_task_env_get(void);
extern int32_t espradio_semphr_give(void *semphr);

static volatile uint32_t s_task_wake_count;
static volatile uint32_t s_task_wake_nosem;

static void bt_isr_wake_task(void) {
    void *env = r_btdm_vnd_ol_task_env_get();
    if (env == NULL) {
        s_task_wake_nosem++;
        return;
    }
    void *sem = *(void *volatile *)((uint8_t *)env + 8);
    if (sem == NULL) {
        s_task_wake_nosem++;
        return;
    }
    espradio_semphr_give(sem);
    s_task_wake_count++;
}

/* Rate-limited heartbeat wake for the periodic tick.
 *
 * Waking the task makes its goroutine runnable, which feeds back into
 * kickSched() and collapses the 5 ms tick into a ~30 us busy loop. The ISR path
 * above stays unthrottled (a real event must be serviced immediately); this is
 * only for the periodic keep-alive, capped at ~1 kHz. */
extern uint64_t espradio_time_us_now(void);

static void bt_wake_task_throttled(void) {
    static uint64_t s_last_wake_us;
    uint64_t now = espradio_time_us_now();
    if (now - s_last_wake_us < 1000) {
        return;
    }
    s_last_wake_us = now;
    bt_isr_wake_task();
}

/* Called from Go ISR handler for CPU interrupt assigned to RWBT+BT_BB */
void espradio_bt_isr_dispatch_5(void) {
    if (s_bt_isr_fn_5) {
        s_bt_isr_fn_5(s_bt_isr_arg_5);
    }
    bt_isr_wake_task();
    /* Re-raise priority — TinyGo's trap handler restores to defaultThreshold (5)
     * which equals CPU_INT_THRESH, preventing future interrupts. */
    BT_CPU_INT_PRI_REG_5 = 7u;
}

static volatile uint32_t s_bt_isr8_count;

/* ISR trace ring — printf from interrupt context would perturb the very timing
 * we're measuring, so record and let the diagnostic goroutine drain it. */
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
    if (s_bt_isr_fn_8) {
        s_bt_isr_fn_8(s_bt_isr_arg_8);
    }
    bt_isr_wake_task();
    /* Re-raise priority — same reason as above */
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

/* Called from schedOnce() to poll BLE ISR (like WiFi's espradio_call_wifi_isr).
 * Drives the BLE scheduler forward when hardware interrupts don't fire. */
void espradio_call_bt_isr(void) {
    if (s_bt_isr_fn_8) {
        s_bt_isr_fn_8(s_bt_isr_arg_8);
    }
    if (s_bt_isr_fn_5) {
        s_bt_isr_fn_5(s_bt_isr_arg_5);
    }
}

uint32_t espradio_bt_isr8_count(void) { return s_bt_isr8_count; }

/* INTC diagnostic — check if BLE CPU interrupts are enabled and pending */
uint32_t espradio_bt_intc_enable(void) {
    return *(volatile uint32_t *)(0x600C2000u + 0x104u); /* CPU_INT_ENABLE */
}
/* ─── sch_arb_insert probe ───
 * ip_funcs[0x6b0] is the scheduler-arbiter insert that r_lld_scan_start calls to
 * schedule the scan activity.  It can return 0 (success) on paths that don't
 * actually link the element, so wrap the table entry to capture the real return
 * value and the timing inputs.  Same mechanism the blob's own *_funcs_reset()
 * uses, so it is safe to install after controller_enable. */
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

/* ─── Counter probes on the event-start / radio-programming path ───
 * These run in ISR context, so they only bump a counter — printf here would
 * change the timing being measured.  Drained by espradio_bt_dump_isr_trace().
 * Generic 4-arg trampoline: on RISC-V forwarding a0..a3 is safe even for
 * callees that take fewer arguments, since they simply ignore the extra regs. */
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

/* Re-run the BT RF phase-match calibration after the controller is fully up.
 *
 * r_cali_phase_match_p sweeps hi/lo (0..3, 0..3) in BLE +0xf8 bits 9:8 / 5:4,
 * asserting bit 0 to start each attempt, and waits for bit 12 to signal lock.
 * During btdm_controller_enable it exhausts all 16 and prints "phase match cali
 * failed!".  If it locks when re-run here, the calibration itself is fine and the
 * power-up sequence simply runs it too early; if it still fails, the RF/PLL it
 * calibrates against is not coming up at all. */
extern void r_cali_phase_match_p(void);

void espradio_bt_recali_phase_match(void) {
    /* The cali loop polls bit 12 with only ets_delay_us(1) of settling between
     * attempts. ROM ets_delay_us busy-waits on the cycle counter scaled by the
     * ROM's cached ticks-per-us; if TinyGo changed the CPU clock without calling
     * ets_update_cpu_frequency, that delay is wrong and the comparator is sampled
     * before it settles -- which would exhaust all 16 combos and report failure
     * on hardware that is actually fine. Measure it against a known time source. */
    extern void ets_delay_us(uint32_t us);
    /* Cross-check three independent clocks so we know which one is lying:
     *   espradio_time_us_now() - the OSI time source
     *   mcycle                 - RISC-V cycle counter (160 MHz => 160 ticks/us)
     *   r_lld_read_clock()     - BLE native clock, 312.5us per tick
     * A real 1000us delay is 160000 cycles and ~3.2 BLE ticks. */
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
    /* Compare the OSI time source against the BLE native clock across the
     * caller's 2s interval. bleclk ticks are 312.5us, so a healthy pair is
     * ~2000000us against ~6400 ticks. This runs in normal goroutine context,
     * unlike the ets_delay_us probe which measures inside a ROM busy-wait. */
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
            (unsigned long)s_task_wake_count, (unsigned long)s_task_wake_nosem);
    BLE_DBG("  probes:");
    for (int i = 0; i < PROBE_COUNT; i++) {
        BLE_DBG(" %s=%lu", s_probe_name[i], (unsigned long)s_probe_hits[i]);
    }
    BLE_DBG("\n");

    /* If a blob critical section leaked (yield between interrupt_disable and
     * interrupt_restore), mstatus.MIE stays 0 for every goroutine and no
     * interrupt can ever be delivered again.  nest!=0 or mie==0 here is the
     * smoking gun for that. */
    uint32_t mstatus;
    __asm__ volatile ("csrr %0, mstatus" : "=r"(mstatus));
    /* ESP32-C3 interrupt matrix: an interrupt is delivered only when its
     * priority is STRICTLY GREATER than CPU_INT_THRESH.  If TinyGo's trap
     * handler leaves the threshold raised to the priority of the interrupt it
     * just serviced (7 for BT), then BT can never fire a second time. */
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

    /* lld_scan_env is a 0x20-byte struct whose first two words are the two
     * scheduler-activity structs (1M PHY, coded PHY).  r_lld_scan_start only
     * allocates one when the corresponding PHY bit is set in the scan params,
     * and only then does it call sch_arb_insert — so a non-zero evt[0] is the
     * decisive proof that the scan activity actually got scheduled. */
    uint32_t evt0 = 0, evt1 = 0;
    if (scan_env) {
        evt0 = *(volatile uint32_t *)(scan_env + 0);
        evt1 = *(volatile uint32_t *)(scan_env + 4);
    }

    /* PHY mask lives at byte 2 of the activity slot's param block:
     * p_llm_env[8] = activity table, slot = 0x44 bytes, slot[0] = param ptr,
     * slot[0x40] = state (6 == scan reserved). */
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

    /* BLE core registers (see r_rwip_timer_hus_set / r_rwble_isr_hack):
     *   +0x0c = interrupt enable/control, bit 11 (0x800) = hus timer target
     *   +0x10 = raw interrupt status
     *   +0xec = CLKN timer target ("COMPVAL")
     *   +0xf0 = fine (half-us) target within the slot */
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

/* ═══════════════════════════════════════════════════════════════════════════
 * VHCI Ring Buffer (controller → host)
 * ═══════════════════════════════════════════════════════════════════════════ */

/* The ring itself lives in vhci_ring.c so the unit-test target can reach it. */
extern int espradio_vhci_ring_push(const uint8_t *data, int len);

static volatile int s_vhci_send_available = 1;

/* Called by ROM controller when it has HCI data for the host. */
static int vhci_host_recv_cb(uint8_t *data, uint16_t len) {
    /* Decode HCI event for debug */
    if (len >= 4 && data[0] == 0x04 && data[1] == 0x0E) {
        /* Command Complete: data[3]=num_cmds, data[4:5]=opcode, data[6]=status */
        if (len >= 7) {
            uint16_t opcode = data[4] | ((uint16_t)data[5] << 8);
            BLE_DBG("vhci_rx: CmdComplete op=0x%04x status=0x%02x\n", opcode, data[6]);
        }
    } else if (len >= 3 && data[0] == 0x04 && data[1] == 0x3E) {
        /* LE Meta Event: data[3]=subevent */
        BLE_DBG("vhci_rx: LE_Meta sub=0x%02x len=%u\n", data[3], (unsigned)len);
    } else {
        BLE_DBG("vhci_rx: len=%u type=0x%02x\n", (unsigned)len, len > 0 ? data[0] : 0);
    }
    int stored = espradio_vhci_ring_push(data, (int)len);
    if (stored < (int)len) {
        BLE_DBG("vhci_rx: drop %d bytes\n", (int)len - stored);
    }
    return 0;
}

/* Called by ROM controller when it can accept a new HCI packet. */
static void vhci_host_send_available_cb(void) {
    s_vhci_send_available = 1;
}

/* espradio_vhci_buffered / _read_byte / _read live in vhci_ring.c */

/* ROM VHCI API */
extern bool API_vhci_host_check_send_available(void);
extern void API_vhci_host_send_packet(const uint8_t *data, uint16_t len);
extern int API_vhci_host_register_callback(const esp_vhci_host_callback_t *callback);

static const esp_vhci_host_callback_t s_vhci_cbs = {
    .notify_host_send_available = vhci_host_send_available_cb,
    .notify_host_recv           = vhci_host_recv_cb,
};

static volatile int s_vhci_tx_busy;

/* Defined further down, next to the scheduler tick. Drives the controller so a
 * packet just handed over is actually picked up. */
static void bt_pump_hci(void);

/* Hand one HCI packet to the controller, and do not return until the
 * controller has taken it.
 *
 * The wait at the end is the important part. The controller has a single HCI
 * input slot, and under the cooperative scheduler nothing runs it between two
 * back-to-back writes -- the 5 ms tick is far away. The host stack emits
 * LE_Set_Advertising_Enable via sendWithoutResponse (which does not wait for
 * the Command Complete) and then, on the Connect -> DiscoverServices path, the
 * first ATT request ~200 us later, both out of its single scratch buffer. The
 * second write reached the slot before the controller had read the first, so
 * the Command Complete for 0x200a never arrived and the controller -- signalled
 * twice but finding the ATT request both times -- transmitted that request
 * twice, which showed up as two Number-Of-Completed-Packets events for one
 * write. The peer received a duplicated request, never answered it, and service
 * discovery timed out.
 *
 * The yields here can reschedule, so a second writer must not enter while a
 * packet is in flight. */
int espradio_vhci_write(const uint8_t *data, int len) {
    if (len <= 0) {
        return 0;
    }

    while (s_vhci_tx_busy) {
        espradio_task_yield_go();
    }
    s_vhci_tx_busy = 1;

    /* Wait until the controller can accept a packet. */
    while (!API_vhci_host_check_send_available()) {
        espradio_task_yield_go();
    }

    BLE_DBG("vhci_tx: len=%d type=0x%02x\n", len, data[0]);
    s_vhci_send_available = 0;
    API_vhci_host_send_packet((uint8_t *)data, (uint16_t)len);
    bt_pump_hci();

    s_vhci_tx_busy = 0;
    return len;
}

/* ═══════════════════════════════════════════════════════════════════════════
 * BT OSI Function Table
 * ═══════════════════════════════════════════════════════════════════════════ */

/* Forward declarations for existing WiFi OSI primitives (from osi.c / radio.go) */
extern void *espradio_semphr_create(uint32_t max, uint32_t init);
extern void  espradio_semphr_delete(void *semphr);
extern int32_t espradio_semphr_take(void *semphr, uint32_t block_time_tick);
extern int32_t espradio_semphr_give(void *semphr);
extern void *espradio_recursive_mutex_create(void);
extern void  espradio_mutex_delete(void *mutex);
extern int32_t espradio_mutex_lock(void *mutex);
extern int32_t espradio_mutex_unlock(void *mutex);
extern void *espradio_arena_alloc(size_t size);
extern void  espradio_arena_free(void *p);
extern bool  espradio_is_from_isr(void);
extern void  espradio_task_yield_go(void);
extern void  espradio_run_task(void *task_func, void *param);
extern uint64_t espradio_time_us_now(void);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);

/* ─── Queue primitives (reuse from osi.c) ─── */
extern void *espradio_queue_create_internal(uint32_t len, uint32_t item_size);
extern void  espradio_queue_delete_internal(void *queue);
extern int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
extern int32_t espradio_queue_send_from_isr(void *queue, void *item, void *hptw);
extern int32_t espradio_queue_recv(void *queue, void *item, uint32_t block_time_tick);
extern int32_t espradio_queue_recv_from_isr(void *queue, void *item, void *hptw);

/* ─── ISR context tracking ─── */
static volatile int s_bt_in_isr;

static int bt_is_in_isr(void) {
    return s_bt_in_isr > 0 || espradio_is_from_isr();
}

/* ─── Interrupt disable/restore ───
 * The blob relies on these being a REAL critical section, not advisory hints.
 * r_ke_task_schedule_hack wraps its ke_env queue extraction in
 * osi_funcs[0x14]/[0x18] (= these two), and the RWBLE ISR concurrently pushes
 * onto the same ke queues via ke_msg_send_from_isr.  With no-ops here the ke
 * message list gets corrupted and event delivery silently stops.
 *
 * Implemented as a counted save/restore of mstatus.MIE rather than by touching
 * CPU_INT_THRESH.  Nesting is tracked so the inner-most restore doesn't
 * re-enable early.  The blob never yields inside these regions (in ESP-IDF they
 * map to portENTER_CRITICAL), so the cooperative scheduler can't leak a
 * disabled state across goroutines. */
static void bt_interrupt_disable(void) {
    uint32_t mstatus;
    /* Atomically clear mstatus.MIE (bit 3) and return the previous value. */
    __asm__ volatile ("csrrci %0, mstatus, 8" : "=r"(mstatus) :: "memory");
    if (s_bt_int_nesting == 0) {
        s_bt_int_saved_mie = mstatus & 0x8u;
    }
    s_bt_int_nesting++;
}

static void bt_interrupt_restore(void) {
    if (s_bt_int_nesting == 0) {
        return; /* unbalanced restore — ignore rather than enabling early */
    }
    if (--s_bt_int_nesting == 0 && s_bt_int_saved_mie) {
        __asm__ volatile ("csrsi mstatus, 8" ::: "memory");
    }
}

/* ─── Interrupt alloc/handler_set ─── */
/* Called from Go (espradio_bt_enable_interrupts) after init to enable hw ints */
extern void espradio_bt_enable_hw_interrupts(void);

static void bt_interrupt_handler_set(int interrupt_no, void (*func)(void *), void *arg);

static int bt_interrupt_alloc(int cpu_no, int intr_source,
                              void (*handler)(void *), void *arg,
                              void **ret_handle) {
    (void)cpu_no;
    (void)ret_handle;
    BLE_DBG("bt_interrupt_alloc: src=%d handler=%p\n", intr_source, handler);
    /* The blob passes the ISR handler here (not via interrupt_handler_set).
     * Map peripheral source to interrupt index and store it.
     * Source 5 (BT_BB) and 7 (RWBT) → index 5; Source 8 (RWBLE) → index 8. */
    int int_no = -1;
    if (intr_source == 5 || intr_source == 7) {
        int_no = 5;
    } else if (intr_source == 8) {
        int_no = 8;
    }
    if (int_no >= 0) {
        bt_interrupt_handler_set(int_no, handler, arg);
    }
    return 0;
}

static int bt_interrupt_free(void *handle) {
    (void)handle;
    return 0;
}

static void bt_interrupt_handler_set(int interrupt_no, void (*func)(void *), void *arg) {
    BLE_DBG("bt_interrupt_handler_set: int=%d fn=%p arg=%p\n", interrupt_no, func, arg);
    switch (interrupt_no) {
    case 5:
        s_bt_isr_fn_5 = (bt_isr_fn_t)func;
        s_bt_isr_arg_5 = arg;
        break;
    case 8:
        s_bt_isr_fn_8 = (bt_isr_fn_t)func;
        s_bt_isr_arg_8 = arg;
        break;
    default:
        BLE_DBG("bt_interrupt_handler_set: unsupported int %d\n", interrupt_no);
        break;
    }
    /* Enable the hardware interrupt immediately (matching esp-hal behavior).
     * The blob expects the interrupt to be active right after handler_set. */
    espradio_bt_enable_hw_interrupts();
}

static int bt_interrupt_on(int intr_num) {
    (void)intr_num;
    return 0;
}

static int bt_interrupt_off(int intr_num) {
    (void)intr_num;
    return 0;
}

/* ─── Task management ─── */
extern int32_t espradio_task_create_pinned_to_core(void *func, const char *name,
    uint32_t stack_depth, void *param, uint32_t prio, void *handle, uint32_t core_id);

static int bt_task_create(void *func, const char *name, uint32_t stack_depth,
                          void *param, uint32_t prio, void *handle, uint32_t core_id) {
    BLE_DBG("bt_task_create: %s stack=%u\n", name ? name : "(null)", stack_depth);
    return (int)espradio_task_create_pinned_to_core(func, name, stack_depth,
                                                    param, prio, handle, core_id);
}

static void bt_task_delete(void *handle) {
    (void)handle;
    BLE_DBG("bt_task_delete: %p (no-op)\n", handle);
}

/* ─── Semaphore from ISR ─── */
static int bt_semphr_take_from_isr(void *semphr, void *hptw) {
    (void)hptw;
    return espradio_semphr_take(semphr, 0);
}

static int bt_semphr_give_from_isr(void *semphr, void *hptw) {
    (void)hptw;
    return espradio_semphr_give(semphr);
}

/* ─── Memory ─── */
static void *bt_malloc(uint32_t size) {
    return espradio_arena_alloc((size_t)size);
}

static void bt_free(void *ptr) {
    espradio_arena_free(ptr);
}

static int bt_read_efuse_mac(void *mac) {
    return espradio_hal_read_mac_go((unsigned char *)mac, 2 /* BT */);
}

/* ─── Random ─── */
static void bt_srand(uint32_t seed) { (void)seed; }
static int  bt_rand(void) {
    return (int)(*(volatile uint32_t *)0x600260B0u); /* RNG_DATA_REG */
}

/* ─── Time/Clock stubs ─── */
static uint32_t bt_lpcycles_2_hus(uint32_t cycles, uint32_t err_corr) {
    (void)err_corr;
    return cycles * 2; /* placeholder: 1 LP cycle ≈ 2 half-us at 500kHz */
}

static uint32_t bt_hus_2_lpcycles(uint32_t us) {
    return us / 2;
}

static int bt_sleep_check_duration(int slot_cnt) { (void)slot_cnt; return 0; }
static void bt_sleep_enter_phase1(int lpcycles)  { (void)lpcycles; }
static void bt_sleep_enter_phase2(void)          {}
static void bt_sleep_exit_phase1(void)           {}
static void bt_sleep_exit_phase2(void)           {}
static void bt_sleep_exit_phase3(void)           {}

/* ─── Coexistence ─── */
static void bt_coex_wifi_sleep_set(int sleep) { (void)sleep; }

static int bt_coex_core_ble_conn_dyn_prio_get(int *low, int *high) {
    if (low) *low = 0;
    if (high) *high = 0;
    return 0;
}

static int bt_coex_schm_register_btdm_callback(void *callback) {
    (void)callback;
    return 0;
}

static void bt_coex_schm_status_bit_set(int typ, int status) {
    (void)typ; (void)status;
}

static void bt_coex_schm_status_bit_clear(int typ, int status) {
    (void)typ; (void)status;
}

static uint32_t bt_coex_schm_interval_get(void) { return 0; }
static uint8_t  bt_coex_schm_curr_period_get(void) { return 0; }
static void    *bt_coex_schm_curr_phase_get(void) { return NULL; }

/* ─── Wakeup ─── */
extern void btdm_wakeup_request(void);
extern void btdm_in_wakeup_requesting_set(bool set);

static volatile uint32_t s_wakeup_request_count;

static void bt_coex_bt_wakeup_request(void) {
    s_wakeup_request_count++;
    BLE_DBG("coex_bt_wakeup_request (#%lu pwr=%lu)\n",
            (unsigned long)s_wakeup_request_count,
            (unsigned long)*(volatile uint32_t *)0x3fcdff18u);
    btdm_wakeup_request();
}

static void bt_coex_bt_wakeup_request_end(void) {
    btdm_in_wakeup_requesting_set(false);
}

uint32_t espradio_bt_wakeup_count(void) { return s_wakeup_request_count; }

/* Called from schedOnce() to drive the BLE link-layer scheduler.
 * Programs COMPVAL for next scan/connection event. Safe from non-ISR context.
 * Unlike calling the ISR (which corrupts state), this just runs the scheduler. */
extern void r_rwip_schedule(void);
extern void r_ke_event_schedule(void);
extern void r_ke_task_schedule(void);

/* What the 5 ms ticker is allowed to drive.
 *   bit 0 — ke message pump (ke_event_schedule + ke_task_schedule)
 *   bit 1 — rwip_schedule (reprograms the BLE hardware timer)
 * The message pump is REQUIRED: the controller task goroutine does not run its
 * own loop under the cooperative scheduler, so without this HCI commands are
 * never dequeued and even LE Set Scan Enable never reaches lld_scan_start.
 * rwip_schedule must stay OFF — see espradio_bt_sched_tick(). */
#define BT_TICK_KE_PUMP       0x1  /* ke_event_schedule (event dispatch)  */
#define BT_TICK_RWIP_SCHEDULE 0x2  /* rwip_schedule (LL scheduler)        */
#define BT_TICK_KE_TASK       0x4  /* ke_task_schedule (message dispatch) */

/* ke_event_schedule is the dispatcher that matters: it walks ke_env.evt_field
 * and calls each pending event's callback, and ke_task_schedule is simply the
 * callback registered for event 3 (r_ke_task_init does
 * ke_event_callback_set(3, ke_task_schedule)), so message dispatch already
 * happens inside it. That is why BT_TICK_KE_TASK stays off: driving
 * ke_task_schedule from the tick as well only runs message dispatch a second
 * time. */
static int s_sched_tick_mask = BT_TICK_KE_PUMP | BT_TICK_RWIP_SCHEDULE;

void espradio_bt_set_sched_tick_mask(int mask) {
    s_sched_tick_mask = mask;
}

/* Drive the controller until it has taken the packet just handed to it, so
 * back-to-back writes cannot overwrite its single HCI input slot.
 *
 * API_vhci_host_check_send_available() is no use as the completion signal here:
 * it reports capacity and is already true immediately after a send, so polling
 * it returns at once without the controller having run. The real signal is
 * notify_host_send_available, latched in s_vhci_send_available and cleared by
 * the writer before handing the packet over.
 *
 * Reaching the controller also takes more than a yield: btdm_controller_task
 * blocks on its semaphore, so it has to be woken explicitly. Bounded, because
 * the ROM only promises the callback on a transition. */
static void bt_pump_hci(void) {
    for (int i = 0; i < 4 && !s_vhci_send_available; i++) {
        bt_isr_wake_task();
        espradio_task_yield_go();
        r_ke_event_schedule();
    }
}

void espradio_bt_sched_tick(void) {
    if (!s_bt_isr_fn_8) return; /* BLE not initialized */

    /* Watch for any sign of life from the BLE core's event-reporting paths.
     * r_lld_core_init puts the core in IRQ-FIFO mode (INTCNTL = 0x640000|0x166,
     * 0x600312d8 |= 0x8000001e), so r_rwble_isr_hack takes event status from the
     * FIFO at +0x2d8 rather than the classic status register at +0x10:
     *   count = (dat2d8 >> 5) & 0x1f,  entry = (dat2d8 << 1) >> 11
     * Latch whether either ever becomes non-zero.  If the FIFO does fill but no
     * interrupt arrives, the fault is interrupt delivery/masking; if it stays
     * empty, the programmed radio event genuinely never completes. */
    uint32_t fifo = *(volatile uint32_t *)0x600312d8u;
    uint32_t stat = *(volatile uint32_t *)0x60031010u;
    if (((fifo >> 5) & 0x1fu) != 0) s_fifo_seen_count++;
    if (stat != 0) {
        s_stat_seen_count++;
        s_stat_seen_bits |= stat;
    }

    /* Heartbeat the controller task.
     *
     * btdm_controller_task blocks on btdm_ol_task_env->sem and, once released,
     * runs btdm_rw_run(s_btdm_state) -> rw_schedule() even when the message
     * queue is empty.  In a preemptive build the RWBLE ISR supplies that wake on
     * every event; here the ISR currently fires only once, so give the semaphore
     * on each tick to keep the task's loop turning. This is the mechanism that
     * actually lets the blob reschedule its own activities, rather than us
     * calling its scheduler from the wrong context. */
    bt_wake_task_throttled();

    /* The ke message pump has to run from somewhere: the blob's controller task
     * goroutine does not spin its own loop here, so nothing else dequeues ke
     * messages and HCI commands would never be executed at all. */
    if (s_sched_tick_mask & BT_TICK_KE_PUMP) {
        r_ke_event_schedule();
    }
    if (s_sched_tick_mask & BT_TICK_KE_TASK) {
        r_ke_task_schedule();
    }

    /* r_rwip_schedule() ends in sch_arb_prog_timer(), which reprograms the BLE
     * hardware timer from the arbiter list head, and calls
     * rwip_timer_hus_set(0xffffffff) (clearing timer-enable bit 11 of BLE +0x0c)
     * when that list is empty.  Driving it from an unrelated goroutine at 200 Hz
     * is not how the blob is meant to be scheduled, so it is a suspect worth
     * keeping switchable — but measured A/B it makes no difference to the
     * current failure, and the bit-11 clear seen after the first event comes
     * from rwip_timer_hus_handler (the half-us timer is a deliberate one-shot),
     * NOT from this call.  Left enabled; do not "fix" it without evidence. */
    if (s_sched_tick_mask & BT_TICK_RWIP_SCHEDULE) {
        r_rwip_schedule();
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

/* ─── Power ─── */
static void bt_hw_power_down(void) {}
static void bt_hw_power_up(void) {}

/* ─── Misc ─── */
extern void ets_backup_dma_copy(uint32_t reg, uint32_t mem_addr, uint32_t num, int to_rem);
extern void ets_delay_us(uint32_t us);

static void bt_ets_backup_dma_copy(uint32_t reg, uint32_t mem_addr, uint32_t num, int to_rem) {
    ets_backup_dma_copy(reg, mem_addr, num, to_rem);
}

static void bt_ets_delay_us(uint32_t us) {
    ets_delay_us(us);
}

/* ROM table ready — reset function pointer tables in ROM */
extern void ble_base_funcs_reset(void);
extern void ble_42_adv_funcs_reset(void);
extern void ble_ext_adv_funcs_reset(void);
extern void ble_dtm_funcs_reset(void);
extern void ble_scan_funcs_reset(void);
extern void ble_ext_scan_funcs_reset(void);
extern void ble_enc_funcs_reset(void);
extern void ble_init_funcs_reset(void);
extern void ble_con_funcs_reset(void);

/* Patch ALL of the ROM function-pointer tables, in the same order as ESP-IDF
 * and esp-hal.  Partial patching is not a supported configuration: these
 * tables are one ABI.  ble_base_funcs_reset() installs the flash scheduler and
 * ISR core (ip[0x6a8]=sch_arb_event_start_isr_hack, ip[0x12c]=rwble_isr_hack,
 * ip[0x6c0]=sch_prog_end_isr_hack, ip[0x7a8]=sch_prog_ble_push_hack), and those
 * only interoperate with the matching flash scan entries installed here:
 *   ble_scan_funcs_reset():     ip[0x438]=lld_scan_start_eco,
 *                               ip[0x514]=llm_scan_start_eco,
 *                               ip[0x3ec]=lld_scan_evt_start_cbk_eco,
 *                               ip[0x3fc]=lld_scan_frm_skip_isr_eco,
 *                               ip[0x408]=lld_scan_process_pkt_rx_hack,
 *                               ip[0x424]=lld_scan_process_pkt_rx_adv_rep_hack
 *   ble_ext_scan_funcs_reset(): ip[0x3f4]=lld_scan_frm_eof_isr_eco
 * ip[0x408]/[0x424] are the advertising-report RX path — leaving them at the
 * ROM versions means received advertisements never turn into HCI LE Advertising
 * Report events, even when the radio and scheduler are working. */
static void bt_rom_table_ready(void) {
    BLE_DBG("bt_rom_table_ready\n");
    ble_base_funcs_reset();
    ble_42_adv_funcs_reset();
    ble_ext_adv_funcs_reset();
    ble_dtm_funcs_reset();
    ble_scan_funcs_reset();
    ble_ext_scan_funcs_reset();
    ble_enc_funcs_reset();
    ble_init_funcs_reset();
    ble_con_funcs_reset();
}

static uint64_t bt_get_time_us(void) {
    return espradio_time_us_now();
}

static void bt_assert(void) {
    espradio_panic("BT assert");
}

/* ─── OSI Function Table (matches esp-hal os_adapter_esp32c3_s3.rs exactly) ─── */

typedef struct {
    uint32_t magic;
    uint32_t version;
    int  (*interrupt_alloc)(int, int, void (*)(void *), void *, void **);
    int  (*interrupt_free)(void *);
    void (*interrupt_handler_set)(int, void (*)(void *), void *);
    void (*interrupt_disable)(void);
    void (*interrupt_restore)(void);
    void (*task_yield)(void);
    void (*task_yield_from_isr)(void);
    void *(*semphr_create)(uint32_t, uint32_t);
    void (*semphr_delete)(void *);
    int  (*semphr_take_from_isr)(void *, void *);
    int  (*semphr_give_from_isr)(void *, void *);
    int  (*semphr_take)(void *, uint32_t);
    int  (*semphr_give)(void *);
    void *(*mutex_create)(void);
    void (*mutex_delete)(void *);
    int  (*mutex_lock)(void *);
    int  (*mutex_unlock)(void *);
    void *(*queue_create)(uint32_t, uint32_t);
    void (*queue_delete)(void *);
    int  (*queue_send)(void *, void *, uint32_t);
    int  (*queue_send_from_isr)(void *, void *, void *);
    int  (*queue_recv)(void *, void *, uint32_t);
    int  (*queue_recv_from_isr)(void *, void *, void *);
    int  (*task_create)(void *, const char *, uint32_t, void *, uint32_t, void *, uint32_t);
    void (*task_delete)(void *);
    int  (*is_in_isr)(void);
    int  (*cause_sw_intr_to_core)(int, int);  /* NULL on C3 */
    void *(*malloc)(uint32_t);
    void *(*malloc_internal)(uint32_t);
    void (*free)(void *);
    int  (*read_efuse_mac)(void *);
    void (*srand)(uint32_t);
    int  (*rand)(void);
    uint32_t (*btdm_lpcycles_2_hus)(uint32_t, uint32_t);
    uint32_t (*btdm_hus_2_lpcycles)(uint32_t);
    int  (*btdm_sleep_check_duration)(int);
    void (*btdm_sleep_enter_phase1)(int);
    void (*btdm_sleep_enter_phase2)(void);
    void (*btdm_sleep_exit_phase1)(void);
    void (*btdm_sleep_exit_phase2)(void);
    void (*btdm_sleep_exit_phase3)(void);
    void (*coex_wifi_sleep_set)(int);
    int  (*coex_core_ble_conn_dyn_prio_get)(int *, int *);
    int  (*coex_schm_register_btdm_callback)(void *);
    void (*coex_schm_status_bit_set)(int, int);
    void (*coex_schm_status_bit_clear)(int, int);
    uint32_t (*coex_schm_interval_get)(void);
    uint8_t  (*coex_schm_curr_period_get)(void);
    void    *(*coex_schm_curr_phase_get)(void);
    int  (*interrupt_on)(int);
    int  (*interrupt_off)(int);
    void (*esp_hw_power_down)(void);
    void (*esp_hw_power_up)(void);
    void (*ets_backup_dma_copy)(uint32_t, uint32_t, uint32_t, int);
    void (*ets_delay_us)(uint32_t);
    void (*btdm_rom_table_ready)(void);
    void (*coex_bt_wakeup_request)(void);
    void (*coex_bt_wakeup_request_end)(void);
    uint64_t (*get_time_us)(void);
    void (*assert)(void);
} bt_osi_funcs_t;

static const bt_osi_funcs_t s_bt_osi_funcs = {
    .magic   = 0xFADEBEAD,
    .version = 0x0001000A,
    .interrupt_alloc          = bt_interrupt_alloc,
    .interrupt_free           = bt_interrupt_free,
    .interrupt_handler_set    = bt_interrupt_handler_set,
    .interrupt_disable        = bt_interrupt_disable,
    .interrupt_restore        = bt_interrupt_restore,
    .task_yield               = espradio_task_yield_go,
    .task_yield_from_isr      = espradio_task_yield_go,
    .semphr_create            = espradio_semphr_create,
    .semphr_delete            = espradio_semphr_delete,
    .semphr_take_from_isr     = bt_semphr_take_from_isr,
    .semphr_give_from_isr     = bt_semphr_give_from_isr,
    .semphr_take              = espradio_semphr_take,
    .semphr_give              = espradio_semphr_give,
    .mutex_create             = espradio_recursive_mutex_create,
    .mutex_delete             = espradio_mutex_delete,
    .mutex_lock               = espradio_mutex_lock,
    .mutex_unlock             = espradio_mutex_unlock,
    .queue_create             = espradio_queue_create_internal,
    .queue_delete             = espradio_queue_delete_internal,
    .queue_send               = espradio_queue_send,
    .queue_send_from_isr      = espradio_queue_send_from_isr,
    .queue_recv               = espradio_queue_recv,
    .queue_recv_from_isr      = espradio_queue_recv_from_isr,
    .task_create              = bt_task_create,
    .task_delete              = bt_task_delete,
    .is_in_isr                = bt_is_in_isr,
    .cause_sw_intr_to_core    = NULL, /* not supported on RISC-V */
    .malloc                   = bt_malloc,
    .malloc_internal          = bt_malloc,
    .free                     = bt_free,
    .read_efuse_mac           = bt_read_efuse_mac,
    .srand                    = bt_srand,
    .rand                     = bt_rand,
    .btdm_lpcycles_2_hus      = bt_lpcycles_2_hus,
    .btdm_hus_2_lpcycles      = bt_hus_2_lpcycles,
    .btdm_sleep_check_duration = bt_sleep_check_duration,
    .btdm_sleep_enter_phase1  = bt_sleep_enter_phase1,
    .btdm_sleep_enter_phase2  = bt_sleep_enter_phase2,
    .btdm_sleep_exit_phase1   = bt_sleep_exit_phase1,
    .btdm_sleep_exit_phase2   = bt_sleep_exit_phase2,
    .btdm_sleep_exit_phase3   = bt_sleep_exit_phase3,
    .coex_wifi_sleep_set      = bt_coex_wifi_sleep_set,
    .coex_core_ble_conn_dyn_prio_get = bt_coex_core_ble_conn_dyn_prio_get,
    .coex_schm_register_btdm_callback = bt_coex_schm_register_btdm_callback,
    .coex_schm_status_bit_set   = bt_coex_schm_status_bit_set,
    .coex_schm_status_bit_clear = bt_coex_schm_status_bit_clear,
    .coex_schm_interval_get     = bt_coex_schm_interval_get,
    .coex_schm_curr_period_get  = bt_coex_schm_curr_period_get,
    .coex_schm_curr_phase_get   = bt_coex_schm_curr_phase_get,
    .interrupt_on             = bt_interrupt_on,
    .interrupt_off            = bt_interrupt_off,
    .esp_hw_power_down        = bt_hw_power_down,
    .esp_hw_power_up          = bt_hw_power_up,
    .ets_backup_dma_copy      = bt_ets_backup_dma_copy,
    .ets_delay_us             = bt_ets_delay_us,
    .btdm_rom_table_ready     = bt_rom_table_ready,
    .coex_bt_wakeup_request   = bt_coex_bt_wakeup_request,
    .coex_bt_wakeup_request_end = bt_coex_bt_wakeup_request_end,
    .get_time_us              = bt_get_time_us,
    .assert                   = bt_assert,
};

/* ═══════════════════════════════════════════════════════════════════════════
 * BLE Controller Initialization
 * ═══════════════════════════════════════════════════════════════════════════ */

/* ROM functions from libbtdm_app.a */
extern int  btdm_osi_funcs_register(const void *osi_funcs);
extern int  btdm_controller_rom_data_init(void);
extern int  btdm_controller_init(const void *config);
extern void btdm_controller_enable(uint32_t mode);
extern void coex_pti_v2(void);

/* Config constants */
#define ESP_BT_MODE_BLE               1

/* CFG_MASK and SLAVE_CE_LEN_MIN from esp-hal defaults */
#define BT_CFG_MASK                   0x00000001
#define SLAVE_CE_LEN_MIN_DEFAULT      5

/* The blob's internal diagnostics (scan CS programming, "BLE assert %s %d",
 * PTI/coex state) are all gated behind this and default to 0 = silent.
 * Raising it is the only way to see the controller explain itself. */
extern uint32_t g_bt_plf_log_level;

int espradio_ble_init(void) {
    BLE_DBG("espradio_ble_init: start\n");

#if ESPRADIO_BLE_DEBUG
    g_bt_plf_log_level = 10;
    BLE_DBG("  g_bt_plf_log_level=%lu\n", (unsigned long)g_bt_plf_log_level);
#endif

    /* Step 0: Enable BT peripheral clock and reset.
     * The modem reset was done by initHardware() (Go side) but clock enable
     * is separate. Without the clock, btdm_controller_init fails with
     * "Funcs table create fails". */
    #define APB_CTRL_WIFI_CLK_EN_REG  (*(volatile uint32_t *)0x60026014u)
    #define APB_CTRL_WIFI_RST_EN_REG  (*(volatile uint32_t *)0x60026018u)
    /* Full modem clock enable mask (WiFi+BT+coex) — same as ESP-IDF's
     * SYSTEM_WIFI_CLK_EN value. Just BT bits (0x860) isn't enough; the BT_BB
     * CLKNCNT counter needs the full modem domain clock tree active. */
    #define MODEM_CLK_EN  0x00FB9FCFu
    /* BT reset bits: BTBB(3), BTMAC(4), RW_BTMAC(9), RW_BTMAC_REG(11), BTBB_REG(13) */
    #define BT_RST_BITS     ((1u << 3) | (1u << 4) | (1u << 9) | (1u << 11) | (1u << 13))

    /* ORDER MATTERS: clock first, THEN release reset.
     *
     * ESP-IDF brings a modem block up via periph_module_enable() ->
     * periph_ll_enable_clk_clear_rst(), which sets the clock-enable mask and only
     * then clears the reset mask.  initHardware() (Go side) pulses the BT reset
     * bits, but it runs before this function, i.e. before any modem clock is
     * enabled — so BT_BB and RW_BTMAC were being released from reset with no
     * clock running, which leaves their internal state machines undefined.  The
     * visible symptom was r_cali_phase_match_p sweeping all 16 hi/lo settings
     * without BLE +0xf8 bit 12 ever asserting ("phase match cali failed!"), i.e.
     * an uncalibrated receiver.
     *
     * Enable the clock, let it settle, then pulse the BT resets so the blocks
     * come out of reset with a running clock. */
    /* RTC_CNTL DIG_PWC bit 11 = BT_FORCE_PD, DIG_ISO bit 22 = BT_FORCE_ISO.
     * If either is still set the BT analog/RF domain stays powered down or
     * isolated even though its digital registers respond normally. */
    BLE_DBG("  clk/rst before: clk=0x%08lx rst=0x%08lx dig_pwc=0x%08lx dig_iso=0x%08lx\n",
            (unsigned long)APB_CTRL_WIFI_CLK_EN_REG,
            (unsigned long)APB_CTRL_WIFI_RST_EN_REG,
            (unsigned long)*(volatile uint32_t *)0x60008088u,  /* RTC_CNTL_DIG_PWC_REG */
            (unsigned long)*(volatile uint32_t *)0x6000808Cu); /* RTC_CNTL_DIG_ISO_REG */

    /* Clear-then-set, exactly as ESP-IDF's esp_perip_clk_init() and esp-hal's
     * init_clocks() do:
     *
     *     wifi_clk_en = (wifi_clk_en & ~WIFI_BT_SDIO_CLK) | SYSTEM_WIFI_CLK_EN
     *
     * A bare |= is NOT equivalent. Bit 12 is inside SYSTEM_WIFI_CLK_EN so it
     * comes back either way, but bit 5 is not — so the clear is the only thing
     * that ever turns bit 5 off, and the register's power-on default
     * (0xfffce030) has it on. Bit 5 is ESP-IDF's SYSTEM_WIFI_CLK_UNUSED_BIT5,
     * the internal analog I2C clock used to program the RF/BBPLL. Leaving it
     * enabled left this register at 0xffffffff instead of 0xffffffdf. */
    #define SYSTEM_WIFI_CLK_I2C_CLK_EN    (1u << 5)
    #define SYSTEM_WIFI_CLK_UNUSED_BIT12  (1u << 12)
    #define WIFI_BT_SDIO_CLK  (SYSTEM_WIFI_CLK_I2C_CLK_EN | SYSTEM_WIFI_CLK_UNUSED_BIT12)

    APB_CTRL_WIFI_CLK_EN_REG =
        (APB_CTRL_WIFI_CLK_EN_REG & ~WIFI_BT_SDIO_CLK) | MODEM_CLK_EN;
    __asm__ volatile ("fence" ::: "memory");
    ets_delay_us(50);

    /* Assert then release only the BT bits, so a WiFi session that is already
     * running is left alone. */
    APB_CTRL_WIFI_RST_EN_REG |= BT_RST_BITS;
    __asm__ volatile ("fence" ::: "memory");
    ets_delay_us(10);
    APB_CTRL_WIFI_RST_EN_REG &= ~BT_RST_BITS;
    __asm__ volatile ("fence" ::: "memory");
    ets_delay_us(50);

    BLE_DBG("  clk/rst after:  clk=0x%08lx rst=0x%08lx\n",
            (unsigned long)APB_CTRL_WIFI_CLK_EN_REG,
            (unsigned long)APB_CTRL_WIFI_RST_EN_REG);

    /* Step 0b: Tell the ROM what the CPU is actually running at.
     *
     * ROM ets_delay_us() busy-waits using a ticks-per-microsecond value cached in
     * ROM data, which is only updated by ets_update_cpu_frequency(). TinyGo raises
     * the CPU clock without calling it, so the ROM kept a stale (20 MHz) value and
     * every ets_delay_us() came back 8x early -- measured: a requested 100ms
     * elapsed in 12.5ms against the BLE native clock.
     *
     * The blob leans on ets_delay_us for hardware settling all through RF and
     * baseband bring-up. r_cali_phase_match_p in particular polls its "phase
     * locked" bit with just ets_delay_us(1) between attempts, so at 1/8 scale it
     * samples the comparator ~125ns after kicking it, never sees the bit set, and
     * burns all 16 hi/lo combinations in a few microseconds -- reporting
     * "phase match cali failed!" on hardware that never got a chance to settle. */
    extern void ets_update_cpu_frequency(uint32_t ticks_per_us);
    ets_update_cpu_frequency(160);
    BLE_DBG("  ets_update_cpu_frequency(160) done\n");

    /* Step 1: ROM data init */
    btdm_controller_rom_data_init();
    BLE_DBG("  rom_data_init done\n");

    /* Step 2: Build config */
    esp_bt_controller_config_t cfg = {0};
    cfg.version = ESP_BT_CTRL_CONFIG_VERSION;
    cfg.controller_task_stack_size = 8192;
    cfg.controller_task_prio = 253; /* high priority */
    cfg.controller_task_run_cpu = 0;
    cfg.bluetooth_mode = ESP_BT_MODE_BLE;
    cfg.ble_max_act = 6;
    cfg.sleep_mode = 0;
    cfg.sleep_clock = 0;
    cfg.ble_st_acl_tx_buf_nb = 0;
    cfg.ble_hw_cca_check = 0;
    cfg.ble_adv_dup_filt_max = 30;
    cfg.coex_param_en = 0;
    cfg.ce_len_type = 0;
    cfg.coex_use_hooks = 0;
    cfg.hci_tl_type = 1; /* VHCI */
    cfg.hci_tl_funcs = NULL;
    cfg.txant_dft = 0;
    cfg.rxant_dft = 0;
    cfg.txpwr_dft = 9; /* +9 dBm */
    cfg.cfg_mask = BT_CFG_MASK;
    cfg.scan_duplicate_mode = 0;
    cfg.scan_duplicate_type = 0;
    cfg.mesh_adv_size = 0;
    cfg.normal_adv_size = 100;
    cfg.coex_phy_coded_tx_rx_time_limit = 0;
    cfg.hw_target_code = 0x01010000; /* ESP32-C3 specific */
    cfg.slave_ce_len_min = SLAVE_CE_LEN_MIN_DEFAULT;
    cfg.hw_recorrect_en = 0;
    cfg.cca_thresh = 75;
    cfg.dup_list_refresh_period = 0;
    cfg.scan_backoff_upperlimitmax = 0;
    /* Match BT_CONTROLLER_INIT_CONFIG_DEFAULT / esp-hal: BLE 5.0 features on.
     * The flash scan handlers accept the legacy commands (0x200B/0x200C) that
     * the bluetooth package sends; forcing this to 0 diverges from the only
     * configuration the blob's function tables are built for. */
    cfg.ble_50_feat_supp = 1;
    cfg.ble_cca_mode = 0;
    cfg.ble_chan_ass_en = 0;
    cfg.ble_data_lenth_zero_aux = 0;
    cfg.ble_ping_en = 0;
    cfg.ble_llcp_disc_flag = 0;
    cfg.run_in_flash = 0;
    cfg.dtm_en = 1;
    cfg.enc_en = 1;
    cfg.qa_test = 0;
    cfg.connect_en = 1;
    cfg.scan_en = 1;
    cfg.ble_aa_check = 1;
    cfg.adv_en = 1;
    cfg.magic = ESP_BT_CTRL_CONFIG_MAGIC_VAL;

    /* Step 3: Register OSI functions */
    int res = btdm_osi_funcs_register(&s_bt_osi_funcs);
    if (res != 0) {
        BLE_DBG("  btdm_osi_funcs_register FAILED: %d\n", res);
        return -1;
    }
    BLE_DBG("  osi_funcs registered\n");

    /* Step 4: Power domain + modem init (prerequisites for PHY) */
    extern void esp_wifi_bt_power_domain_on(void);
    esp_wifi_bt_power_domain_on();
    extern void esp_phy_modem_init(void);
    esp_phy_modem_init();
    BLE_DBG("  power domain + modem init done\n");

    /* Step 4c: Disable sleep at ROM level BEFORE controller_init.
     * The task starts during init and calls rw_schedule → r_rwip_sleep.
     * With rw_sleep_enable=0, the sleep path is skipped. */
    *(volatile uint32_t *)0x3fcdfc34u = 0; /* rw_sleep_enable = 0 */

    /* Step 5: Enable the PHY *before* btdm_controller_init().
     *
     * This ordering is not cosmetic.  ESP-IDF's esp_bt_controller_init() for the
     * C3 (components/bt/controller/esp32c3/bt.c) runs:
     *
     *     periph_module_enable(PERIPH_BT_MODULE);
     *     periph_module_reset(PERIPH_BT_MODULE);
     *     esp_phy_enable();            <-- PHY up first
     *     btdm_controller_init(cfg);   <-- then the controller
     *     coex_pti_v2();
     *
     * btdm_controller_init() runs r_rw_rf_init() ("initialise RF LC Todd"), which
     * programs the BT RF front-end, and btdm_controller_enable() then runs
     * r_cali_phase_match_p() to calibrate BB<->RF phase alignment.  With the PHY
     * still unpowered at that point the RF is initialised against a dead PLL, and
     * the calibration sweeps all 16 hi/lo settings without BLE +0xf8 bit 12 ever
     * asserting -> "phase match cali failed!" -> uncalibrated receiver.
     *
     * (esp-hal has these two the other way round, which is where the previous
     * "matching esp-hal" comment came from; ESP-IDF is the authority here.) */
    BLE_DBG("  calling esp_phy_enable(BT)...\n");
    esp_phy_enable(2 /* PHY_MODEM_BT */);
    BLE_DBG("  phy_enable done\n");

    /* Step 6: Init controller (creates btController task goroutine) */
    res = btdm_controller_init(&cfg);
    if (res != 0) {
        BLE_DBG("  btdm_controller_init FAILED: %d\n", res);
        return -2;
    }
    BLE_DBG("  controller_init done\n");

    /* Step 6b: Tell PHY that BT is active (not WiFi-only).
     * Without this, the radio RF path isn't configured for BLE reception. */
    extern void phy_set_wifi_mode_only(bool wifi_only);
    phy_set_wifi_mode_only(false);
    BLE_DBG("  phy wifi_mode_only=false\n");

    /* Step 7: Coex PTI (C3/S3 — esp-hal does this instead of bt_bb_v2_init) */
    coex_pti_v2();
    BLE_DBG("  coex_pti_v2 done\n");

    /* Step 8: Enable BLE mode (blob calls interrupt_handler_set here) */
    btdm_controller_enable(ESP_BT_MODE_BLE);
    BLE_DBG("  controller enabled (BLE)\n");

    /* Immediately yield to let the controller task run while internal state
     * (pwr_state) may be temporarily set by controller_enable. In a preemptive
     * scheduler, the task would run immediately; we simulate this. */
    for (int i = 0; i < 50; i++) {
        espradio_task_yield_go();
    }

    /* Step 8a: Enable the BLE core's end-of-event interrupt.
     *
     * BLE +0x0c is the core interrupt-enable mask.  Tracing every write to it in
     * libbtdm_app shows each bit has exactly one owner:
     *   bit 0  wakeup/clkn     bit 9  10 ms timer    bit 12 software int
     *   bit 3  END OF EVENT    bit 10 half-slot      bit 19 CCA software int
     *   bit 7  crypt           bit 11 half-us timer
     * r_rwip_driver_init sets 0x1180 (bits 7, 8, 12) — note: NO bit 3.  The only
     * code that ever sets bit 3 is r_rwip_wakeup_end_hack (|= 0x1188), i.e. the
     * tail of a sleep->wake cycle, which also sets btdm_pwr_state = 4.
     *
     * Because we hold the controller permanently awake (sleep_mode = 0,
     * rw_sleep_enable = 0, prevent_sleep_set below), that wakeup tail never runs,
     * so bit 3 stayed masked.  The observable effect was precisely: the half-us
     * timer fires once, sch_arb_event_start_isr starts the scan event,
     * sch_prog_ble_push programs and kicks the radio — and then the completion
     * interrupt never arrives, so nothing ever reschedules the next scan window
     * and no advertising report is ever delivered.
     *
     * NOTE: setting bit 3 here was tried and had no effect, because
     * r_lld_core_init puts this core in IRQ-FIFO mode (INTCNTL = 0x640000|0x166)
     * where event status is delivered through the FIFO at +0x2d8 and these
     * classic per-source enables are not the mechanism. Left documented rather
     * than poked. btdm_pwr_state also never reaches 4 (stays 0) for the same
     * reason — the wakeup tail never runs. */
    BLE_DBG("  intcntl=0x%08lx pwr_state=%lu\n",
            (unsigned long)*(volatile uint32_t *)0x6003100Cu,
            (unsigned long)*(volatile uint32_t *)0x3fcdff18u);

    /* Step 8b: Disable modem sleep and set prevent_sleep flags. */
    extern void btdm_controller_enable_sleep(bool enable);
    btdm_controller_enable_sleep(false);
    extern void r_rwip_prevent_sleep_set(uint32_t prv_slp_bit);
    r_rwip_prevent_sleep_set(0x0F);
    BLE_DBG("  enable_sleep(false) + prevent_sleep done\n");

    /* Step 9: Register VHCI callbacks */
    res = API_vhci_host_register_callback(&s_vhci_cbs);
    if (res != 0) {
        BLE_DBG("  vhci_register FAILED: %d\n", res);
        return -3;
    }
    BLE_DBG("  vhci registered\n");

    BLE_DBG("espradio_ble_init: OK\n");
    return 0;
}
