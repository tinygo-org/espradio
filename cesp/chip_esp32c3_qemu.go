//go:build esp32c3_qemu_target

package cesp

import "runtime/interrupt"

const (
	WifiCPUInterrupt = 1
	TicksPerSecond   = 16_000_000
	ArenaPoolSize    = 32 * 1024
)

// InitHardware is a no-op for QEMU: no modem power/clock hardware present.
func InitHardware() error { return nil }

// WifiISRHandler is a no-op for QEMU: no real WiFi interrupts fire.
func WifiISRHandler(_ interrupt.Interrupt) {}
