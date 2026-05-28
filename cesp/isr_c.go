//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package cesp

/*
#cgo CFLAGS: -fno-short-enums
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#include "espradio.h"
*/
import "C"
import "unsafe"

func isrIntsOff(mask uint32) {
	C.espradio_ints_off(C.uint32_t(mask))
}

func isrWifiUnmask() {
	C.espradio_wifi_unmask()
}

func isrCallWifiISR() {
	C.espradio_call_wifi_isr()
}

func isrRingTail() uint32 {
	return uint32(C.espradio_isr_ring_tail())
}

func isrRingHead() uint32 {
	return uint32(C.espradio_isr_ring_head())
}

func isrRingEntryQueue(idx uint32) unsafe.Pointer {
	return C.espradio_isr_ring_entry_queue(C.uint32_t(idx))
}

func isrRingEntryItem(idx uint32) unsafe.Pointer {
	return C.espradio_isr_ring_entry_item(C.uint32_t(idx))
}

func isrRingAdvanceTail() {
	C.espradio_isr_ring_advance_tail()
}

func isrRingDrops() uint32 {
	return uint32(C.espradio_isr_ring_drops())
}

func isrGetWifiISRCount() uint32 {
	return uint32(C.espradio_get_wifi_isr_count())
}

func isrWifiIntRaisePriority() {
	C.espradio_wifi_int_raise_priority()
}

func isrPrewireWifiInterrupts() {
	C.espradio_prewire_wifi_interrupts()
}

func isrWifiIntToLevel() {
	C.espradio_wifi_int_to_level()
}
