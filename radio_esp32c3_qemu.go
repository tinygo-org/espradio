//go:build esp32c3_qemu_target

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
*/
import "C"

import "runtime/interrupt"

// ticksPerSecond matches the real ESP32-C3 value used to convert timer ticks.
const ticksPerSecond = 16_000_000

// wifiCPUInterrupt is the CPU interrupt number for WiFi MAC (matches real C3).
const wifiCPUInterrupt = 1

// arenaPoolSize matches the real C3 value.
const arenaPoolSize = 32 * 1024

// initHardware is a no-op for QEMU: no modem power / clock hardware present.
func initHardware() error { return nil }

// wifiISRHandler is a no-op for QEMU: no real WiFi interrupts.
func wifiISRHandler(interrupt.Interrupt) {}
