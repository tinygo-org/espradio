//go:build esp32

package hwcrypto

import "device/esp"

// DPORT_PERI_EN bits, ESP-IDF components/soc/esp32/include/soc/dport_reg.h.
const (
	periAES        = 1 << 0
	periSecureBoot = 1 << 3
	periDS         = 1 << 4
)

var enabled bool

// enable turns on the AES clock. The DS and secure boot resets also hold AES in
// reset, ESP-IDF components/hal/esp32/include/hal/clk_gate_ll.h.
func enable() {
	if enabled {
		return
	}
	esp.DPORT.PERI_CLK_EN.SetBits(periAES)
	esp.DPORT.PERI_RST_EN.ClearBits(periAES | periSecureBoot | periDS)
	enabled = true
}
