//go:build esp32s3

package hwcrypto

import "hash"

// newSHA384 returns SHA-384 on the SHA accelerator. The ESP32-C3 has no SHA-512 engine.
func newSHA384() hash.Hash {
	return &digest{mode: shaMode384, size: 48, blockSize: 128, stateLen: 16}
}
