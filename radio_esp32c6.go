//go:build esp32c6

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c6
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo LDFLAGS: -Lblobs/libs/esp32c6 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant -lble_app -lbtbb

#include "espradio.h"
*/
import "C"

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/esp32c6"
)

const wifiCPUInterrupt = 1

// Pulse the modem resets, as in esp-hal enable_wifi_power_domain().
// A system reset does not reset the modem power domain on this chip.
func initHardware() error {
	const mask = esp.MODEM_SYSCON_MODEM_RST_CONF_RST_FE |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_BTBB |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_BTBB_APB |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_BTMAC |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_BTMAC_APB |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_ZBMAC |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_WIFIBB |
		esp.MODEM_SYSCON_MODEM_RST_CONF_RST_WIFIMAC
	esp.MODEM_SYSCON.MODEM_RST_CONF.SetBits(mask)
	esp.MODEM_SYSCON.MODEM_RST_CONF.ClearBits(mask)
	return nil
}

const ticksPerSecond = 16_000_000

const arenaPoolSize = 48 * 1024

const rxRingSize = 8

func wifiISRHandler(interrupt.Interrupt) {
	countHWWiFiISR()
	C.espradio_enter_hw_isr()
	C.espradio_call_wifi_isr()
	C.espradio_exit_hw_isr()
	kickSched()
}

// CPU interrupt numbers for BT, must match bt_ble_esp32c6.c.
const (
	btCPUIntMAC   = 28
	btCPUIntTimer = 30
)

func setupBTInterrupts() {
	interrupt.New(btCPUIntMAC, btMACHandler).Enable()
	interrupt.New(btCPUIntTimer, btTimerHandler).Enable()
}

func btMACHandler(interrupt.Interrupt) {
	C.espradio_bt_isr_dispatch_mac()
	kickSched()
}

func btTimerHandler(interrupt.Interrupt) {
	C.espradio_bt_isr_dispatch_timer()
	kickSched()
}
