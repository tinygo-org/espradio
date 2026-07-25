//go:build esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_IDF_TARGET_ESP32=1
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums
#cgo LDFLAGS: -Lblobs/libs/esp32 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lrtc -lwpa_supplicant

#include "include.h"
*/
import "C"

import (
	"runtime/interrupt"
	"unsafe"

	_ "tinygo.org/x/espradio/esp32"
)

// ─── Hardware init ───────────────────────────────────────────────────────────

// CPU interrupt number for WiFi MAC. On Xtensa, TinyGo dispatches only lines
// 6-30. Interrupt 12 is a level-triggered, level-1 interrupt in the allocatable range.
const wifiCPUInterrupt = 12

func initHardware() error {
	// esp-hal's ESP32 radio init only enables the WiFi/BT clock gates; it does
	// not power-cycle or reset the modem here (the ROM bootloader already
	// powered up the WiFi domain, and there is no WiFi MAC reset bit in
	// DPORT.CORE_RST_EN). The actual clock enable is done in
	// espradio_hal_init_clocks_go, called from Enable().
	// See: esp-radio/src/radio_clocks/clocks_ll/esp32.rs
	return nil
}

// ESP32 uses the CPU clock counter (CCOUNT) at 240 MHz by default.
// However, for systimer compatibility with the espradio framework, we use
// the FRC timer at 80 MHz (APB clock / 256 = 312.5 KHz, but we actually
// use esp_timer_get_time which returns microseconds).
// The blob uses esp_timer_get_time (microseconds), so ticksPerSecond = 1MHz.
const ticksPerSecond = 16_000_000

// ESP32 arena lives in a dedicated region in safe upper SRAM1 (see _arena_start
// in esp32.ld), outside the Go GC heap. This value is an upper bound; the actual
// size is clamped to the linker-reserved region (the remainder of DRAM1 after
// .bss), so it uses whatever space is left there.
const arenaPoolSize = 48 * 1024

// Linker symbols bounding the dedicated arena region (remainder of DRAM1).
//
//go:extern _arena_start
var espradioArenaStart [0]byte

//go:extern _arena_end
var espradioArenaEnd [0]byte

// makeArenaPool returns a slice over the dedicated arena region reserved in the
// linker script, outside the Go GC heap. This isolates WiFi DMA buffers from
// GC-managed Go objects and keeps them off the SRAM2 heap.
func makeArenaPool(size int) []byte {
	start := uintptr(unsafe.Pointer(&espradioArenaStart))
	end := uintptr(unsafe.Pointer(&espradioArenaEnd))
	if region := int(end - start); size > region {
		size = region
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&espradioArenaStart)), size)
}

// ESP32 (Xtensa): don't run the blob ISR from interrupt context — the deep
// windowed call chains can overflow the interrupted goroutine's 8KB stack.
// Just mask the level-triggered interrupt and wake the scheduler; schedOnce()
// will call espradio_call_wifi_isr() on its own goroutine stack.
func wifiISRHandler(interrupt.Interrupt) {
	C.espradio_ints_off(C.uint32_t(1 << wifiCPUInterrupt))
	kickSched()
}
