//go:build esp32c3 || esp32s3

package hwcrypto

import "device/esp"

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
