//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

import "runtime/interrupt"

const (
	WifiCPUInterrupt = 0
	TicksPerSecond   = 0
	ArenaPoolSize    = 0
)

func InitHardware() error                  { panic("espradio: not an ESP32 target") }
func WifiISRHandler(_ interrupt.Interrupt) { panic("espradio: not an ESP32 target") }
