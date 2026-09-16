/* OSI function table for the classic ESP32.
 * bt_ble.c includes this file. It uses the static primitives in that file, so
 * it is a header and not a separate translation unit.
 *
 * The layout is not the same as on the ESP32-C3 and the ESP32-S3. _version is
 * the first field and _magic is the last field, which is the opposite order.
 * There is no interrupt_alloc slot and no ets_delay_us slot.
 * See ESP-IDF v5.5 components/bt/controller/esp32/bt.c, struct osi_funcs_t. */

#ifndef ESPRADIO_BT_OSI_ESP32_H
#define ESPRADIO_BT_OSI_ESP32_H

/* The blob checks these two values and refuses the table if either is wrong.
 * The values are in the literal pool of btdm_osi_funcs_register in osi.o. */
#define BT_OSI_VERSION_ESP32  0x00010005u
#define BT_OSI_MAGIC          0xFADEBEADu

typedef void (*bt_xt_handler_t)(void *arg);

extern void espradio_bt_unmask(void);

/* The blob gives the interrupt number that it wants and expects the handler
 * that was there before. */
static bt_xt_handler_t bt_set_isr(int n, bt_xt_handler_t f, void *arg) {
    bt_xt_handler_t old = (n == 5) ? s_bt_isr_fn_5 : s_bt_isr_fn_8;
    bt_interrupt_handler_set(n, f, arg);
    return old;
}

/* The blob expects bool and void *, which the shared primitives do not use. */
static bool bt_is_in_isr_bool(void) { return bt_is_in_isr() != 0; }

static int32_t bt_read_efuse_mac_u8(uint8_t *mac) {
    return (int32_t)bt_read_efuse_mac((void *)mac);
}

static void bt_ints_on(unsigned int mask) {
    (void)mask;
    espradio_bt_unmask();
}

/* cfg.hli is false, so level 3 is the only level that the blob uses. */
static int bt_cause_sw_intr_to_core(int core_id, int intr_no) {
    (void)core_id; (void)intr_no;
    return 0;
}

/* The low power clock is the 150 kHz RC oscillator. btdm_lpcycle_us is
 * 2 << 19 and the fraction is 19 bits, as in ESP-IDF bt.c btdm_lpcycle_us. */
#define BT_LPCYCLE_US_FRAC  19
#define BT_LPCYCLE_US       (2u << BT_LPCYCLE_US_FRAC)

static uint32_t bt_lpcycles_2_us_esp32(uint32_t cycles) {
    uint64_t us = (uint64_t)BT_LPCYCLE_US * cycles;
    return (uint32_t)(us >> BT_LPCYCLE_US_FRAC);
}

static uint32_t bt_us_2_lpcycles_esp32(uint32_t us) {
    return (uint32_t)(((uint64_t)us << BT_LPCYCLE_US_FRAC) / BT_LPCYCLE_US);
}

/* Sleep stays off, thus the controller never gets permission to sleep. */
static bool bt_sleep_check_duration_esp32(uint32_t *slot_cnt) {
    (void)slot_cnt;
    return false;
}

static void bt_sleep_enter_phase1_esp32(uint32_t lpcycles) { (void)lpcycles; }

static bool bt_coex_bt_wakeup_request_esp32(void) {
    bt_coex_bt_wakeup_request();
    return true;
}

/* Coexistence with WiFi is not supported yet. These report "nothing to do". */
static int bt_coex_bt_request(uint32_t event, uint32_t latency, uint32_t duration) {
    (void)event; (void)latency; (void)duration;
    return 0;
}

static int bt_coex_bt_release(uint32_t event) { (void)event; return 0; }
static int bt_coex_register_bt_cb(void *cb) { (void)cb; return 0; }
static uint32_t bt_coex_bb_reset_lock(void) { return 0; }
static void bt_coex_bb_reset_unlock(uint32_t restore) { (void)restore; }

static int bt_coex_wifi_channel_get(uint8_t *primary, uint8_t *secondary) {
    (void)primary; (void)secondary;
    return -1;
}

static int bt_coex_register_wifi_channel_change_callback(void *cb) {
    (void)cb;
    return 0;
}

static int bt_coex_version_get(unsigned int *major, unsigned int *minor,
                               unsigned int *patch) {
    (void)major; (void)minor; (void)patch;
    return -1;
}

static void bt_coex_schm_status_bit_clear_u32(uint32_t type, uint32_t status) {
    bt_coex_schm_status_bit_clear((int)type, (int)status);
}

static void bt_coex_schm_status_bit_set_u32(uint32_t type, uint32_t status) {
    bt_coex_schm_status_bit_set((int)type, (int)status);
}

/* Reset the ROM function pointer tables. BLE only, thus the BR/EDR table is
 * not reset. See ESP-IDF bt.c patch_apply. */
extern void config_btdm_funcs_reset(void);
extern void config_ble_funcs_reset(void);

static void bt_patch_apply(void) {
    config_btdm_funcs_reset();
    config_ble_funcs_reset();
}

typedef struct {
    uint32_t version;
    bt_xt_handler_t (*set_isr)(int, bt_xt_handler_t, void *);
    void (*ints_on)(unsigned int);
    void (*interrupt_disable)(void);
    void (*interrupt_restore)(void);
    void (*task_yield)(void);
    void (*task_yield_from_isr)(void);
    void *(*semphr_create)(uint32_t, uint32_t);
    void (*semphr_delete)(void *);
    int32_t (*semphr_take_from_isr)(void *, void *);
    int32_t (*semphr_give_from_isr)(void *, void *);
    int32_t (*semphr_take)(void *, uint32_t);
    int32_t (*semphr_give)(void *);
    void *(*mutex_create)(void);
    void (*mutex_delete)(void *);
    int32_t (*mutex_lock)(void *);
    int32_t (*mutex_unlock)(void *);
    void *(*queue_create)(uint32_t, uint32_t);
    void (*queue_delete)(void *);
    int32_t (*queue_send)(void *, void *, uint32_t);
    int32_t (*queue_send_from_isr)(void *, void *, void *);
    int32_t (*queue_recv)(void *, void *, uint32_t);
    int32_t (*queue_recv_from_isr)(void *, void *, void *);
    int32_t (*task_create)(void *, const char *, uint32_t, void *, uint32_t, void *, uint32_t);
    void (*task_delete)(void *);
    bool (*is_in_isr)(void);
    int  (*cause_sw_intr_to_core)(int, int);
    void *(*malloc)(size_t);
    void *(*malloc_internal)(size_t);
    void (*free)(void *);
    int32_t (*read_efuse_mac)(uint8_t *);
    void (*srand)(unsigned int);
    int  (*rand)(void);
    uint32_t (*btdm_lpcycles_2_us)(uint32_t);
    uint32_t (*btdm_us_2_lpcycles)(uint32_t);
    bool (*btdm_sleep_check_duration)(uint32_t *);
    void (*btdm_sleep_enter_phase1)(uint32_t);
    void (*btdm_sleep_enter_phase2)(void);
    void (*btdm_sleep_exit_phase1)(void);
    void (*btdm_sleep_exit_phase2)(void);
    void (*btdm_sleep_exit_phase3)(void);
    bool (*coex_bt_wakeup_request)(void);
    void (*coex_bt_wakeup_request_end)(void);
    int  (*coex_bt_request)(uint32_t, uint32_t, uint32_t);
    int  (*coex_bt_release)(uint32_t);
    int  (*coex_register_bt_cb)(void *);
    uint32_t (*coex_bb_reset_lock)(void);
    void (*coex_bb_reset_unlock)(uint32_t);
    int  (*coex_schm_register_btdm_callback)(void *);
    void (*coex_schm_status_bit_clear)(uint32_t, uint32_t);
    void (*coex_schm_status_bit_set)(uint32_t, uint32_t);
    uint32_t (*coex_schm_interval_get)(void);
    uint8_t  (*coex_schm_curr_period_get)(void);
    void    *(*coex_schm_curr_phase_get)(void);
    int  (*coex_wifi_channel_get)(uint8_t *, uint8_t *);
    int  (*coex_register_wifi_channel_change_callback)(void *);
    bt_xt_handler_t (*set_isr_l3)(int, bt_xt_handler_t, void *);
    void (*interrupt_l3_disable)(void);
    void (*interrupt_l3_restore)(void);
    void *(*customer_queue_create)(uint32_t, uint32_t);
    int  (*coex_version_get)(unsigned int *, unsigned int *, unsigned int *);
    void (*patch_apply)(void);
    uint32_t magic;
} bt_osi_funcs_esp32_t;

static const bt_osi_funcs_esp32_t s_bt_osi_funcs_esp32 = {
    .version                  = BT_OSI_VERSION_ESP32,
    .set_isr                  = bt_set_isr,
    .ints_on                  = bt_ints_on,
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
    .is_in_isr                = bt_is_in_isr_bool,
    .cause_sw_intr_to_core    = bt_cause_sw_intr_to_core,
    .malloc                   = bt_malloc,
    .malloc_internal          = bt_malloc,
    .free                     = bt_free,
    .read_efuse_mac           = bt_read_efuse_mac_u8,
    .srand                    = bt_srand,
    .rand                     = bt_rand,
    .btdm_lpcycles_2_us       = bt_lpcycles_2_us_esp32,
    .btdm_us_2_lpcycles       = bt_us_2_lpcycles_esp32,
    .btdm_sleep_check_duration = bt_sleep_check_duration_esp32,
    .btdm_sleep_enter_phase1  = bt_sleep_enter_phase1_esp32,
    .btdm_sleep_enter_phase2  = bt_sleep_enter_phase2,
    .btdm_sleep_exit_phase1   = bt_sleep_exit_phase1,
    .btdm_sleep_exit_phase2   = bt_sleep_exit_phase2,
    .btdm_sleep_exit_phase3   = bt_sleep_exit_phase3,
    .coex_bt_wakeup_request   = bt_coex_bt_wakeup_request_esp32,
    .coex_bt_wakeup_request_end = bt_coex_bt_wakeup_request_end,
    .coex_bt_request          = bt_coex_bt_request,
    .coex_bt_release          = bt_coex_bt_release,
    .coex_register_bt_cb      = bt_coex_register_bt_cb,
    .coex_bb_reset_lock       = bt_coex_bb_reset_lock,
    .coex_bb_reset_unlock     = bt_coex_bb_reset_unlock,
    .coex_schm_register_btdm_callback = bt_coex_schm_register_btdm_callback,
    .coex_schm_status_bit_clear = bt_coex_schm_status_bit_clear_u32,
    .coex_schm_status_bit_set   = bt_coex_schm_status_bit_set_u32,
    .coex_schm_interval_get     = bt_coex_schm_interval_get,
    .coex_schm_curr_period_get  = bt_coex_schm_curr_period_get,
    .coex_schm_curr_phase_get   = bt_coex_schm_curr_phase_get,
    .coex_wifi_channel_get      = bt_coex_wifi_channel_get,
    .coex_register_wifi_channel_change_callback = bt_coex_register_wifi_channel_change_callback,
    .set_isr_l3               = bt_set_isr,
    .interrupt_l3_disable     = bt_interrupt_disable,
    .interrupt_l3_restore     = bt_interrupt_restore,
    .customer_queue_create    = espradio_queue_create_internal,
    .coex_version_get         = bt_coex_version_get,
    .patch_apply              = bt_patch_apply,
    .magic                    = BT_OSI_MAGIC,
};

const void *espradio_bt_osi_table(void) { return &s_bt_osi_funcs_esp32; }

#endif /* ESPRADIO_BT_OSI_ESP32_H */
