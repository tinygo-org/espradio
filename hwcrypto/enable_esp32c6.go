//go:build esp32c6

package hwcrypto

import "device/esp"

var enabled bool

// enable turns on the AES and SHA clocks. The DS and HMAC resets also hold AES
// and SHA in reset, ESP-IDF components/hal/esp32c6/include/hal/clk_gate_ll.h.
func enable() {
	if enabled {
		return
	}
	esp.PCR.AES_CONF.SetBits(esp.PCR_AES_CONF_AES_CLK_EN)
	esp.PCR.SHA_CONF.SetBits(esp.PCR_SHA_CONF_SHA_CLK_EN)
	esp.PCR.DS_CONF.ClearBits(esp.PCR_DS_CONF_DS_RST_EN)
	esp.PCR.HMAC_CONF.ClearBits(esp.PCR_HMAC_CONF_HMAC_RST_EN)
	esp.PCR.AES_CONF.ClearBits(esp.PCR_AES_CONF_AES_RST_EN)
	esp.PCR.SHA_CONF.ClearBits(esp.PCR_SHA_CONF_SHA_RST_EN)
	enabled = true
}
