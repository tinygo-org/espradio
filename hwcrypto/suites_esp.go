//go:build esp32c3 || esp32s3

package hwcrypto

import (
	"hash"

	"github.com/soypat/lneto/crypto/tlsauto"
)

// TLSAES128GCMSHA256 returns the TLS_AES_128_GCM_SHA256 suite, RFC 8446 B.4.
func TLSAES128GCMSHA256() tlsauto.Suite {
	return tlsauto.Suite{
		ID:      0x1301,
		KeyLen:  16,
		NewAEAD: func() tlsauto.AEADCipher { return new(AESGCM) },
		NewHash: func() hash.Hash { return NewSHA256() },
	}
}

// TLSAES256GCMSHA384 returns the TLS_AES_256_GCM_SHA384 suite, RFC 8446 B.4.
// SHA-384 runs in software on the ESP32-C3.
func TLSAES256GCMSHA384() tlsauto.Suite {
	return tlsauto.Suite{
		ID:      0x1302,
		KeyLen:  32,
		NewAEAD: func() tlsauto.AEADCipher { return new(AESGCM) },
		NewHash: newSHA384,
	}
}
