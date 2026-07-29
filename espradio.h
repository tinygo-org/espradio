#pragma once

#include "include.h"

/* Store barrier for the single-producer/single-consumer rings shared with ISR
 * context: publish the payload before publishing the index.  isr.c carries its
 * own copy because it deliberately includes no blob headers. */
#ifndef ESPRADIO_MEMORY_BARRIER
#ifdef __XTENSA__
#define ESPRADIO_MEMORY_BARRIER() __asm__ volatile ("memw" ::: "memory")
#else
#define ESPRADIO_MEMORY_BARRIER() __asm__ volatile ("fence" ::: "memory")
#endif
#endif

/* ===== Go → C (implemented in top-level .c files) ===== */
void espradio_arena_init(uint8_t *base, size_t cap);
void espradio_arena_stats(uint32_t *used, uint32_t *capacity);
void espradio_set_blob_log_level(uint32_t level);
esp_err_t espradio_wifi_init(void);
void espradio_wifi_init_completed(void);
void espradio_timer_fire(void *ptimer);
void espradio_event_register_default_cb(void);
int espradio_event_loop_run_once(void);
int espradio_timer_poll_due(int max_fire);
int espradio_esp_timer_poll_due(int max_fire);
void espradio_prepare_memory_for_wifi(void);
void espradio_ensure_osi_ptr(void);
void espradio_coex_adapter_init(void);
void espradio_call_wifi_isr(void);
void espradio_enter_hw_isr(void);
void espradio_exit_hw_isr(void);
bool espradio_in_hw_isr(void);
void espradio_mark_wifi_isr_slot(int32_t n);
void espradio_isr_tables_init(void);
uint32_t espradio_get_wifi_isr_count(void);
uint32_t espradio_wifi_isr_slots(void);
uint32_t espradio_wifi_isr_handler_calls(void);
uint32_t espradio_wifi_isr_installed(void);
void espradio_prewire_wifi_interrupts(void);
void espradio_wifi_int_to_level(void);
void espradio_wifi_int_raise_priority(void);
void espradio_wifi_unmask(void);
void espradio_set_unmask_interval_us(uint32_t us);
uint32_t espradio_unmask_interval_us(void);
uint32_t espradio_unmask_suppressed(void);
void espradio_snapshot_intenable(void);
void espradio_lower_intlevel(void);
void espradio_ints_on(uint32_t mask);
void espradio_ints_off(uint32_t mask);
int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
uint32_t espradio_queue_send_full_count(void);
void *espradio_malloc(size_t size);
void  espradio_free(void *p);
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
esp_err_t espradio_esp_now_register_recv_cb(void);
esp_err_t espradio_esp_now_register_send_cb(void);
esp_err_t espradio_esp_now_fetch_peer(int from_head, esp_now_peer_info_t *peer);
void espradio_esp_now_peer_set_encrypt(esp_now_peer_info_t *peer, int encrypt);
extern esp_err_t esp_wifi_connect_internal(void);

/* ===== netif (netif.c) ===== */
void      espradio_netif_init_netstack_cb(void);
void      espradio_post_start_cb(void);
void      espradio_save_rom_ptrs(void);
void      espradio_restore_rom_ptrs(void);
int       espradio_rom_ptrs_ready(void);
uint32_t  espradio_rom_ptrs_saved_unready(void);
uint32_t  espradio_rom_ptrs_missing(void);
esp_err_t espradio_netif_start_rx(int ap_mode);
int       espradio_netif_rx_available(void);
uint16_t  espradio_netif_rx_pop(void *dst, uint16_t dst_len);
int       espradio_netif_tx(void *buf, uint16_t len);
void      espradio_netif_set_connected(int connected);
esp_err_t espradio_netif_get_mac(uint8_t mac[6]);
uint32_t  espradio_netif_rx_cb_count(void);
uint32_t  espradio_netif_rx_cb_drop(void);
uint32_t  espradio_netif_rx_oversize(void);
void      espradio_netif_tx_stats(uint32_t *attempts, uint32_t *fail_nomem,
                                  uint32_t *fail_other, uint32_t *not_connected,
                                  uint32_t *tx_done, uint32_t *retries,
                                  uint32_t *busy_waits);

/* ===== C → Go (//export from Go, resolved by linker) ===== */
__attribute__((noreturn))
extern void espradio_panic(char *s);
extern uint32_t espradio_log_timestamp(void);
extern void espradio_run_task(void *task_func, void *param);
extern uint64_t espradio_time_us_now(void);
extern void espradio_task_yield_go(void);
extern void espradio_pump_sched_once(void);
extern void espradio_hal_init_clocks_go(void);
extern void espradio_hal_disable_clocks_go(void);
extern void espradio_hal_wifi_rtc_enable_iso_go(void);
extern void espradio_hal_wifi_rtc_disable_iso_go(void);
extern void espradio_hal_reset_wifi_mac_go(void);
extern int espradio_hal_read_mac_go(unsigned char *mac, unsigned int iftype);
extern void espradio_on_wifi_event(int32_t eventID, void *data);
extern void espradio_on_esp_now_recv(const uint8_t *src_addr, const uint8_t *dest_addr,
                                     int rssi, uint8_t channel, uint8_t secondary_channel,
                                     int noise_floor, uint32_t timestamp,
                                     const uint8_t *data, int data_len);
extern void espradio_on_esp_now_send(const uint8_t *dest_addr, const uint8_t *src_addr,
                                     wifi_interface_t ifidx, wifi_phy_rate_t rate,
                                     wifi_tx_status_t tx_status, esp_now_send_status_t status);

/* ===== chip-specific → linker (implemented in esp32c3/ or esp32s3/ *.c) ===== */
extern void esp_phy_enable(esp_phy_modem_t modem);
extern void esp_phy_disable(esp_phy_modem_t modem);

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

/* ===== BLE (bt_ble.c) ===== */
int  espradio_ble_init(void);
int  espradio_vhci_buffered(void);
int  espradio_vhci_read_byte(void);
int  espradio_vhci_read(uint8_t *buf, int max_len);
int  espradio_vhci_write(const uint8_t *data, int len);
void espradio_bt_isr_dispatch_5(void);
void espradio_bt_isr_dispatch_8(void);
void espradio_bt_enable_hw_interrupts(void);
void espradio_call_bt_isr(void);
void espradio_bt_sched_tick(void);
