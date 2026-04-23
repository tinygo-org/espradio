//go:build esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -DESPRADIO_RADIO_DEBUG=1
#cgo CFLAGS: -fno-short-enums
// libprintf provides the Espressif blob's *_printf helpers (pp_printf,
// phy_printf, net80211_printf, coexist_printf, rtc_printf) and the OSI
// __esp_radio_log_write/_writev entry points. We never call its varargs
// printf/vprintf/vsnprintf from Clang code — those stay GCC->GCC inside
// the blob. Our diagnostic output goes through ets_printf instead.
// Place -lprintf last and group with the other blobs so members get
// pulled in to satisfy back-references like phy_printf.
#cgo LDFLAGS: -Lblobs/libs/esp32 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lrtc -lwpa_supplicant -lprintf

#include "include.h"
*/
import "C"

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/esp32"
)

// ─── Hardware init ───────────────────────────────────────────────────────────

// CPU interrupt number for WiFi MAC. On Xtensa, TinyGo dispatches only lines
// 6-30. Interrupt 12 is a level-triggered, level-1 interrupt in the allocatable range.
const wifiCPUInterrupt = 12

func initHardware() error {
	// ESP32 uses DPORT for modem clock/reset control (not APB_CTRL like S3).
	// See: https://github.com/esp-rs/esp-hal/blob/main/esp-radio/src/radio_clocks/clocks_ll/esp32.rs

	esp.RTC_CNTL.DIG_PWC.ClearBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD)

	// Pulse WiFi MAC reset.
	const wifiMacRst = 1 << 2
	esp.DPORT.CORE_RST_EN.SetBits(wifiMacRst)
	esp.DPORT.CORE_RST_EN.ClearBits(wifiMacRst)

	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)

	return nil
}

// ESP32 uses the TIMG0 timer at 40MHz (APB 80MHz / prescaler 2).
// Same as ESP32-C3/S3 — 25ns per tick.
const ticksPerSecond = 16_000_000

// ESP32 has 328KB DRAM total. 32KB runs out under WPA connect (two ~1700-byte
// supplicant allocs fail simultaneously), producing unreliable connects; 40KB
// gives enough headroom for those plus scan/reconnect churn.
const arenaPoolSize = 40 * 1024

// ESP32 (Xtensa): don't run the blob ISR from interrupt context — the deep
// windowed call chains can overflow the interrupted goroutine's stack.
// Mask the level-triggered interrupt and wake the scheduler; schedOnce()
// will call espradio_call_wifi_isr() on its own goroutine stack.
func wifiISRHandler(interrupt.Interrupt) {
	C.espradio_ints_off(C.uint32_t(1 << wifiCPUInterrupt))
	kickSched()
}
