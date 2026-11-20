//go:build esp32c3 || esp32c6

package hwcrypto

import (
	"hash"

	"github.com/soypat/lcrypto/sha512"
)

func newSHA384() hash.Hash {
	return sha512.New384()
}
