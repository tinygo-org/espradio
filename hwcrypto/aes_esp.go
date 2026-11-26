//go:build esp32c3 || esp32s3 || esp32c6

package hwcrypto

import (
	"device/esp"
	"runtime/volatile"
	"unsafe"
)

var (
	aesKey  = (*[8]volatile.Register32)(unsafe.Pointer(&esp.AES.KEY_0))
	aesIn   = (*[4]volatile.Register32)(unsafe.Pointer(&esp.AES.TEXT_IN_0))
	aesOut  = (*[4]volatile.Register32)(unsafe.Pointer(&esp.AES.TEXT_OUT_0))
	aesMode = &esp.AES.MODE
)

func (a *hwAES) begin() {
	lock()
	enable()
	esp.AES.DMA_ENABLE.Set(0)
	aesMode.Set(a.mode)
	for i := 0; i < a.n; i++ {
		aesKey[i].Set(a.key[i])
	}
}

func (a *hwAES) encrypt(dst, src *[16]byte) {
	for i := range 4 {
		aesIn[i].Set(le32(src[i*4:]))
	}
	esp.AES.TRIGGER.Set(1)
	for esp.AES.STATE.Get() != 0 {
	}
	for i := range 4 {
		putLE32(dst[i*4:], aesOut[i].Get())
	}
}
