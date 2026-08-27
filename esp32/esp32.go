//go:build esp32

package esp32

// #cgo CFLAGS: -I../blobs/include
// #cgo CFLAGS: -I../blobs/include/esp32
// #cgo CFLAGS: -I../blobs/include/local
// #cgo CFLAGS: -I..
// #cgo CFLAGS: -DCONFIG_IDF_TARGET_ESP32=1
// #cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
// #cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
// #cgo CFLAGS: -fno-short-enums
//
// #include <stdint.h>
// extern void ets_delay_us(uint32_t us);
import "C"

import (
	"device/esp"
	"sync/atomic"
	"unsafe"
)

// modemResetMask matches MODEM_RESET_FIELD_WHEN_PU in esp-idf's
// components/soc/esp32/register/soc/dport_reg.h:1082-1086 — DPORT_WIFIBB_RST
// (bit0) | DPORT_WIFIMAC_RST (bit2) | DPORT_BTBB_RST (bit3) |
// DPORT_BTMAC_RST (bit4) | DPORT_RW_BTMAC_RST (bit9). Pulsed on
// DPORT.CORE_RST_EN by the real esp_wifi_bt_power_domain_on() — contrary to
// a previous comment here, classic ESP32 DOES have a WiFi MAC reset bit;
// only the newer PCR/MODEM_SYSCON-based chips (checked earlier this session
// for ESP32-C6) lack a software-controlled reset for it.
const modemResetMask = 1<<0 | 1<<2 | 1<<3 | 1<<4 | 1<<9

var halWiFiClockRefcnt atomic.Uint32

//export espradio_hal_init_clocks_go
func espradio_hal_init_clocks_go() {
	if halWiFiClockRefcnt.Add(1) != 1 {
		return
	}

	// Full esp_wifi_bt_power_domain_on() sequence for classic ESP32
	// (components/esp_phy/src/phy_init.c:427-452 — SOC_PM_MODEM_PD_BY_SW=1
	// here, components/soc/esp32/include/soc/soc_caps.h:357, so unlike
	// ESP32-C6 this is NOT a no-op on this chip): release WIFI_FORCE_PD,
	// wait 10us for the domain to power up, enable the WiFi/BT common
	// clock, pulse the modem reset, then release WIFI_FORCE_ISO. The
	// previous version of this function only did the clock-enable step —
	// harmless on a cold boot (the ROM bootloader already leaves
	// FORCE_PD/FORCE_ISO cleared), but wrong after any path that sets them
	// (e.g. resuming from a power-down sleep state).
	esp.RTC_CNTL.SetDIG_PWC_WIFI_FORCE_PD(0)
	C.ets_delay_us(10)
	esp.DPORT.SetWIFI_CLK_EN(0xFFFFFFFF)
	esp.DPORT.CORE_RST_EN.SetBits(modemResetMask)
	esp.DPORT.CORE_RST_EN.ClearBits(modemResetMask)
	esp.RTC_CNTL.DIG_ISO.ClearBits(1 << 28) // WIFI_FORCE_ISO
}

//export espradio_hal_disable_clocks_go
func espradio_hal_disable_clocks_go() {
	for {
		curRef := halWiFiClockRefcnt.Load()
		if curRef == 0 {
			return
		}
		if halWiFiClockRefcnt.CompareAndSwap(curRef, curRef-1) {
			if curRef != 1 {
				return
			}
			break
		}
	}

	// Mirrors esp_wifi_bt_power_domain_off(): re-assert FORCE_ISO and
	// FORCE_PD (components/esp_phy/src/phy_init.c:455-467), in addition to
	// the existing clock disable.
	esp.RTC_CNTL.DIG_ISO.SetBits(1 << 28) // WIFI_FORCE_ISO
	esp.RTC_CNTL.SetDIG_PWC_WIFI_FORCE_PD(1)

	// Disable WiFi clocks (DPORT_WIFI_CLK_WIFI_EN_M = 0x406).
	cur := esp.DPORT.GetWIFI_CLK_EN()
	esp.DPORT.SetWIFI_CLK_EN(cur &^ 0x00000406)
}

//export espradio_hal_wifi_rtc_enable_iso_go
func espradio_hal_wifi_rtc_enable_iso_go() {
	esp.RTC_CNTL.DIG_ISO.SetBits(1 << 28) // WIFI_FORCE_ISO
}

//export espradio_hal_wifi_rtc_disable_iso_go
func espradio_hal_wifi_rtc_disable_iso_go() {
	esp.RTC_CNTL.DIG_ISO.ClearBits(1 << 28) // WIFI_FORCE_ISO
}

//export espradio_hal_reset_wifi_mac_go
func espradio_hal_reset_wifi_mac_go() {
	// Deliberately still a no-op, but not for the reason the previous
	// comment gave: classic ESP32 DOES have a WiFi MAC reset bit
	// (DPORT_WIFIMAC_RST) — see modemResetMask above — and esp-idf's real
	// esp_wifi_bt_power_domain_on() does pulse it. That pulse now happens
	// in espradio_hal_init_clocks_go, in the same place and order as the
	// real esp-idf sequence; duplicating it here (this hook is called
	// separately by the blob's own OSI "_reset_mac" callback) would just
	// re-reset the MAC a second time for no benefit.
}

//export espradio_hal_read_mac_go
func espradio_hal_read_mac_go(mac *C.uchar, iftype C.uint) C.int {
	if mac == nil {
		return -1
	}

	// components/hal/efuse_hal.c efuse_hal_get_mac() (chip-generic, used by
	// esp32 via components/hal/esp32/include/hal/efuse_ll.h's
	// efuse_ll_get_mac0/1() reading EFUSE.blk0_rdata1.rd_mac /
	// blk0_rdata2.rd_mac_1) does a plain little-endian store, NOT the
	// byte-reversed packing this port previously used (that reversed
	// version was also found and fixed on ESP32-C3/C6 this session — same
	// class of bug, copied across ports instead of derived from esp-idf):
	//   *(uint32_t*)&mac[0] = efuse_ll_get_mac0();  // mac[0..3] = w0 LE
	//   *(uint16_t*)&mac[4] = (uint16_t)efuse_ll_get_mac1(); // mac[4..5] = w1 LE
	w0 := esp.EFUSE.BLK0_RDATA1.Get()
	w1 := esp.EFUSE.BLK0_RDATA2.Get()

	m := (*[6]byte)(unsafe.Pointer(mac))
	m[0] = byte(w0 & 0xff)
	m[1] = byte((w0 >> 8) & 0xff)
	m[2] = byte((w0 >> 16) & 0xff)
	m[3] = byte((w0 >> 24) & 0xff)
	m[4] = byte(w1 & 0xff)
	m[5] = byte((w1 >> 8) & 0xff)

	if iftype != 0 {
		m[0] |= 0x02
		m[5] = byte(uint32(m[5]) + uint32(iftype))
	}

	return 0
}
