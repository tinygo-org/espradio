//go:build esp32s3

package cesp

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/cesp/esp32s3"
)

const (
	WifiCPUInterrupt = 12
	TicksPerSecond   = 16_000_000
	ArenaPoolSize    = 48 * 1024
)

func InitHardware() error {
	// See:
	// https://github.com/esp-rs/esp-hal/blob/main/esp-radio/src/radio_clocks/clocks_ll/esp32s3.rs
	const (
		SYSTEM_WIFIBB_RST       = 1 << 0
		SYSTEM_FE_RST           = 1 << 1
		SYSTEM_WIFIMAC_RST      = 1 << 2
		SYSTEM_BTBB_RST         = 1 << 3
		SYSTEM_BTMAC_RST        = 1 << 4
		SYSTEM_RW_BTMAC_RST     = 1 << 9
		SYSTEM_RW_BTMAC_REG_RST = 1 << 11
		SYSTEM_BTBB_REG_RST     = 1 << 13
	)
	const modemResetFieldWhenPU = SYSTEM_WIFIBB_RST |
		SYSTEM_FE_RST |
		SYSTEM_WIFIMAC_RST |
		SYSTEM_BTBB_RST |
		SYSTEM_BTMAC_RST |
		SYSTEM_RW_BTMAC_RST |
		SYSTEM_RW_BTMAC_REG_RST |
		SYSTEM_BTBB_REG_RST

	esp.RTC_CNTL.DIG_PWC.ClearBits(1 << 17) // WIFI_FORCE_PD
	esp.APB_CTRL.WIFI_RST_EN.SetBits(modemResetFieldWhenPU)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(modemResetFieldWhenPU)
	esp.RTC_CNTL.DIG_ISO.ClearBits(1 << 28) // WIFI_FORCE_ISO
	return nil
}

// WifiISRHandler is the hardware interrupt handler for the WiFi MAC interrupt.
// On Xtensa (ESP32-S3) the blob ISR cannot run in interrupt context (deep windowed
// call chains overflow the stack), so the interrupt is masked and the scheduler wakes
// a goroutine to call the ISR on a full-sized stack.
func WifiISRHandler(_ interrupt.Interrupt) {
	isrIntsOff(1 << WifiCPUInterrupt)
}
