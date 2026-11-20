//go:build esp32

package hwcrypto

import (
	"hash"

	"github.com/soypat/lcrypto/sha256"
	"github.com/soypat/lcrypto/sha512"
)

// The ESP32 SHA accelerator cannot load a saved hash state, so it cannot run
// the transcript and HMAC hashes at once. SHA runs in software.
func newSHA256() hash.Hash { return sha256.New() }

func newSHA384() hash.Hash { return sha512.New384() }
