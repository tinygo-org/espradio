//go:build esp32c3

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo LDFLAGS: -Lblobs/libs/esp32c3 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant -lbtbb -lbtdm_app

#include "include.h"
*/
import "C"

import (
	"device/esp"
	"runtime/interrupt"

	_ "tinygo.org/x/espradio/esp32c3"
)

// ─── Hardware init ───────────────────────────────────────────────────────────

// CPU interrupt number for WiFi MAC. On RISC-V, interrupt 1 is valid.
const wifiCPUInterrupt = 1

func initHardware() error {
	// See:
	// https://github.com/esp-rs/esp-wifi/blob/main/esp-wifi/src/common_adapter/common_adapter_esp32c3.rs#L18

	const (
		SYSTEM_WIFIBB_RST       = 1 << 0
		SYSTEM_FE_RST           = 1 << 1
		SYSTEM_WIFIMAC_RST      = 1 << 2
		SYSTEM_BTBB_RST         = 1 << 3  // Bluetooth Baseband
		SYSTEM_BTMAC_RST        = 1 << 4  // deprecated
		SYSTEM_RW_BTMAC_RST     = 1 << 9  // Bluetooth MAC
		SYSTEM_RW_BTMAC_REG_RST = 1 << 11 // Bluetooth MAC Regsiters
		SYSTEM_BTBB_REG_RST     = 1 << 13 // Bluetooth Baseband Registers
	)

	const MODEM_RESET_FIELD_WHEN_PU = SYSTEM_WIFIBB_RST |
		SYSTEM_FE_RST |
		SYSTEM_WIFIMAC_RST |
		SYSTEM_BTBB_RST |
		SYSTEM_BTMAC_RST |
		SYSTEM_RW_BTMAC_RST |
		SYSTEM_RW_BTMAC_REG_RST |
		SYSTEM_BTBB_REG_RST

	// Release both the WiFi *and* Bluetooth power domains from forced power-down
	// and forced isolation. esp-hal's init_clocks() for the C3 clears
	// wifi_force_iso + bt_force_iso and wifi_force_pd + bt_force_pd; only the
	// WiFi bits were being cleared here, so RTC_CNTL could keep the BT domain
	// isolated/powered down. The digital side still responds in that state
	// (registers read/write, the BLE CLKN counter runs), which makes it look
	// healthy while the RF never actually comes up.
	esp.RTC_CNTL.DIG_PWC.ClearBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD |
		esp.RTC_CNTL_DIG_PWC_BT_FORCE_PD)
	esp.APB_CTRL.WIFI_RST_EN.SetBits(MODEM_RESET_FIELD_WHEN_PU)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(MODEM_RESET_FIELD_WHEN_PU)
	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_FORCE_OFF |
		esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO |
		esp.RTC_CNTL_DIG_ISO_BT_FORCE_ISO)

	return nil
}

// This is the value used for the ESP32-C3, see:
// https://github.com/esp-rs/esp-wifi/blob/v0.2.0/esp-wifi/src/timer/riscv.rs#L28
const ticksPerSecond = 16_000_000

// C3 has only 321KB DRAM total, however 48KB is required for the arena pool for netlink.
const arenaPoolSize = 48 * 1024

// ESP32-C3 (RISC-V): call the blob's WiFi ISR directly from the
// hardware interrupt handler.  On RISC-V the interrupt context can
// safely call the blob's ISR without stack overflow concerns.
func wifiISRHandler(interrupt.Interrupt) {
	countHWWiFiISR()
	// Real interrupt context: mark it so nothing below yields.
	C.espradio_enter_hw_isr()
	C.espradio_call_wifi_isr()
	C.espradio_exit_hw_isr()
	// Unthrottled deliberately: this is a real hardware event and must be
	// serviced now.  Only the cooperative-yield path is rate-limited.
	kickSched()
}
