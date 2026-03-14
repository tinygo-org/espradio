//go:build esp32c3

#include "sdkconfig.h"
#include "include.h"

#ifndef ESPRADIO_PHY_DEBUG
#define ESPRADIO_PHY_DEBUG 0
#endif

#if ESPRADIO_PHY_DEBUG
#define PHY_DBG(...) printf(__VA_ARGS__)
#else
#define PHY_DBG(...) ((void)0)
#endif

extern void *g_phyFuns;                 /* libphy.a: ROM function table for PHY (used by set_chanfreq) */
extern uint32_t rom_chip_i2c_readReg(uint32_t block, uint32_t host_id, uint32_t reg_add);
extern void rom_chip_i2c_writeReg(uint32_t block, uint32_t host_id, uint32_t reg_add, uint32_t data);
extern uint32_t rom_i2c_readReg_Mask(uint32_t block, uint32_t host_id, uint32_t reg_add, uint32_t msb, uint32_t lsb);
extern void espradio_pll_trace_i2c(const char *op, uint32_t block, uint32_t host, uint32_t reg,
                                   uint32_t value, uint32_t aux0, uint32_t aux1);
extern void espradio_pll_note_write(uint32_t block, uint32_t host, uint32_t reg);
extern void espradio_pll_note_read(uint32_t block, uint32_t host, uint32_t reg,
                                   uint32_t msb, uint32_t lsb, uint32_t value);
extern void espradio_pll_trace_readback(const char *op, uint32_t block, uint32_t host, uint32_t reg, uint32_t expected);
extern void espradio_panic(char *s);

#define G_PHYFUNS_MAX_OFFSET 0x288

#ifndef ESPRADIO_PHY_VERIFY_WRITE
#define ESPRADIO_PHY_VERIFY_WRITE 1
#endif
#define G_PHYFUNS_NUM_SLOTS  ((G_PHYFUNS_MAX_OFFSET / 4) + 1)

#ifndef ESPRADIO_PLL_FORCE_CAL_READY_AFTER
#define ESPRADIO_PLL_FORCE_CAL_READY_AFTER 0u
#endif

static volatile uint32_t s_phy_stub_calls;
static volatile uint32_t s_phy_rf_trace_calls;
static volatile uint32_t s_phy_hook_trace_enabled = 1u;
static volatile uint32_t s_phy_pll_cal_reads;
static volatile uint32_t s_phy_1ac_calls;
static volatile uint32_t s_phy_1b4_calls;
static volatile uint32_t s_phy_1bc_calls;
static volatile uint32_t s_phy_1b8_calls;
static uint32_t s_phy_1ac_last_a0 = 0xffffffffu;
static uint32_t s_phy_1ac_last_a1 = 0xffffffffu;
static uint32_t s_phy_1ac_last_a2 = 0xffffffffu;
static uint32_t s_phy_1ac_repeat;

static inline uint32_t phy_fix_host(uint32_t block, uint32_t host) {
    if (host == 0u && (block == 0x66u || block == 0x6bu)) {
        return 1u;
    }
    return host;
}

#if ESPRADIO_PHY_VERIFY_WRITE
static void phy_verify_write_full(uint32_t block, uint32_t host, uint32_t reg, uint32_t expected,
                                  const char *op) {
    uint32_t got = rom_chip_i2c_readReg(block, host, reg) & 0xffu;
    uint32_t exp = expected & 0xffu;
    if (got != exp) {
        printf("espradio: phy write verify fail %s blk=0x%lx h=%lu reg=%lu exp=0x%02lx got=0x%02lx\n",
               op, (unsigned long)block, (unsigned long)host, (unsigned long)reg,
               (unsigned long)exp, (unsigned long)got);
        espradio_panic("phy write verify fail");
    }
}
static void phy_verify_write_mask(uint32_t block, uint32_t host, uint32_t reg,
                                  uint32_t mask, uint32_t expected_bits, const char *op) {
    uint32_t got = rom_chip_i2c_readReg(block, host, reg) & 0xffu;
    if ((got & mask) != (expected_bits & mask)) {
        printf("espradio: phy write verify fail %s blk=0x%lx h=%lu reg=%lu mask=0x%02lx exp=0x%02lx got=0x%02lx\n",
               op, (unsigned long)block, (unsigned long)host, (unsigned long)reg,
               (unsigned long)mask, (unsigned long)(expected_bits & mask), (unsigned long)(got & mask));
        espradio_panic("phy write verify fail");
    }
}
#endif

typedef uint32_t (*phy_fn6_t)(uint32_t, uint32_t, uint32_t, uint32_t, uint32_t, uint32_t);
typedef void (*phy_fn0_t)(void);
typedef uint32_t (*phy_fn3_t)(uint32_t, uint32_t, uint32_t);
static phy_fn6_t s_rom_phy_1b4;
static phy_fn6_t s_rom_phy_1b8;
static phy_fn6_t s_rom_phy_1bc;
static phy_fn3_t s_rom_phy_1ac;
static phy_fn0_t s_rom_phy_228;

static uint32_t phy_rom_stub_noop(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    (void)a0;
    (void)a1;
    (void)a2;
    (void)a3;
    return 0;
}

static uint32_t phy_rom_stub_trace(const char *slot, uint32_t limit,
                                   uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    uint32_t n = s_phy_stub_calls++;
    if (s_phy_hook_trace_enabled && n < limit) {
        PHY_DBG("espradio: g_phyFuns[%s] a0=%lu a1=%lu a2=%lu a3=%lu\n",
                slot,
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2, (unsigned long)a3);
    }
    return 0;
}

static uint32_t phy_rom_stub_rf_write(const char *slot,
                                      uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3,
                                      uint32_t a4, uint32_t a5) {
    uint32_t n = s_phy_rf_trace_calls++;
    if (s_phy_hook_trace_enabled && n < 24u) {
        PHY_DBG("espradio: g_phyFuns[%s] early-write a0=%lu a1=%lu a2=%lu a3=%lu a4=%lu a5=%lu\n",
                slot,
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2, (unsigned long)a3,
                (unsigned long)a4, (unsigned long)a5);
    } else if (s_phy_hook_trace_enabled && n < 96 && (a0 == 0x62u || a0 == 0x63u) && a1 == 1u) {
        PHY_DBG("espradio: g_phyFuns[%s] write a0=%lu a1=%lu a2=%lu a3=%lu a4=%lu a5=%lu\n",
                slot,
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2, (unsigned long)a3,
                (unsigned long)a4, (unsigned long)a5);
    }
    return 0;
}

static uint32_t phy_rom_stub_trace_0x64(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x64", 96, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x68(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x68", 96, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x6c(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x6c", 96, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x100(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x100", 128, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x104(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x104", 128, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x108(uint32_t a0, uint32_t a1, uint32_t a2, uint32_t a3) {
    return phy_rom_stub_trace("0x108", 128, a0, a1, a2, a3);
}

static uint32_t phy_rom_stub_trace_0x1ac(uint32_t a0, uint32_t a1, uint32_t a2) {
    uint32_t call = ++s_phy_1ac_calls;
    uint32_t same = (a0 == s_phy_1ac_last_a0 && a1 == s_phy_1ac_last_a1 && a2 == s_phy_1ac_last_a2) ? 1u : 0u;
    if (same) {
        s_phy_1ac_repeat++;
    } else {
        s_phy_1ac_repeat = 0u;
        s_phy_1ac_last_a0 = a0;
        s_phy_1ac_last_a1 = a1;
        s_phy_1ac_last_a2 = a2;
    }
    if (s_phy_hook_trace_enabled) {
        if (call <= 48u || same == 0u || ((s_phy_1ac_repeat & 63u) == 0u)) {
            PHY_DBG("espradio: g_phyFuns[0x1ac] n=%lu rep=%lu a0=%lu a1=%lu a2=%lu\n",
                    (unsigned long)call, (unsigned long)s_phy_1ac_repeat,
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)a2);
        }
    }
    uint32_t ret = 0;
    uint32_t host = phy_fix_host(a0, a1);
    if (s_rom_phy_1ac) {
        ret = s_rom_phy_1ac(a0, host, a2);
        if (s_phy_hook_trace_enabled && host != a1 && call <= 128u) {
            PHY_DBG("espradio: g_phyFuns[0x1ac] host-remap a0=%lu h=%lu->%lu a2=%lu ret=0x%02lx\n",
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)host, (unsigned long)a2,
                    (unsigned long)(ret & 0xffu));
        }
    } else {
        ret = rom_chip_i2c_readReg(a0, host, a2) & 0xffu;
    }
    if (s_phy_hook_trace_enabled && call <= 16u &&
        (a0 == 102u || a0 == 106u || a0 == 97u || a0 == 103u)) {
        PHY_DBG("espradio: g_phyFuns[0x1ac] ret-early n=%lu a0=%lu a1=%lu a2=%lu ret=0x%02lx\n",
                (unsigned long)call,
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2,
                (unsigned long)(ret & 0xffu));
    }
    if (s_phy_hook_trace_enabled && a0 == 0x62u && a1 == 1u &&
        (a2 == 0x0cu || a2 == 7u || a2 == 5u || a2 == 0x0bu)) {
        uint32_t sel = (ret >> 2) & 3u;
        PHY_DBG("espradio: g_phyFuns[0x1ac] ret a0=%lu a1=%lu a2=%lu ret=0x%02lx sel=%lu\n",
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2,
                (unsigned long)(ret & 0xffu), (unsigned long)sel);
    }
    return ret;
}

static void phy_rom_stub_trace_0x228(void) {
    PHY_DBG("espradio: g_phyFuns[0x228] call\n");
    if (s_rom_phy_228) {
        s_rom_phy_228();
    }
}

static uint32_t phy_rom_stub_trace_0x1b4(uint32_t a0, uint32_t a1, uint32_t a2,
                                         uint32_t a3, uint32_t a4, uint32_t a5) {
    uint32_t c = ++s_phy_1b4_calls;
    (void)phy_rom_stub_rf_write("0x1b4", a0, a1, a2, a3, a4, a5);
    uint32_t host = phy_fix_host(a0, a1);
    espradio_pll_note_write(a0, host, a2);
    espradio_pll_trace_i2c("w", a0, host, a2, a3, a4, a5);
    if (s_rom_phy_1b4) {
        uint32_t rc = s_rom_phy_1b4(a0, host, a2, a3, a4, a5);
#if ESPRADIO_PHY_VERIFY_WRITE
        phy_verify_write_full(a0, host, a2, a3, "0x1b4");
#endif
        if (s_phy_hook_trace_enabled && host != a1 && c <= 24u) {
            PHY_DBG("espradio: g_phyFuns[0x1b4] host-remap n=%lu a0=%lu h=%lu->%lu a2=%lu a3=%lu rc=0x%02lx\n",
                    (unsigned long)c,
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)host,
                    (unsigned long)a2, (unsigned long)a3, (unsigned long)(rc & 0xffu));
        }
        if (s_phy_hook_trace_enabled && c <= 24u) {
            PHY_DBG("espradio: g_phyFuns[0x1b4] early-ret n=%lu a0=%lu a1=%lu a2=%lu rc=0x%02lx\n",
                    (unsigned long)c,
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)a2,
                    (unsigned long)(rc & 0xffu));
        }
        espradio_pll_trace_readback("w", a0, a1, a2, a3);
        return rc;
    }
    rom_chip_i2c_writeReg(a0, host, a2, a3);
#if ESPRADIO_PHY_VERIFY_WRITE
    phy_verify_write_full(a0, host, a2, a3, "0x1b4");
#endif
    espradio_pll_trace_readback("w", a0, a1, a2, a3);
    return 0;
}

static uint32_t phy_rom_stub_trace_0x1bc(uint32_t a0, uint32_t a1, uint32_t a2,
                                         uint32_t a3, uint32_t a4, uint32_t a5) {
    uint32_t c = ++s_phy_1bc_calls;
    (void)phy_rom_stub_rf_write("0x1bc", a0, a1, a2, a3, a4, a5);
    uint32_t host = phy_fix_host(a0, a1);
    uint32_t msb = a3 & 7u;
    uint32_t lsb = a4 & 7u;
    if (msb < lsb) {
        uint32_t t = msb;
        msb = lsb;
        lsb = t;
    }
    uint32_t width = msb - lsb + 1u;
    uint32_t mask = (width >= 8u) ? 0xffu : ((1u << width) - 1u) << lsb;
    uint32_t expected_bits = (a5 << lsb) & mask;
    espradio_pll_note_write(a0, host, a2);
    espradio_pll_trace_i2c("wm", a0, host, a2, a5, msb, lsb);
    if (s_rom_phy_1bc) {
        uint32_t rc = s_rom_phy_1bc(a0, host, a2, a3, a4, a5);
#if ESPRADIO_PHY_VERIFY_WRITE
        phy_verify_write_mask(a0, host, a2, mask, expected_bits, "0x1bc");
#endif
        if (s_phy_hook_trace_enabled && host != a1 && c <= 24u) {
            PHY_DBG("espradio: g_phyFuns[0x1bc] host-remap n=%lu a0=%lu h=%lu->%lu a2=%lu rc=0x%02lx\n",
                    (unsigned long)c,
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)host, (unsigned long)a2,
                    (unsigned long)(rc & 0xffu));
        }
        if (s_phy_hook_trace_enabled && c <= 24u) {
            PHY_DBG("espradio: g_phyFuns[0x1bc] early-ret n=%lu a0=%lu a1=%lu a2=%lu rc=0x%02lx\n",
                    (unsigned long)c,
                    (unsigned long)a0, (unsigned long)a1, (unsigned long)a2,
                    (unsigned long)(rc & 0xffu));
        }
        {
            uint32_t full = rom_chip_i2c_readReg(a0, host, a2) & 0xffu;
            espradio_pll_trace_readback("wm", a0, host, a2, full);
        }
        return rc;
    }
    uint32_t cur = rom_chip_i2c_readReg(a0, host, a2) & 0xffu;
    uint32_t out = (cur & ~mask) | expected_bits;
    rom_chip_i2c_writeReg(a0, host, a2, out & 0xffu);
#if ESPRADIO_PHY_VERIFY_WRITE
    phy_verify_write_mask(a0, host, a2, mask, expected_bits, "0x1bc");
#endif
    espradio_pll_trace_readback("wm", a0, host, a2, out & 0xffu);
    return 0;
}

static uint32_t phy_rom_stub_trace_0x1b8(uint32_t a0, uint32_t a1, uint32_t a2,
                                         uint32_t a3, uint32_t a4, uint32_t a5) {
    uint32_t c = ++s_phy_1b8_calls;
    uint32_t n = s_phy_rf_trace_calls++;
    if (s_phy_hook_trace_enabled && n < 24u) {
        PHY_DBG("espradio: g_phyFuns[0x1b8] early-read a0=%lu a1=%lu a2=%lu a3=%lu a4=%lu a5=%lu\n",
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2, (unsigned long)a3,
                (unsigned long)a4, (unsigned long)a5);
    } else if (s_phy_hook_trace_enabled && n < 128 && a0 == 0x62u && a1 == 1u && a2 == 7u) {
        PHY_DBG("espradio: g_phyFuns[0x1b8] read  a0=%lu a1=%lu a2=%lu a3=%lu a4=%lu a5=%lu\n",
                (unsigned long)a0, (unsigned long)a1, (unsigned long)a2, (unsigned long)a3,
                (unsigned long)a4, (unsigned long)a5);
    }
    uint32_t host = phy_fix_host(a0, a1);
    uint32_t cur;
    if (s_rom_phy_1b8) {
        cur = s_rom_phy_1b8(a0, host, a2, a3, a4, a5) & 0xffu;
    } else {
        cur = rom_chip_i2c_readReg(a0, host, a2) & 0xffu;
    }
    uint32_t msb = a3 & 7u;
    uint32_t lsb = a4 & 7u;
    if (msb < lsb) {
        uint32_t t = msb;
        msb = lsb;
        lsb = t;
    }
    uint32_t width = msb - lsb + 1u;
    uint32_t mask = (width >= 8u) ? 0xffu : ((1u << width) - 1u) << lsb;
    uint32_t v_chip = (cur & mask) >> lsb;

    if (a0 == 0x62u && a1 == 1u && a2 == 7u && msb == 2u && lsb == 2u) {
        s_phy_pll_cal_reads++;
#if ESPRADIO_PLL_FORCE_CAL_READY_AFTER > 0u
        if (s_phy_pll_cal_reads >= ESPRADIO_PLL_FORCE_CAL_READY_AFTER) {
            if (s_phy_hook_trace_enabled) {
                PHY_DBG("espradio: force pll-cal ready on read=%lu\n",
                        (unsigned long)s_phy_pll_cal_reads);
            }
            return 1u;
        }
#endif
    }

    espradio_pll_note_read(a0, host, a2, msb, lsb, v_chip);
    espradio_pll_trace_i2c("r", a0, host, a2, v_chip, msb, lsb);
    if (s_phy_hook_trace_enabled && n < 128 && a0 == 0x62 && a1 == 1 && a2 == 7) {
        PHY_DBG("espradio: g_phyFuns[0x1b8] vals raw=0x%02lx chip=%lu\n",
                (unsigned long)cur, (unsigned long)v_chip);
    }
    if (s_phy_hook_trace_enabled && c <= 24u) {
        PHY_DBG("espradio: g_phyFuns[0x1b8] early-ret n=%lu a0=%lu a1=%lu->%lu a2=%lu v=0x%02lx\n",
                (unsigned long)c,
                (unsigned long)a0, (unsigned long)a1, (unsigned long)host, (unsigned long)a2,
                (unsigned long)(v_chip & 0xffu));
    }
    return v_chip;
}

static uint32_t phy_funs_stub_table[G_PHYFUNS_NUM_SLOTS];

void espradio_phy_funs_install(void) {
    for (int i = 0; i < G_PHYFUNS_NUM_SLOTS; i++) {
        phy_funs_stub_table[i] = (uint32_t)(uintptr_t)phy_rom_stub_noop;
    }
    phy_funs_stub_table[0x64 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x64;
    phy_funs_stub_table[0x68 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x68;
    phy_funs_stub_table[0x6c / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x6c;
    phy_funs_stub_table[0x100 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x100;
    phy_funs_stub_table[0x104 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x104;
    phy_funs_stub_table[0x108 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x108;
    phy_funs_stub_table[0x1b4 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1b4;
    phy_funs_stub_table[0x1b8 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1b8;
    phy_funs_stub_table[0x1bc / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1bc;
    g_phyFuns = (void *)phy_funs_stub_table;
    PHY_DBG("espradio: g_phyFuns install (%u slots), trace: 0x64 0x68 0x6c 0x100 0x104 0x108 0x1b4 0x1b8 0x1bc\n",
            (unsigned)G_PHYFUNS_NUM_SLOTS);
}

void espradio_phy_patch_romfuncs(void) {
    if (!g_phyFuns) {
        PHY_DBG("espradio: g_phyFuns patch skipped (null)\n");
        return;
    }
    uint32_t *tbl = (uint32_t *)g_phyFuns;
    s_rom_phy_1ac = (phy_fn3_t)(uintptr_t)tbl[0x1ac / 4];
    s_rom_phy_228 = (phy_fn0_t)(uintptr_t)tbl[0x228 / 4];
    s_rom_phy_1b4 = (phy_fn6_t)(uintptr_t)tbl[0x1b4 / 4];
    s_rom_phy_1b8 = (phy_fn6_t)(uintptr_t)tbl[0x1b8 / 4];
    s_rom_phy_1bc = (phy_fn6_t)(uintptr_t)tbl[0x1bc / 4];

    tbl[0x1ac / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1ac;
    tbl[0x228 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x228;
    tbl[0x1b4 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1b4;
    tbl[0x1b8 / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1b8;
    tbl[0x1bc / 4] = (uint32_t)(uintptr_t)phy_rom_stub_trace_0x1bc;

    PHY_DBG("espradio: g_phyFuns patched 0x1ac/0x228/0x1b4/0x1b8/0x1bc (orig=%p/%p/%p/%p/%p)\n",
            (void *)s_rom_phy_1ac, (void *)s_rom_phy_228,
            (void *)s_rom_phy_1b4, (void *)s_rom_phy_1b8, (void *)s_rom_phy_1bc);
}

void espradio_phy_hook_trace_set(uint32_t enabled) {
    s_phy_hook_trace_enabled = enabled ? 1u : 0u;
}
