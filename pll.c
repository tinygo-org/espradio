//go:build esp32c3

#include "sdkconfig.h"
#include "include.h"

extern void espradio_panic(char *s);
extern uint32_t rom_chip_i2c_readReg(uint32_t block, uint32_t host_id, uint32_t reg_add);
extern uint32_t rom_i2c_readReg_Mask(uint32_t block, uint32_t host_id, uint32_t reg_add, uint32_t msb, uint32_t lsb);
extern void rom_chip_i2c_writeReg(uint32_t block, uint32_t host_id, uint32_t reg_add, uint32_t data);

#ifndef ESPRADIO_PLL_DEBUG
#define ESPRADIO_PLL_DEBUG 0
#endif

#if ESPRADIO_PLL_DEBUG
#define PLL_DBG(...) PLL_DBG(__VA_ARGS__)
#else
#define PLL_DBG(...) ((void)0)
#endif

#ifndef ESPRADIO_PLL_TRAP_READ_N
#define ESPRADIO_PLL_TRAP_READ_N 0u
#endif

static volatile uint32_t s_phy_scan_trace_calls;
static volatile uint32_t s_phy_pll_reads;
static volatile uint32_t s_phy_pll_writes;
static volatile uint32_t s_phy_readback_logs;
static volatile uint32_t s_phy_key_write_logs;
static volatile uint32_t s_phy_pll_trace_enabled = 1u;
static uint32_t s_phy_last_pll_ready = 0xffffffffu;
static uint32_t s_phy_last_pll_cal = 0xffffffffu;

static int pll_trace_filter(uint32_t block, uint32_t host) {
    return (block == 0x62u || block == 0x63u) && host == 1u;
}

static int pll_need_readback(uint32_t block, uint32_t host, uint32_t reg) {
    if (block != 0x62u || host != 1u) {
        return 0;
    }
    return (reg == 0u || reg == 1u || reg == 2u || reg == 7u || reg == 11u);
}

void espradio_pll_trace_i2c(const char *op, uint32_t block, uint32_t host, uint32_t reg,
                            uint32_t value, uint32_t aux0, uint32_t aux1) {
    if (!pll_trace_filter(block, host)) {
        return;
    }
    if (op && op[0] == 'r') {
        if (!(block == 0x62u && host == 1u && reg == 7u)) {
            return;
        }
    } else {
        if (!(block == 0x62u && host == 1u && (reg == 0u || reg == 1u || reg == 2u || reg == 11u))) {
            return;
        }
        if (s_phy_key_write_logs++ >= 64u) {
            return;
        }
    }
    uint32_t n = s_phy_scan_trace_calls++;
    if (s_phy_pll_trace_enabled && n < 220u) {
        PLL_DBG("espradio: scan-i2c %s blk=0x%02lx reg=%lu val=%lu aux0=%lu aux1=%lu pll_r=%lu pll_w=%lu\n",
            op,
            (unsigned long)block,
            (unsigned long)reg,
            (unsigned long)value,
            (unsigned long)aux0,
            (unsigned long)aux1,
            (unsigned long)s_phy_pll_reads,
            (unsigned long)s_phy_pll_writes);
    }
}

void espradio_pll_note_write(uint32_t block, uint32_t host, uint32_t reg) {
    if (block == 0x62u && host == 1u && reg == 7u) {
        s_phy_pll_writes++;
    }
}

void espradio_pll_note_read(uint32_t block, uint32_t host, uint32_t reg,
                            uint32_t msb, uint32_t lsb, uint32_t value) {
    if (block == 0x62u && host == 1u && reg == 7u) {
        s_phy_pll_reads++;
#if ESPRADIO_PLL_TRAP_READ_N > 0u
        if (s_phy_pll_reads == ESPRADIO_PLL_TRAP_READ_N) {
            PLL_DBG("espradio: pll trap on read #%lu msb=%lu lsb=%lu val=%lu\n",
                   (unsigned long)s_phy_pll_reads,
                   (unsigned long)msb,
                   (unsigned long)lsb,
                   (unsigned long)value);
            espradio_panic("espradio: pll trap read");
        }
#endif
        if (msb == 1u && lsb == 1u && s_phy_last_pll_ready != value) {
            s_phy_last_pll_ready = value;
            if (s_phy_pll_trace_enabled) {
                PLL_DBG("espradio: pll-ready bit changed -> %lu (reads=%lu writes=%lu)\n",
                       (unsigned long)value,
                       (unsigned long)s_phy_pll_reads,
                       (unsigned long)s_phy_pll_writes);
            }
        }
        if (msb == 2u && lsb == 2u && s_phy_last_pll_cal != value) {
            s_phy_last_pll_cal = value;
            if (s_phy_pll_trace_enabled) {
                PLL_DBG("espradio: pll-cal bit changed -> %lu (reads=%lu writes=%lu)\n",
                       (unsigned long)value,
                       (unsigned long)s_phy_pll_reads,
                       (unsigned long)s_phy_pll_writes);
            }
        }
    }
}

void espradio_pll_trace_set_enabled(uint32_t enabled) {
    s_phy_pll_trace_enabled = enabled ? 1u : 0u;
}

void espradio_pll_trace_readback(const char *op, uint32_t block, uint32_t host, uint32_t reg, uint32_t expected) {
    if (!pll_need_readback(block, host, reg)) {
        return;
    }
    s_phy_readback_logs++;
    uint32_t actual = rom_chip_i2c_readReg(block, host, reg) & 0xffu;
    int transient_ok = 0;
    if (block == 0x62u && host == 1u && reg == 0u) {
        uint32_t pulse_mask = 0x60u;
        if ((expected & pulse_mask) != 0u && (actual & pulse_mask) == 0u) {
            transient_ok = 1;
        }
    }
    if (block == 0x62u && host == 1u && reg == 11u) {
        uint32_t pulse_mask = 0x40u;
        if ((expected & pulse_mask) != 0u && (actual & pulse_mask) == 0u) {
            transient_ok = 1;
        }
    }
    if (transient_ok) {
        return;
    }
    if (actual != (expected & 0xffu)) {
        uint32_t m = rom_i2c_readReg_Mask(block, host, reg, 7u, 0u) & 0xffu;
        PLL_DBG("espradio: scan-rb mismatch %s blk=0x%02lx reg=%lu expected=0x%02lx actual=0x%02lx\n",
               op,
               (unsigned long)block,
               (unsigned long)reg,
               (unsigned long)expected,
               (unsigned long)actual);
        PLL_DBG("espradio: scan-rb mismatch detail blk=0x%02lx reg=%lu mask_read=0x%02lx\n",
               (unsigned long)block,
               (unsigned long)reg,
               (unsigned long)m);
        espradio_panic("espradio: phy: i2c readback mismatch");
    }
}

void espradio_test_pll(void) {
    const uint32_t blk = 0x62u;
    const uint32_t host = 1u;
    uint32_t r7_before = rom_chip_i2c_readReg(blk, host, 7u) & 0xffu;
    uint32_t r1_before = rom_chip_i2c_readReg(blk, host, 1u) & 0xffu;
    uint32_t r2_before = rom_chip_i2c_readReg(blk, host, 2u) & 0xffu;

    PLL_DBG("espradio: test_pll begin r7=0x%02lx r1=0x%02lx r2=0x%02lx\n",
           (unsigned long)r7_before,
           (unsigned long)r1_before,
           (unsigned long)r2_before);

    rom_chip_i2c_writeReg(blk, host, 1u, 0xffu);
    rom_chip_i2c_writeReg(blk, host, 2u, 0xf0u);

    uint32_t r1_after = rom_chip_i2c_readReg(blk, host, 1u) & 0xffu;
    uint32_t r2_after = rom_chip_i2c_readReg(blk, host, 2u) & 0xffu;
    uint32_t r7_after = rom_chip_i2c_readReg(blk, host, 7u) & 0xffu;
    uint32_t r7_bit1 = rom_i2c_readReg_Mask(blk, host, 7u, 1u, 1u) & 0xffu;

    PLL_DBG("espradio: test_pll after w r1=0x%02lx r2=0x%02lx r7=0x%02lx r7b1=%lu\n",
           (unsigned long)r1_after,
           (unsigned long)r2_after,
           (unsigned long)r7_after,
           (unsigned long)r7_bit1);
}
