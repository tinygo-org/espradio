//go:build esp32c3 || esp32s3

/* Controller start for the ESP32-C3 and the ESP32-S3. See bt_ble.h.
 * The two chips share this sequence. The classic ESP32 is different and is in
 * bt_ble_init_esp32.c. */

#include "espradio.h"
#include "esp_bt.h"
#include "bt_ble.h"
#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>

#ifndef ESPRADIO_BLE_DEBUG
#define ESPRADIO_BLE_DEBUG 0
#endif

#if ESPRADIO_BLE_DEBUG
#define BLE_DBG(...) printf(__VA_ARGS__)
#else
#define BLE_DBG(...) ((void)0)
#endif

/* ROM and PHY entry points that this sequence uses. */
extern void ets_delay_us(uint32_t us);
extern void ets_update_cpu_frequency(uint32_t ticks_per_us);
extern void esp_phy_modem_init(void);
extern void esp_wifi_bt_power_domain_on(void);
extern void phy_set_wifi_mode_only(bool wifi_only);
extern void btdm_controller_enable_sleep(bool enable);
extern void r_rwip_prevent_sleep_set(uint32_t prv_slp_bit);

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


void espradio_bt_chip_clocks_up(void) {
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
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(50);

    /* Assert then release only the BT bits, so a WiFi session that is already
     * running is left alone. */
    APB_CTRL_WIFI_RST_EN_REG |= BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
    ets_delay_us(10);
    APB_CTRL_WIFI_RST_EN_REG &= ~BT_RST_BITS;
    ESPRADIO_MEMORY_BARRIER();
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
    uint32_t ticks_per_us = espradio_bt_cpu_ticks_per_us();
    ets_update_cpu_frequency(ticks_per_us);
    BLE_DBG("  ets_update_cpu_frequency(%lu) done\n", (unsigned long)ticks_per_us);
}

int espradio_bt_chip_controller_bringup(void) {
    int res;
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
    cfg.hw_target_code = espradio_bt_hw_target_code();
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

    /* Step 4: Power domain + modem init (prerequisites for PHY) */
    extern void esp_wifi_bt_power_domain_on(void);
    esp_wifi_bt_power_domain_on();
    extern void esp_phy_modem_init(void);
    esp_phy_modem_init();
    BLE_DBG("  power domain + modem init done\n");

    /* Step 4c: Disable sleep at ROM level BEFORE controller_init.
     * The task starts during init and calls rw_schedule → r_rwip_sleep.
     * With rw_sleep_enable=0, the sleep path is skipped. */
    rw_sleep_enable = 0;

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
    esp_phy_enable(PHY_MODEM_BT);
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
    BLE_DBG("  pwr_state=%lu\n", (unsigned long)btdm_pwr_state);

    /* Step 8b: Disable modem sleep and set prevent_sleep flags. */
    extern void btdm_controller_enable_sleep(bool enable);
    btdm_controller_enable_sleep(false);
    extern void r_rwip_prevent_sleep_set(uint32_t prv_slp_bit);
    r_rwip_prevent_sleep_set(0x0F);
    BLE_DBG("  enable_sleep(false) + prevent_sleep done\n");

    return 0;
}

/* Wake the controller task.
 *
 * btdm_controller_task does not block on g_rw_schd_queue. It blocks on a
 * semaphore and drains the queue without blocking, thus an item pushed into
 * the queue never wakes it. Giving the semaphore releases it, and even with an
 * empty queue that reaches btdm_rw_run() and rw_schedule(), which reprograms
 * the scheduler after an event.
 *
 * btdm_ol_task_env->sem is at offset 8. Use the blob accessor for the env
 * rather than a fixed address. */
extern void *r_btdm_vnd_ol_task_env_get(void);
extern int32_t espradio_semphr_give(void *semphr);

int espradio_bt_chip_wake_task(void) {
    void *env = r_btdm_vnd_ol_task_env_get();
    if (env == NULL) {
        return 0;
    }
    void *sem = *(void *volatile *)((uint8_t *)env + 8);
    if (sem == NULL) {
        return 0;
    }
    espradio_semphr_give(sem);
    return 1;
}

extern void r_ke_task_schedule(void);

void espradio_bt_chip_ke_task_schedule(void) {
    r_ke_task_schedule();
}
