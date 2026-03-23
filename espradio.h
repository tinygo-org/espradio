#pragma once

#include "include.h"

/* ===== Go → C (implemented in top-level .c files) ===== */
void espradio_set_blob_log_level(uint32_t level);
esp_err_t espradio_wifi_init(void);
void espradio_wifi_init_completed(void);
void espradio_timer_fire(void *ptimer);
void espradio_event_register_default_cb(void);
void espradio_event_loop_run_once(void);
int espradio_fire_one_pending_timer(void);
int espradio_timer_poll_due(int max_fire);
void espradio_fire_pending_timers(void);
int espradio_esp_timer_poll_due(int max_fire);
void espradio_prepare_memory_for_wifi(void);
void espradio_ensure_osi_ptr(void);
void espradio_coex_adapter_init(void);
void espradio_call_saved_isr(int32_t n);
void espradio_call_wifi_isr(void);
void espradio_prewire_wifi_interrupts(void);
void espradio_wifi_int_to_level(void);
void espradio_wifi_int_raise_priority(void);
void espradio_wifi_unmask(void);
void espradio_ints_on(uint32_t mask);
void espradio_ints_off(uint32_t mask);
int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
uint32_t espradio_isr_ring_head(void);
uint32_t espradio_isr_ring_tail(void);
void     espradio_isr_ring_advance_tail(void);
void    *espradio_isr_ring_entry_queue(uint32_t idx);
void    *espradio_isr_ring_entry_item(uint32_t idx);
uint32_t espradio_isr_ring_drops(void);
void espradio_alloc_stats(unsigned *out_alloc, unsigned *out_free);
uint32_t espradio_wifi_boot_state(void);
int espradio_esp_wifi_start(void);
int rtc_get_reset_reason(int cpu_no);
esp_err_t espradio_set_country_eu_manual(void);
esp_err_t espradio_sta_set_config(const char *ssid, int ssid_len,
                                  const char *pwd, int pwd_len);
esp_err_t espradio_sniff_begin(uint8_t channel);
esp_err_t espradio_sniff_end(void);
uint32_t espradio_sniff_count(void);
esp_err_t espradio_ap_set_config(const char *ssid, int ssid_len,
                                 const char *pwd, int pwd_len,
                                 uint8_t channel, int auth_open);
extern esp_err_t esp_wifi_connect_internal(void);

/* ===== netif (netif.c) ===== */
void      espradio_netif_init_netstack_cb(void);
void      espradio_post_start_cb(void);
esp_err_t espradio_netif_start_rx(int ap_mode);
int       espradio_netif_rx_available(void);
uint16_t  espradio_netif_rx_pop(void *dst, uint16_t dst_len);
int       espradio_netif_tx(void *buf, uint16_t len);
esp_err_t espradio_netif_get_mac(uint8_t mac[6]);
uint32_t  espradio_netif_rx_cb_count(void);
uint32_t  espradio_netif_rx_cb_drop(void);

/* ===== C → Go (//export from Go, resolved by linker) ===== */
__attribute__((noreturn))
extern void espradio_panic(char *s);
extern uint32_t espradio_log_timestamp(void);
extern void espradio_run_task(void *task_func, void *param);
extern uint64_t espradio_time_us_now(void);
extern void espradio_task_yield_go(void);
extern void espradio_hal_init_clocks_go(void);
extern void espradio_hal_disable_clocks_go(void);
extern void espradio_hal_wifi_rtc_enable_iso_go(void);
extern void espradio_hal_wifi_rtc_disable_iso_go(void);
extern void espradio_hal_reset_wifi_mac_go(void);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
extern void espradio_on_wifi_event(int32_t eventID, void *data);

/* ===== esp32c3/ → linker (implemented in esp32c3/ *.c) ===== */
extern void esp_phy_enable(uint32_t modem);
extern void esp_phy_disable(uint32_t modem);

// Interrupt controller / ISR helpers.
void intr_matrix_set(uint32_t cpu_no, uint32_t model_num, uint32_t intr_num);
void ets_isr_attach(uint32_t intr_num, void (*fn)(void *), void *arg);
void ets_isr_mask(uint32_t mask);
void ets_isr_unmask(uint32_t mask);

// Global ROM lock / printf hooks.
void ets_install_uart_printf(void);
void ets_install_lock(void (*lock)(void), void (*unlock)(void));
void ets_intr_lock(void);
void ets_intr_unlock(void);
