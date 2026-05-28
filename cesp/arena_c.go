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

func arenaInit(base []byte) {
	C.espradio_arena_init((*C.uint8_t)(unsafe.Pointer(&base[0])), C.size_t(len(base)))
}

func arenaStats() (used, capacity uint32) {
	var u, c C.uint32_t
	C.espradio_arena_stats(&u, &c)
	return uint32(u), uint32(c)
}
