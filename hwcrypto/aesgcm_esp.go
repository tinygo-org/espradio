//go:build esp32 || esp32c3 || esp32s3 || esp32c6

package hwcrypto

import "errors"

var (
	errUnkeyed = errors.New("hwcrypto: cipher used before Rekey")
	errKeySize = errors.New("hwcrypto: key must be 16 or 32 bytes")
)

// AESGCM is AES-GCM with a 12 byte nonce and 16 byte tag on the AES
// accelerator. It implements tlsauto.AEADCipher. Its zero value is unkeyed.
type AESGCM struct {
	hw    hwAES
	g     gcm
	keyed bool
}

func (c *AESGCM) NonceSize() int { return gcmNonceSize }

func (c *AESGCM) Overhead() int { return gcmTagSize }

// Seal panics if c is unkeyed.
func (c *AESGCM) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	if !c.keyed {
		panic("hwcrypto: Seal before Rekey")
	}
	return c.g.seal(dst, nonce, plaintext, additionalData)
}

func (c *AESGCM) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if !c.keyed {
		return nil, errUnkeyed
	}
	return c.g.open(dst, nonce, ciphertext, additionalData)
}

// Rekey installs a 16 byte (AES-128) or 32 byte (AES-256) key.
func (c *AESGCM) Rekey(key []byte) error {
	c.Zeroize()
	if len(key) != 16 && len(key) != 32 {
		return errKeySize
	}
	c.hw.setKey(key)
	c.g.init(&c.hw)
	c.keyed = true
	return nil
}

func (c *AESGCM) Zeroize() {
	c.hw.zeroize()
	c.g.zeroize()
	c.keyed = false
}
