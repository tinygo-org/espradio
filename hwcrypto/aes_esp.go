//go:build esp32c3 || esp32s3

package hwcrypto

import (
	"device/esp"
	"runtime/volatile"
	"unsafe"
)

// AES_MODE values for encryption, ESP-IDF components/hal/esp32c3/include/hal/aes_ll.h.
const (
	aesMode128 = 0
	aesMode256 = 2
)

var enabled bool

// enable turns on the AES and SHA clocks. The DS and HMAC resets also hold AES
// and SHA in reset, ESP-IDF components/hal/esp32c3/include/hal/aes_ll.h and sha_ll.h.
func enable() {
	if enabled {
		return
	}
	esp.SYSTEM.PERIP_CLK_EN1.SetBits(esp.SYSTEM_PERIP_CLK_EN1_CRYPTO_AES_CLK_EN | esp.SYSTEM_PERIP_CLK_EN1_CRYPTO_SHA_CLK_EN)
	esp.SYSTEM.PERIP_RST_EN1.ClearBits(esp.SYSTEM_PERIP_RST_EN1_CRYPTO_AES_RST | esp.SYSTEM_PERIP_RST_EN1_CRYPTO_SHA_RST |
		esp.SYSTEM_PERIP_RST_EN1_CRYPTO_DS_RST | esp.SYSTEM_PERIP_RST_EN1_CRYPTO_HMAC_RST)
	enabled = true
}

var (
	aesKey  = (*[8]volatile.Register32)(unsafe.Pointer(&esp.AES.KEY_0))
	aesIn   = (*[4]volatile.Register32)(unsafe.Pointer(&esp.AES.TEXT_IN_0))
	aesOut  = (*[4]volatile.Register32)(unsafe.Pointer(&esp.AES.TEXT_OUT_0))
	aesMode = &esp.AES.MODE
)

// hwAES encrypts single blocks on the AES accelerator in typical mode.
type hwAES struct {
	key  [8]uint32
	n    int
	mode uint32
}

func (a *hwAES) setKey(key []byte) {
	a.n = len(key) / 4
	for i := 0; i < a.n; i++ {
		a.key[i] = le32(key[i*4:])
	}
	a.mode = aesMode128
	if len(key) == 32 {
		a.mode = aesMode256
	}
}

func (a *hwAES) zeroize() {
	*a = hwAES{}
}

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

func (a *hwAES) end() {
	unlock()
}
