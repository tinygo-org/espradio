//go:build esp32 || esp32c3 || esp32s3 || esp32c6

package hwcrypto

// AES_MODE values for encryption, ESP-IDF components/hal/esp32c3/include/hal/aes_ll.h.
const (
	aesMode128 = 0
	aesMode256 = 2
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

func (a *hwAES) end() {
	unlock()
}
