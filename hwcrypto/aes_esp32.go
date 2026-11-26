//go:build esp32

package hwcrypto

import (
	"device/esp"
	"runtime/volatile"
	"unsafe"
)

var (
	aesKey  = (*[8]volatile.Register32)(unsafe.Pointer(&esp.AES.KEY_0))
	aesText = (*[4]volatile.Register32)(unsafe.Pointer(&esp.AES.TEXT_0))
)

// The ESP32 reads the input from and writes the result to the same TEXT
// registers, ESP-IDF components/hal/esp32/include/hal/aes_ll.h.
func (a *hwAES) begin() {
	lock()
	enable()
	esp.AES.MODE.Set(a.mode)
	for i := 0; i < a.n; i++ {
		aesKey[i].Set(a.key[i])
	}
}

func (a *hwAES) encrypt(dst, src *[16]byte) {
	for i := range 4 {
		aesText[i].Set(le32(src[i*4:]))
	}
	esp.AES.START.Set(1)
	for esp.AES.IDLE.Get() == 0 {
	}
	for i := range 4 {
		putLE32(dst[i*4:], aesText[i].Get())
	}
}
