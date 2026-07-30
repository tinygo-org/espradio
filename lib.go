//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums

#include "espradio.h"

// The disconnect counterpart to esp_wifi_connect_internal, which espradio.h
// already declares.
extern esp_err_t esp_wifi_disconnect_internal(void);
*/
import "C"

import "unsafe"

// Gap-fillers for symbols the WiFi blobs and libwpa_supplicant link against but
// that picolibc and TinyGo do not provide. Every one of these is an undefined
// reference in the blob archives, so each needs a definition or the link fails.
//
// These are reached from blob context, so they must not allocate: none of them
// does.

// ─── Time ────────────────────────────────────────────────────────────────────

// gettimeofday fills a struct timeval. Only the two 32-bit fields the blob
// reads are written; tz is ignored, as it is by the IDF.
//
//export gettimeofday
func gettimeofday(tv, tz unsafe.Pointer) int32 {
	if tv != nil {
		us := espradio_time_us_now()
		*(*uint32)(tv) = uint32(us / 1000000)
		*(*uint32)(unsafe.Add(tv, 4)) = uint32(us % 1000000)
	}
	return 0
}

// sleep and usleep both round up to at least one tick: the blob uses them as
// yield points, and a zero-tick delay would spin.

//export sleep
func sleep(secs uint32) uint32 {
	espradio_task_delay(secs * 100)
	return 0
}

//export usleep
func usleep(us uint32) int32 {
	ticks := us / 10000
	if ticks == 0 {
		ticks = 1
	}
	espradio_task_delay(ticks)
	return 0
}

//export vTaskDelay
func vTaskDelay(ticks uint32) {
	espradio_task_delay(ticks)
}

// esp_timer_get_time is deliberately absent here: esp_timer_shim.c carries a
// weak definition with the same body, and with nothing else defining the symbol
// that weak one is what links.

// ─── Randomness ──────────────────────────────────────────────────────────────

// xorshiftState seeds the PRNG behind esp_fill_random. This is not a CSPRNG --
// the C original was not either -- it is a time-mixed xorshift standing in for
// the hardware RNG the blob expects.
var xorshiftState uint32 = 0x12345678

// mixRandom advances the state with the current time folded in.
func mixRandom() uint32 {
	t := uint32(espradio_time_us_now())
	xorshiftState ^= t + 0x85ebca6b + (xorshiftState << 6) + (xorshiftState >> 2)
	return xorshiftState
}

// nextRandom is mixRandom plus the three xorshift rounds.
func nextRandom() uint32 {
	mixRandom()
	xorshiftState ^= xorshiftState << 13
	xorshiftState ^= xorshiftState >> 17
	xorshiftState ^= xorshiftState << 5
	return xorshiftState
}

// esp_fill_random fills buf with pseudo-random bytes.
//
// Note the asymmetry between the 4-byte body and the tail: the tail applies only
// the time-mixing step and skips the three xorshift rounds. That is carried over
// verbatim from the C version rather than corrected, because changing it changes
// the byte stream the blob gets; it is called out here so the difference reads as
// deliberate rather than as a transcription slip.
//
//export esp_fill_random
func esp_fill_random(buf unsafe.Pointer, length C.size_t) {
	if buf == nil || length == 0 {
		return
	}
	p := unsafe.Slice((*byte)(buf), int(length))
	i := 0
	for len(p)-i >= 4 {
		putLE32(p[i:], nextRandom())
		i += 4
	}
	if i < len(p) {
		v := mixRandom()
		for n := 0; i+n < len(p); n++ {
			p[i+n] = byte(v >> (8 * n))
		}
	}
}

// putLE32 writes v little-endian, matching the memcpy the C version used on
// these little-endian targets.
func putLE32(p []byte, v uint32) {
	p[0] = byte(v)
	p[1] = byte(v >> 8)
	p[2] = byte(v >> 16)
	p[3] = byte(v >> 24)
}

//export esp_random
func esp_random() uint32 {
	return nextRandom()
}

// ─── String ──────────────────────────────────────────────────────────────────

// strrchr returns a pointer to the last occurrence of c in s, or nil if absent.
// Searching for '\0' returns a pointer to the terminator, per C semantics.
//
//export strrchr
func strrchr(s *C.char, c C.int) *C.char {
	target := byte(c)
	p := unsafe.Pointer(s)
	var last unsafe.Pointer
	for {
		ch := *(*byte)(p)
		if ch == 0 {
			break
		}
		if ch == target {
			last = p
		}
		p = unsafe.Add(p, 1)
	}
	if target == 0 {
		return (*C.char)(p)
	}
	return (*C.char)(last)
}

// ─── esp_wifi high-level wrappers → blob internals ───────────────────────────

// The blob archives reference these public names while implementing only the
// _internal ones. Go's own connect path calls esp_wifi_connect_internal
// directly (see radio.go), so these exist purely to satisfy the blob.

//export esp_wifi_connect
func esp_wifi_connect() int32 {
	return int32(C.esp_wifi_connect_internal())
}

//export esp_wifi_disconnect
func esp_wifi_disconnect() int32 {
	return int32(C.esp_wifi_disconnect_internal())
}
