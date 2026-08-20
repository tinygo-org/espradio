//go:build !esp32c3 && !esp32s3

/* BLE is available on the ESP32-C3 and the ESP32-S3 only.
 * This file supplies the stubs for the other targets.
 *
 * schedOnce() in radio.go is shared by every supported target and calls
 * espradio_bt_sched_tick() unconditionally, so the other targets still need a
 * definition to link against. Without this, esp32/esp32s3 fail at link time
 * with "undefined symbol: espradio_bt_sched_tick".
 *
 * This also covers the esp32c3_qemu_target build used by the unit tests, which
 * does not compile bt_ble.c. */

void espradio_bt_sched_tick(void) {}
