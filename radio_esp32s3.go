//go:build esp32s3

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32s3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums
#cgo LDFLAGS: -Lblobs/libs/esp32s3 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant

#include "include.h"
*/
import "C"

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/esp32s3"
)

// ─── Hardware init ───────────────────────────────────────────────────────────

// CPU interrupt number for WiFi MAC. On Xtensa, TinyGo dispatches only lines
// 6-30. Interrupt 12 is a level-triggered, level-1 interrupt in the allocatable range.
const wifiCPUInterrupt = 12

func initHardware() error {
	// See:
	// https://github.com/esp-rs/esp-hal/blob/main/esp-radio/src/radio_clocks/clocks_ll/esp32s3.rs

	const (
		SYSTEM_WIFIBB_RST       = 1 << 0
		SYSTEM_FE_RST           = 1 << 1
		SYSTEM_WIFIMAC_RST      = 1 << 2
		SYSTEM_BTBB_RST         = 1 << 3  // Bluetooth Baseband
		SYSTEM_BTMAC_RST        = 1 << 4  // deprecated
		SYSTEM_RW_BTMAC_RST     = 1 << 9  // Bluetooth MAC
		SYSTEM_RW_BTMAC_REG_RST = 1 << 11 // Bluetooth MAC Registers
		SYSTEM_BTBB_REG_RST     = 1 << 13 // Bluetooth Baseband Registers
	)

	const MODEM_RESET_FIELD_WHEN_PU = SYSTEM_WIFIBB_RST |
		SYSTEM_FE_RST |
		SYSTEM_WIFIMAC_RST |
		SYSTEM_BTBB_RST |
		SYSTEM_BTMAC_RST |
		SYSTEM_RW_BTMAC_RST |
		SYSTEM_RW_BTMAC_REG_RST |
		SYSTEM_BTBB_REG_RST

	esp.RTC_CNTL.DIG_PWC.ClearBits(1 << 17) // WIFI_FORCE_PD
	esp.APB_CTRL.WIFI_RST_EN.SetBits(MODEM_RESET_FIELD_WHEN_PU)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(MODEM_RESET_FIELD_WHEN_PU)
	esp.RTC_CNTL.DIG_ISO.ClearBits(1 << 28) // WIFI_FORCE_ISO

	return nil
}

// ESP32-S3 uses the Xtensa systimer at 16 MHz.
// See: https://github.com/esp-rs/esp-hal/blob/main/esp-hal/src/timer/systimer.rs
const ticksPerSecond = 16_000_000

// S3 has 416KB SRAM1; larger arena pool for blob allocations.
const arenaPoolSize = 48 * 1024

// ESP32-S3 (Xtensa): don't run the blob ISR from interrupt context — the deep
// windowed call chains can overflow the interrupted goroutine's 8KB stack.
// Just mask the level-triggered interrupt and wake the scheduler; schedOnce()
// will call espradio_call_wifi_isr() on its own goroutine stack.
func wifiISRHandler(interrupt.Interrupt) {
	countHWWiFiISR()
	C.espradio_ints_off(C.uint32_t(1 << wifiCPUInterrupt))
	// Unthrottled deliberately: a real hardware event, and here it is the only
	// thing that will run the blob ISR, since this handler does not.
	kickSched()
}
