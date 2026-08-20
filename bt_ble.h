/* Chip layer for the shared BLE driver in bt_ble.c.
 * bt_ble_esp32c3.c and bt_ble_esp32s3.c supply the chip parts below the blob. */

#ifndef ESPRADIO_BT_BLE_H
#define ESPRADIO_BT_BLE_H

#include <stdint.h>

/* The chip layer supplies these. bt_ble.c calls them. */

/* Disable and enable the interrupts around the blob ke_env queue operations.
 * Count the nesting. Only the innermost exit enables the interrupts again. */
void espradio_bt_cs_enter(void);
void espradio_bt_cs_exit(void);

/* Nesting depth of the critical section. Zero means that no section is open. */
uint32_t espradio_bt_cs_depth(void);

/* One word from the hardware random number generator. */
uint32_t espradio_bt_hw_rand(void);

/* Value for esp_bt_controller_config_t.hw_target_code. The esp_bt.h macro keys
 * off CONFIG_IDF_TARGET_ESP32C3, which bt_ble.c does not set. */
uint32_t espradio_bt_hw_target_code(void);

/* CPU ticks per microsecond for ROM ets_update_cpu_frequency().
 * A wrong value makes ROM ets_delay_us() too short and the RF calibration fails. */
uint32_t espradio_bt_cpu_ticks_per_us(void);

/* Run the blob ISRs that the chip layer deferred. The ESP32-C3 does nothing. */
void espradio_bt_chip_service_isrs(void);

/* Chip diagnostics. These compile to nothing when ESPRADIO_BLE_DEBUG is 0. */
void espradio_bt_chip_debug_after_init(void);
void espradio_bt_chip_debug_tick(void);

/* bt_ble.c supplies these. The chip layer calls them. */

/* Run one registered blob ISR. which is 5 for RWBT and BT_BB, 8 for RWBLE.
 * Returns 0 when no handler is registered. */
int espradio_bt_run_isr(int which);

/* Counters for the semaphore gives and for the wakes that found no semaphore. */
uint32_t espradio_bt_wake_gives(void);
uint32_t espradio_bt_wake_nosem(void);

/* ROM data symbols. TinyGo targets/esp32c3.ld and targets/esp32s3.ld give the
 * addresses, which are different on each chip. */
extern uint32_t rw_sleep_enable;
extern uint32_t btdm_pwr_state;

#endif /* ESPRADIO_BT_BLE_H */
