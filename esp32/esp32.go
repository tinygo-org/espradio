//go:build esp32

package esp32

// #cgo CFLAGS: -I../blobs/include
// #cgo CFLAGS: -I../blobs/include/esp32
// #cgo CFLAGS: -I../blobs/include/local
// #cgo CFLAGS: -I../blobs/headers
// #cgo CFLAGS: -I..
// #cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
// #cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
// #cgo CFLAGS: -fno-short-enums
import "C"

import (
	"device/esp"
	"sync/atomic"
	"unsafe"
)

// ESP32 uses DPORT for WiFi clock/reset control (not APB_CTRL like S3/C3).
// esp-hal writes u32::MAX to DPORT.WIFI_CLK_EN for init — simpler than S3's
// selective mask because some bits are needed for BT and disabling them crashes.

// Runtime MAC-only reset (what the OSI _wifi_reset_mac callback wants).
const halWiFiMacRstBit = 1 << 2

// Modem reset mask used by ESP-IDF's esp_wifi_bt_power_domain_on() at boot:
// DPORT_WIFIBB_RST | DPORT_WIFIMAC_RST | DPORT_BTBB_RST | DPORT_BTMAC_RST |
// DPORT_RW_BTMAC_RST = bits 0, 2, 3, 4, 9 = 0x21D.
// See ESP-IDF components/esp_phy/src/phy_init.c:438 and
// components/soc/esp32/register/soc/dport_reg.h MODEM_RESET_FIELD_WHEN_PU.
// FE_RST (bit 1) is deliberately excluded — the front-end is not in the
// WiFi power domain on original ESP32.  Pulsing only bit 2 (MAC) leaves
// the baseband in stale state, which causes AUTH_EXPIRE on connect: TX
// works but the RX path can't parse the AP's auth response.
const halModemResetMask = (1 << 0) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 9)

var halWiFiClockRefcnt atomic.Uint32

//export espradio_hal_init_clocks_go
func espradio_hal_init_clocks_go() {
	if halWiFiClockRefcnt.Add(1) != 1 {
		return
	}

	esp.RTC_CNTL.DIG_PWC.ClearBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD)
	// Pulse the full modem reset set (WIFIBB+WIFIMAC+BTBB+BTMAC+RW_BTMAC) via
	// DPORT.CORE_RST_EN (= DPORT_WIFI_RST_EN_REG in ESP-IDF).  Pulsing only
	// WIFIMAC_RST leaves the WiFi baseband in a stale state, which causes
	// AUTH_EXPIRE on connect: TX works but RX can't parse the AP's auth reply.
	esp.DPORT.CORE_RST_EN.SetBits(halModemResetMask)
	esp.DPORT.CORE_RST_EN.ClearBits(halModemResetMask)
	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)
	// Enable all WiFi/BT clocks. ESP-IDF and esp-hal both use u32::MAX here
	// for ESP32 because selectively disabling bits causes BT stack crashes.
	esp.DPORT.SetWIFI_CLK_EN(0xFFFFFFFF)
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

	esp.DPORT.SetWIFI_CLK_EN(0)
	esp.RTC_CNTL.DIG_PWC.SetBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD)
	esp.RTC_CNTL.DIG_ISO.SetBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)
}

//export espradio_hal_wifi_rtc_enable_iso_go
func espradio_hal_wifi_rtc_enable_iso_go() {
	esp.RTC_CNTL.DIG_ISO.SetBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)
}

//export espradio_hal_wifi_rtc_disable_iso_go
func espradio_hal_wifi_rtc_disable_iso_go() {
	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)
}

//export espradio_hal_reset_wifi_mac_go
func espradio_hal_reset_wifi_mac_go() {
	esp.DPORT.CORE_RST_EN.SetBits(halWiFiMacRstBit)
	esp.DPORT.CORE_RST_EN.ClearBits(halWiFiMacRstBit)
}

//export espradio_hal_read_mac_go
func espradio_hal_read_mac_go(mac *C.uchar, iftype C.uint) C.int {
	if mac == nil {
		return -1
	}

	// ESP32 base MAC is in EFUSE BLK0_RDATA1 (word 0) and BLK0_RDATA2 (bits 0-15).
	w0 := esp.EFUSE.BLK0_RDATA1.Get()
	w1 := esp.EFUSE.GetBLK0_RDATA2_RD_MAC_1()

	m := (*[6]byte)(unsafe.Pointer(mac))
	m[0] = byte((w1 >> 8) & 0xff)
	m[1] = byte(w1 & 0xff)
	m[2] = byte((w0 >> 24) & 0xff)
	m[3] = byte((w0 >> 16) & 0xff)
	m[4] = byte((w0 >> 8) & 0xff)
	m[5] = byte(w0 & 0xff)

	if iftype != 0 {
		m[0] |= 0x02
		m[5] = byte(uint32(m[5]) + uint32(iftype))
	}

	return 0
}
