/* OSI function table for the ESP32-C3 and the ESP32-S3.
 * bt_ble.c includes this file. It uses the static primitives in that file, so
 * it is a header and not a separate translation unit.
 * The layout matches esp-hal os_adapter_esp32c3_s3.rs exactly. */

#ifndef ESPRADIO_BT_OSI_ESP32C3_S3_H
#define ESPRADIO_BT_OSI_ESP32C3_S3_H


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

const void *espradio_bt_osi_table(void) { return &s_bt_osi_funcs; }

#endif /* ESPRADIO_BT_OSI_ESP32C3_S3_H */
