//go:build esp32c3

#include "sdkconfig.h"
#include "include.h"
#include <stddef.h>
#include <stdio.h>
#include <string.h>

#ifndef ESPRADIO_PHY_PROBE_DEBUG
#define ESPRADIO_PHY_PROBE_DEBUG 0
#endif

#if ESPRADIO_PHY_PROBE_DEBUG
#define PHY_PROBE_DBG(...) PHY_PROBE_DBG(__VA_ARGS__)
#else
#define PHY_PROBE_DBG(...) ((void)0)
#endif

extern void *g_phyFuns;
extern void espradio_panic(char *s);

extern void phy_get_romfunc_addr(void);
extern void rom1_i2c_master_reset(void);
extern void register_chipv7_phy_init_param(uint8_t *param_1);
extern uint32_t phy_get_rf_cal_version(void);
extern uint32_t phy_rfcal_data_check(uint32_t is_init, void *cal_data, void *init_data, uint32_t rf_cal_version);
extern void rf_cal_data_recovery(void *cal_data);
extern int phy_rfcal_data_check_value(void *cal_data, void *buf, uint32_t is_init);
extern void rf_init(void);
extern void bb_init(void);
extern void get_temp_init(uint32_t a, uint32_t b);
extern void rf_cal_data_backup(void *cal_data);
extern void rom_phy_bbpll_cal(uint32_t a0);
extern void espradio_phy_hook_trace_set(uint32_t enabled);
extern void espradio_pll_trace_set_enabled(uint32_t enabled);

#ifndef ESPRADIO_PHY_PROBE_PHASE_STOP
#define ESPRADIO_PHY_PROBE_PHASE_STOP 0u
#endif

typedef void (*phy_fn0_t)(void);
typedef void (*phy_fn3_t)(uint32_t, uint32_t, uint32_t);

static void espradio_probe_phase(uint32_t phase, const char *name) {
    PHY_PROBE_DBG("espradio: phy-probe phase=%lu %s\n", (unsigned long)phase, name);
#if ESPRADIO_PHY_PROBE_PHASE_STOP > 0u
    if (phase == ESPRADIO_PHY_PROBE_PHASE_STOP) {
        espradio_panic("espradio: phy-probe stop");
    }
#endif
}

static uint8_t *espradio_probe_init_data_default(uint8_t *buf, size_t n) {
    if (n < 16u) {
        return buf;
    }
    memset(buf, 0, n);
    buf[0] = 2u;
    buf[2] = 0x52u;
    buf[3] = 0x52u;
    buf[4] = 0x50u;
    buf[5] = 0x4cu;
    buf[6] = 0x4cu;
    buf[7] = 0x48u;
    buf[8] = 0x4cu;
    buf[9] = 0x48u;
    buf[10] = 0x48u;
    buf[11] = 0x46u;
    buf[12] = 0x4au;
    buf[13] = 0x46u;
    buf[14] = 0x46u;
    buf[15] = 0x44u;
    return buf;
}

int espradio_register_chipv7_phy_probe(const esp_phy_init_data_t *init_data,
                                       esp_phy_calibration_data_t *cal_data,
                                       esp_phy_calibration_mode_t cal_mode) {
    (void)cal_mode;
    uint8_t local_init[0x80];
    uint8_t check_buf[96];
    uint8_t *use_init = (uint8_t *)init_data;
    if (use_init == NULL) {
        use_init = espradio_probe_init_data_default(local_init, sizeof(local_init));
    }

    espradio_probe_phase(1u, "phy_get_romfunc_addr");
    phy_get_romfunc_addr();
    PHY_PROBE_DBG("espradio: phy-probe g_phyFuns=%p\n", g_phyFuns);

    espradio_probe_phase(2u, "g_phyFuns+0x228");
    if (g_phyFuns != NULL) {
        uint32_t *tbl = (uint32_t *)g_phyFuns;
        phy_fn0_t f = (phy_fn0_t)(uintptr_t)tbl[0x228u / 4u];
        if (f != NULL) {
            f();
        }
    }

    espradio_probe_phase(3u, "rom1_i2c_master_reset");
    rom1_i2c_master_reset();

    espradio_probe_phase(4u, "register_chipv7_phy_init_param");
    register_chipv7_phy_init_param(use_init);

    espradio_probe_phase(5u, "phy_get_rf_cal_version");
    uint32_t ver = phy_get_rf_cal_version();
    PHY_PROBE_DBG("espradio: phy-probe rf_cal_version=%lu\n", (unsigned long)ver);

    espradio_probe_phase(6u, "phy_rfcal_data_check init");
    uint32_t chk = phy_rfcal_data_check(1u, (void *)cal_data, (void *)use_init, ver);
    PHY_PROBE_DBG("espradio: phy-probe rfcal_check_init=%lu\n", (unsigned long)chk);
    if (chk == 0u && cal_data != NULL) {
        rf_cal_data_recovery((void *)cal_data);
    }

    espradio_probe_phase(7u, "phy_rfcal_data_check_value init");
    (void)phy_rfcal_data_check_value((void *)cal_data, check_buf, 0u);

    espradio_probe_phase(8u, "rf_init trace-on");
    espradio_phy_hook_trace_set(1u);
    espradio_pll_trace_set_enabled(0u);
    espradio_probe_phase(9u, "rf_init");
    rf_init();

    espradio_probe_phase(10u, "bb_init trace-on");
    espradio_phy_hook_trace_set(1u);
    espradio_pll_trace_set_enabled(1u);

    espradio_probe_phase(11u, "bb_init");
    bb_init();

    espradio_probe_phase(12u, "bb_init done");

    espradio_probe_phase(13u, "get_temp_init");
    get_temp_init(1u, 1u);

    espradio_probe_phase(14u, "rf_cal_data_backup");
    rf_cal_data_backup((void *)cal_data);

    espradio_probe_phase(15u, "phy_rfcal_data_check post");
    (void)phy_rfcal_data_check(0u, (void *)cal_data, (void *)use_init, ver);

    espradio_probe_phase(16u, "rom_phy_bbpll_cal");
    rom_phy_bbpll_cal(0u);

    espradio_probe_phase(17u, "g_phyFuns+0x1ac");
    if (g_phyFuns != NULL) {
        uint32_t *tbl = (uint32_t *)g_phyFuns;
        phy_fn3_t f = (phy_fn3_t)(uintptr_t)tbl[0x1acu / 4u];
        if (f != NULL) {
            f(99u, 1u, 0u);
        }
    }

    espradio_probe_phase(18u, "done");
    return (int)chk;
}
