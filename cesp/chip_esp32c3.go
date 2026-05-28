//go:build esp32c3

package cesp

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/cesp/esp32c3"
)

const (
	WifiCPUInterrupt = 1
	TicksPerSecond   = 16_000_000
	ArenaPoolSize    = 32 * 1024
)

func InitHardware() error {
	// See:
	// https://github.com/esp-rs/esp-wifi/blob/main/esp-wifi/src/common_adapter/common_adapter_esp32c3.rs#L18
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

	esp.RTC_CNTL.DIG_PWC.ClearBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD)
	esp.APB_CTRL.WIFI_RST_EN.SetBits(modemResetFieldWhenPU)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(modemResetFieldWhenPU)
	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_FORCE_OFF)
	return nil
}

// WifiISRHandler is the hardware interrupt handler for the WiFi MAC interrupt.
// On RISC-V (ESP32-C3) the interrupt context can safely call the blob's ISR directly.
func WifiISRHandler(_ interrupt.Interrupt) {
	isrCallWifiISR()
}
