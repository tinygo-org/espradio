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

/* The OSI table that the blob expects. The layout and the version are not the
 * same on the classic ESP32 as on the ESP32-C3 and the ESP32-S3. */
const void *espradio_bt_osi_table(void);

/* Enable the modem clock, pulse the BT reset, and tell the ROM the CPU speed.
 * The registers are in DPORT on the classic ESP32 and in APB_CTRL on the
 * other chips. */
void espradio_bt_chip_clocks_up(void);

/* Start the controller. This covers the ROM patches, the config structure, the
 * PHY, btdm_controller_init and btdm_controller_enable. The order and the
 * arguments are not the same on each chip. Returns 0 on success. */
int espradio_bt_chip_controller_bringup(void);

/* Wake the blob controller task. Returns 1 when the task was woken.
 * The classic ESP32 returns 1 and does nothing, because its task blocks on a
 * queue that osi.c already releases. */
int espradio_bt_chip_wake_task(void);

/* Run the blob ke task dispatcher. The classic ESP32 does nothing, because the
 * symbol is not in its ROM. */
void espradio_bt_chip_ke_task_schedule(void);

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

/* ROM data symbols. On the ESP32-C3 and the ESP32-S3 the TinyGo linker script
 * gives the addresses, which are different on each chip. On the classic ESP32
 * libbtdm_app.a defines them. */
extern uint32_t rw_sleep_enable;
extern uint32_t btdm_pwr_state;

#endif /* ESPRADIO_BT_BLE_H */
