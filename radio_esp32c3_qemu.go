//go:build esp32c3_qemu_target

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
*/
import "C"

// ticksPerSecond matches the real ESP32-C3 value used to convert timer ticks.
const ticksPerSecond = 16_000_000

// initHardware is a no-op for QEMU: no modem power / clock hardware present.
func initHardware() error { return nil }
