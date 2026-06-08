#include "sdkconfig.h"
#include "espradio.h"
#include "soc/interrupts.h"
#include <string.h>
#include <stddef.h>

#ifndef ESPRADIO_RADIO_DEBUG
#define ESPRADIO_RADIO_DEBUG 0
#endif

#if ESPRADIO_RADIO_DEBUG
#define RADIO_DBG(...) printf(__VA_ARGS__)
#else
#define RADIO_DBG(...) ((void)0)
#endif

/* phy_get_romfunc_addr() from libphy.a would set g_phyFuns via ROM; in our build it leads to
 * an error/crash, so we never call it and always use the stub table below. */

/*
 * Typical IDF flow (esp_wifi_init in wifi_init.c):
 *   1. esp_wifi_set_sleep_* / esp_wifi_set_keep_alive_time — power management
 *   2. esp_wifi_set_log_level()           — in Go before init (g_log_level)
 *   3. esp_wifi_bt_power_domain_on()      — power domain
 *   4. esp_wifi_init_internal(config)     — see below
 *   5. esp_phy_modem_init()               — not in blobs
 *   6. esp_supplicant_init()              — called (WPA2-PSK)
 *   7. wifi_init_completed()
 *
 * esp_wifi_init_internal (implemented in libnet80211.a, no sources):
 *   According to esp_private/wifi.h: allocates resources for the driver — control structure,
 *   RX/TX buffers, WiFi NVS, etc. Must be called before any other WiFi API. Internally (from logs)
 *   it creates a queue, semaphores, and starts the wifi driver task (worker) that processes
 *   cmd 6 (set_log?), cmd 15 (init step). Return: ESP_OK (0) or an error code; the blob sometimes
 *   returns a DRAM address (0x3FC8xxxx) — in Go we treat that as success (isPointerLike).
 */
extern void wifi_init_completed(void);  /* libnet80211.a */
extern uint32_t g_log_level;            /* libnet80211.a: blob log level (0=none..5=verbose), wifi_log checks param_3 <= g_log_level */
extern char gChmCxt[252];               /* libnet80211.a: channel manager context */
extern void phy_get_romfunc_addr(void);
extern void *g_phyFuns;
extern wifi_osi_funcs_t *g_osi_funcs_p;
/* Stub for the WIFI_INIT_CONFIG_DEFAULT() macro; also kept filled as fallback. */
wifi_osi_funcs_t g_wifi_osi_funcs = {0};

/* Heap-allocated copy of the OSI table. Placed well away from BSS/DATA to avoid
 * WiFi DMA corruption (PMP confirmed the zeroing bypasses the CPU). */
wifi_osi_funcs_t *s_heap_osi_funcs;

extern wifi_osi_funcs_t espradio_osi_funcs;

#ifndef ESPRADIO_PHY_PATCH_ROMFUNCS
#define ESPRADIO_PHY_PATCH_ROMFUNCS 0
#endif

void espradio_set_blob_log_level(uint32_t level) {
    g_log_level = level;
}

uint32_t espradio_wifi_boot_state(void) {
    uint32_t state = 0;
    if (g_osi_funcs_p == s_heap_osi_funcs || g_osi_funcs_p == &g_wifi_osi_funcs) {
        state |= 1u;
    }
    if (g_phyFuns != NULL) {
        state |= 2u;
    }
    return state;
}

/* g_wifi_default_wpa_crypto_funcs is provided by libwpa_supplicant.a
 * (crypto_ops.c.obj) with real implementations of HMAC-SHA256, PBKDF2,
 * AES-128, OMAC1, CCMP, AES-GMAC, SHA-256.  Do NOT define it here —
 * overriding with a zeroed struct leaves the crypto function pointers
 * NULL and crashes on WPA2 GTK rekey. */

/* One-time ROM hook setup: route ets_printf/esp_rom_printf to UART
 * and install a global lock based on ROM interrupt lock/unlock.
 * This mirrors what ESP-IDF does very early in startup. */
static int s_rom_hooks_inited;
static int s_bt_irq_wired;

static void espradio_bt_irq_stub(void *arg) {
    (void)arg;
}

static void espradio_bt_irq_prewire(void) {
    if (s_bt_irq_wired) {
        return;
    }
    s_bt_irq_wired = 1;

    enum {
        ESPRADIO_BTBB_INUM = 28,
        ESPRADIO_RWBT_INUM = 29,
        ESPRADIO_RWBLE_INUM = 30,
    };

    intr_matrix_set(0u, ETS_BT_BB_INTR_SOURCE, ESPRADIO_BTBB_INUM);
    intr_matrix_set(0u, ETS_RWBT_INTR_SOURCE, ESPRADIO_RWBT_INUM);
    intr_matrix_set(0u, ETS_RWBLE_INTR_SOURCE, ESPRADIO_RWBLE_INUM);

    ets_isr_attach(ESPRADIO_BTBB_INUM, espradio_bt_irq_stub, NULL);
    ets_isr_attach(ESPRADIO_RWBT_INUM, espradio_bt_irq_stub, NULL);
    ets_isr_attach(ESPRADIO_RWBLE_INUM, espradio_bt_irq_stub, NULL);

    ets_isr_unmask((1u << ESPRADIO_BTBB_INUM) | (1u << ESPRADIO_RWBT_INUM) | (1u << ESPRADIO_RWBLE_INUM));
}

void espradio_rom_hooks_init(void) {
    if (s_rom_hooks_inited) {
        return;
    }
    s_rom_hooks_inited = 1;

    /* Enable ROM printf via UART and install global lock callbacks. */
    ets_install_uart_printf();
    ets_install_lock(ets_intr_lock, ets_intr_unlock);
}

/* Disable ALL watchdog timers so that if the CPU hangs or faults, we get
 * an exception vector (captured in RTC STORE) instead of a Super WDT
 * full-SoC reset that clears the RTC domain.  Each WDT requires unlocking
 * its write-protect register first.
 *
 * Also disable the clock glitch detector and power glitch detector.
 * The WiFi radio TX causes EMI/power noise that trips these detectors,
 * resulting in a GLITCH_RTC_RESET (reset_reason=19) that wipes the
 * entire RTC domain.  ESP-IDF disables these during normal startup. */
static void espradio_disable_all_wdt(void) {
#ifdef __XTENSA__
    /* --- Clock glitch detector --- */
    /* RTC_CNTL_ANA_CONF_REG = 0x60008034, bit 20 = GLITCH_RST_EN */
    *(volatile uint32_t *)0x60008034 &= ~(1u << 20);        /* GLITCH_RST_EN=0 */

    /* --- FIB_SEL: override eFuse with register values --- */
    /* RTC_CNTL_FIB_SEL_REG = 0x60008148, bits[2:0]:
     *   When bit=1, eFuse value overrides register → can't disable via register
     *   When bit=0, register value is used → our GLITCH_RST_EN=0 takes effect
     * Default=0x7 (all eFuse), ESP-IDF sets 0x1.  Set 0x0 to force all to register. */
    *(volatile uint32_t *)0x60008148 = 0x0;

    /* --- Power glitch detector --- */
    /* RTC_CNTL_PG_CTRL_REG = 0x60008144, bit 31 = POWER_GLITCH_EN */
    *(volatile uint32_t *)0x60008144 &= ~(1u << 31);        /* POWER_GLITCH_EN=0 */

    /* --- Brownout detector --- */
    /* RTC_CNTL_BROWN_OUT_REG = 0x600080E8, bit 30 = ENA, bit 26 = RST_ENA */
    *(volatile uint32_t *)0x600080E8 &= ~((1u << 30) | (1u << 26));  /* disable BOD + BOD reset */

    /* --- Super WDT (SWD) --- */
    /* RTC_CNTL_SWD_WPROTECT_REG = 0x600080B8, key = 0x8F1D312A */
    /* RTC_CNTL_SWD_CONF_REG     = 0x600080B4, bit 30 = SWD_DISABLE */
    *(volatile uint32_t *)0x600080B8 = 0x8F1D312A;          /* unlock */
    *(volatile uint32_t *)0x600080B4 |= (1u << 30);         /* SWD_DISABLE=1 */
    *(volatile uint32_t *)0x600080B8 = 0;                   /* re-lock */

    /* --- RTC WDT --- */
    /* RTC_CNTL_WDTWPROTECT_REG = 0x600080B0, key = 0x50D83AA1 */
    /* RTC_CNTL_WDTCONFIG0_REG  = 0x60008098, bit 31 = WDT_EN */
    *(volatile uint32_t *)0x600080B0 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x60008098 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x600080B0 = 0;                   /* re-lock */

    /* --- Timer Group 0 MWDT --- */
    /* TIMG_WDTWPROTECT_REG(0) = 0x6001F064, key = 0x50D83AA1 */
    /* TIMG_WDTCONFIG0_REG(0)  = 0x6001F048, bit 31 = WDT_EN */
    *(volatile uint32_t *)0x6001F064 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x6001F048 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x6001F064 = 0;                   /* re-lock */

    /* --- Timer Group 1 MWDT --- */
    /* TIMG_WDTWPROTECT_REG(1) = 0x60020064, key = 0x50D83AA1 */
    /* TIMG_WDTCONFIG0_REG(1)  = 0x60020048, bit 31 = WDT_EN */
    *(volatile uint32_t *)0x60020064 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x60020048 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x60020064 = 0;                   /* re-lock */

#else /* RISC-V (ESP32-C3) — different RTC_CNTL offsets, same TIMG offsets */

    /* --- Clock glitch detector --- */
    *(volatile uint32_t *)0x60008034 &= ~(1u << 20);        /* GLITCH_RST_EN=0 */
    /* RTC_CNTL_FIB_SEL_REG = 0x6000810C */
    *(volatile uint32_t *)0x6000810C = 0x0;                 /* force register values */

    /* --- Power glitch detector --- */
    /* RTC_CNTL_PG_CTRL_REG = 0x60008124 */
    *(volatile uint32_t *)0x60008124 &= ~(1u << 31);        /* POWER_GLITCH_EN=0 */

    /* --- Brownout detector --- */
    /* RTC_CNTL_BROWN_OUT_REG = 0x600080D8 */
    *(volatile uint32_t *)0x600080D8 &= ~((1u << 30) | (1u << 26));

    /* --- Super WDT (SWD) --- */
    /* RTC_CNTL_SWD_WPROTECT_REG = 0x600080B0, key = 0x8F1D312A */
    /* RTC_CNTL_SWD_CONF_REG     = 0x600080AC, bit 30 = SWD_DISABLE */
    *(volatile uint32_t *)0x600080B0 = 0x8F1D312A;          /* unlock */
    *(volatile uint32_t *)0x600080AC |= (1u << 30);         /* SWD_DISABLE=1 */
    *(volatile uint32_t *)0x600080B0 = 0;                   /* re-lock */

    /* --- RTC WDT --- */
    /* RTC_CNTL_WDTWPROTECT_REG = 0x600080A8, key = 0x50D83AA1 */
    /* RTC_CNTL_WDTCONFIG0_REG  = 0x60008090, bit 31 = WDT_EN */
    *(volatile uint32_t *)0x600080A8 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x60008090 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x600080A8 = 0;                   /* re-lock */

    /* --- Timer Group 0 MWDT --- */
    *(volatile uint32_t *)0x6001F064 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x6001F048 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x6001F064 = 0;                   /* re-lock */

    /* --- Timer Group 1 MWDT --- */
    *(volatile uint32_t *)0x60020064 = 0x50D83AA1;          /* unlock */
    *(volatile uint32_t *)0x60020048 &= ~(1u << 31);        /* WDT_EN=0 */
    *(volatile uint32_t *)0x60020064 = 0;                   /* re-lock */

#endif
}

esp_err_t espradio_wifi_init(void) {
    espradio_rom_hooks_init();

    espradio_disable_all_wdt();

    RADIO_DBG("espradio: before esp_wifi_bt_power_domain_on\n");
    esp_wifi_bt_power_domain_on();
    RADIO_DBG("espradio: after esp_wifi_bt_power_domain_on\n");

#ifndef __XTENSA__
    /* C3 (RISC-V) needs phy_get_romfunc_addr called early.
     * On S3 (Xtensa) it is called internally by register_chipv7_phy. */
    phy_get_romfunc_addr();
    RADIO_DBG("espradio: phy_get_romfunc_addr g_phyFuns=%p\n", g_phyFuns);
#endif

    /* Allocate the OSI table on the heap, with 256-byte alignment and a
     * 256-byte guard region on each side.  PMP analysis proved that the
     * field-100 zeroing bypasses the CPU (WiFi DMA), so moving the table
     * far from BSS prevents DMA descriptor mispointing from hitting it. */
    {
        size_t total = 256 + sizeof(wifi_osi_funcs_t) + 256;
        uint8_t *raw = (uint8_t *)malloc(total);
        if (!raw) return ESP_ERR_NO_MEM;
        memset(raw, 0, total);
        /* Align the table start to a 256-byte boundary within the allocation. */
        uintptr_t table_addr = ((uintptr_t)raw + 256 + 255) & ~(uintptr_t)255;
        s_heap_osi_funcs = (wifi_osi_funcs_t *)table_addr;
        memcpy(s_heap_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));
    }

    /* Populate g_wifi_osi_funcs as a backup; do NOT set g_osi_funcs_p yet.
     * The blob's wifi_osi_funcs_register (called inside esp_wifi_init_internal)
     * checks g_osi_funcs_p at entry and skips critical init if it's non-zero. */
    memcpy(&g_wifi_osi_funcs, &espradio_osi_funcs, sizeof(wifi_osi_funcs_t));

#ifdef __XTENSA__
    /* Clang for Xtensa has an OR-offset bug: &struct->field computes
     * (struct_base | field_offset) instead of (struct_base + field_offset).
     * For wpa_crypto_funcs at offset 4, this fires when bit 2 of struct_base
     * is set — e.g. cfg at 0x3fc9c59c has bit 2 = 1, so &cfg.wpa_crypto_funcs
     * == cfg_base (not cfg_base+4), causing memcpy to overwrite cfg.osi_funcs
     * with size=44 and cfg.wpa_crypto_funcs.size with version=1.
     *
     * Two-layer fix:
     *   1. static + aligned(8) guarantees cfg_base bits 0-2 are always 0,
     *      so the OR equals the ADD for any offset.
     *   2. Use offsetof + char* arithmetic for the wpa_crypto_funcs pointer
     *      so the compiler emits ADD rather than OR regardless of address. */
    static wifi_init_config_t cfg __attribute__((aligned(8)));
    memset(&cfg, 0, sizeof(cfg));
    cfg.osi_funcs              = s_heap_osi_funcs;
    cfg.static_rx_buf_num      = 10;
    cfg.dynamic_rx_buf_num     = 32;
    cfg.tx_buf_type            = 1;
    cfg.static_tx_buf_num      = 0;
    cfg.dynamic_tx_buf_num     = 32;
    cfg.rx_mgmt_buf_type       = 0;
    cfg.rx_mgmt_buf_num        = 5;
    cfg.cache_tx_buf_num       = 0;
    cfg.csi_enable             = 0;
    cfg.ampdu_rx_enable        = 0;
    cfg.ampdu_tx_enable        = 0;
    cfg.amsdu_tx_enable        = 0;
    cfg.nvs_enable             = 0;
    cfg.nano_enable            = 0;
    cfg.rx_ba_win              = 6;
    cfg.wifi_task_core_id      = 0;
    cfg.beacon_max_len         = 752;
    cfg.mgmt_sbuf_num          = 32;
    cfg.feature_caps           = 0;
    cfg.sta_disconnected_pm    = false;
    cfg.espnow_max_encrypt_num = 7;
    cfg.tx_hetb_queue_num      = 1;
    cfg.dump_hesigb_enable     = false;
    cfg.magic                  = 0x1F2F3F4F;
    /* Copy wpa_crypto_funcs using offsetof+char* so the compiler emits ADD
     * not OR for the destination address (see aligned(8) comment above). */
    {
        const wpa_crypto_funcs_t *src = &g_wifi_default_wpa_crypto_funcs;
        wpa_crypto_funcs_t *dst = (wpa_crypto_funcs_t *)(
            (char *)&cfg + offsetof(wifi_init_config_t, wpa_crypto_funcs));
        memcpy(dst, src, sizeof(*dst));
    }
#else
    /* C3 (RISC-V): no OR-offset bug, use the macro directly. */
    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    cfg.osi_funcs = s_heap_osi_funcs;
    cfg.nvs_enable = 0;
#endif

    extern wifi_osi_funcs_t *wifi_funcs;
    wifi_funcs = s_heap_osi_funcs;

    espradio_bt_irq_prewire();
    RADIO_DBG("espradio: after bt_irq_prewire\n");

    extern void espradio_coex_adapter_init(void);
    espradio_coex_adapter_init();
    RADIO_DBG("espradio: after coex_adapter_init\n");
    extern esp_err_t coex_pre_init(void);
    RADIO_DBG("espradio: calling coex_pre_init\n");
    esp_err_t coex_rc = coex_pre_init();
    RADIO_DBG("espradio: coex_pre_init returned %d\n", (int)coex_rc);
    RADIO_DBG("espradio: before esp_wifi_init_internal cfg.osi_funcs=%p\n", (void*)cfg.osi_funcs);

    esp_err_t ret = esp_wifi_init_internal(&cfg);
    RADIO_DBG("espradio: esp_wifi_init_internal returned %d\n", (int)ret);

    if (ret == 0) {
        extern void esp_phy_modem_init(void);
        esp_phy_modem_init();

        extern esp_err_t esp_supplicant_init(void);
        esp_err_t sup_rc = esp_supplicant_init();
        RADIO_DBG("espradio: esp_supplicant_init -> %d\n", (int)sup_rc);
    }
    return ret;
}

void espradio_wifi_init_completed(void) {
    wifi_init_completed();
    RADIO_DBG("espradio: wifi_init_completed\n");
}

/* Minimal symbol expected by blobs (wifi_event_post in libnet80211.a).
 * In IDF this is ESP_EVENT_DECLARE_BASE(WIFI_EVENT), i.e. extern esp_event_base_t const WIFI_EVENT;
 * where esp_event_base_t = const char*. Here we provide the same definition without linking libesp_event. */
esp_event_base_t const WIFI_EVENT = "WIFI_EVENT";

__attribute__((weak)) void net80211_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    va_list args;
    va_start(args, format);
    printf("espradio net80211: ");
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

static volatile uint32_t s_phy_printf_count;

__attribute__((weak)) void phy_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    uint32_t n = s_phy_printf_count++;
    va_list args;
    va_start(args, format);
    printf("espradio phy: [%lu] ", (unsigned long)n);
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

__attribute__((weak)) void pp_printf(const char *format, ...) {
#if ESPRADIO_RADIO_DEBUG
    va_list args;
    va_start(args, format);
    printf("espradio pp: ");
    vprintf(format, args);
    va_end(args);
#else
    (void)format;
#endif
}

esp_err_t espradio_set_country_eu_manual(void) {
    wifi_country_t c;
    esp_err_t rc = esp_wifi_get_country(&c);
    if (rc != ESP_OK) return rc;
    c.cc[0] = 'E'; c.cc[1] = 'U'; c.cc[2] = ' ';
    c.schan = 1; c.nchan = 13;
    c.policy = WIFI_COUNTRY_POLICY_MANUAL;
    return esp_wifi_set_country(&c);
}

esp_err_t espradio_sta_set_config(const char *ssid, int ssid_len,
                                  const char *pwd, int pwd_len) {
    if (ssid_len < 0 || pwd_len < 0)
        return ESP_ERR_INVALID_ARG;
    wifi_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    if (ssid_len > 32) ssid_len = 32;
    memcpy(cfg.sta.ssid, ssid, ssid_len);
    if (pwd_len > 64) pwd_len = 64;
    memcpy(cfg.sta.password, pwd, pwd_len);
    if (pwd_len > 0)
        cfg.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
    return esp_wifi_set_config(WIFI_IF_STA, &cfg);
}

esp_err_t espradio_ap_set_config(const char *ssid, int ssid_len,
                                 const char *pwd, int pwd_len,
                                 uint8_t channel, int auth_open) {
    if (ssid_len < 0 || pwd_len < 0)
        return ESP_ERR_INVALID_ARG;
    wifi_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    if (ssid_len > 32) ssid_len = 32;
    memcpy(cfg.ap.ssid, ssid, ssid_len);
    cfg.ap.ssid_len = (uint8_t)ssid_len;
    if (pwd_len > 64) pwd_len = 64;
    memcpy(cfg.ap.password, pwd, pwd_len);
    cfg.ap.channel = channel ? channel : 1;
    cfg.ap.max_connection = 4;
    cfg.ap.authmode = (auth_open || pwd_len == 0) ? WIFI_AUTH_OPEN : WIFI_AUTH_WPA2_PSK;
    return esp_wifi_set_config(WIFI_IF_AP, &cfg);
}

static volatile uint32_t espradio_sniff_packets = 0;

static void espradio_promisc_rx_cb(void *buf, wifi_promiscuous_pkt_type_t type) {
    (void)buf; (void)type;
    espradio_sniff_packets++;
}

esp_err_t espradio_sniff_begin(uint8_t channel) {
    wifi_promiscuous_filter_t filter;
    filter.filter_mask = WIFI_PROMIS_FILTER_MASK_MGMT | WIFI_PROMIS_FILTER_MASK_CTRL | WIFI_PROMIS_FILTER_MASK_DATA;
    espradio_sniff_packets = 0;
    esp_err_t rc = esp_wifi_set_promiscuous(false);
    (void)rc;
    rc = esp_wifi_set_channel(channel, WIFI_SECOND_CHAN_NONE);
    if (rc != ESP_OK) return rc;
    rc = esp_wifi_set_promiscuous_filter(&filter);
    if (rc != ESP_OK) return rc;
    rc = esp_wifi_set_promiscuous_rx_cb(espradio_promisc_rx_cb);
    if (rc != ESP_OK) return rc;
    return esp_wifi_set_promiscuous(true);
}

esp_err_t espradio_sniff_end(void) {
    return esp_wifi_set_promiscuous(false);
}

uint32_t espradio_sniff_count(void) {
    return espradio_sniff_packets;
}
