//go:build esp32

package esp32

// #cgo CFLAGS: -I../blobs/include
// #cgo CFLAGS: -I../blobs/include/esp32
// #cgo CFLAGS: -I../blobs/include/local
// #cgo CFLAGS: -I../blobs/headers
// #cgo CFLAGS: -I..
// #cgo CFLAGS: -DCONFIG_IDF_TARGET_ESP32=1
// #cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
// #cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
// #cgo CFLAGS: -fno-short-enums
import "C"

import (
	"device/esp"
	"sync/atomic"
	"unsafe"
)

var halWiFiClockRefcnt atomic.Uint32

//export espradio_hal_init_clocks_go
func espradio_hal_init_clocks_go() {
	if halWiFiClockRefcnt.Add(1) != 1 {
		return
	}

	// esp-hal init_clocks() for ESP32 simply enables all WiFi/BT clock gates.
	// It deliberately does NOT touch DIG_PWC/DIG_ISO or pulse any peripheral
	// reset — the ROM bootloader has already powered up the WiFi domain, and
	// there is no WiFi MAC reset bit in DPORT.CORE_RST_EN (IDF's reset mask
	// for the WiFi module is 0).
	// See esp-radio/src/radio_clocks/clocks_ll/esp32.rs init_clocks().
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
	// On ESP32 there is no WiFi MAC reset bit in DPORT.CORE_RST_EN (IDF's
	// periph reset mask for the WiFi module is 0), so this is a no-op.
	// Pulsing an arbitrary CORE_RST_EN bit would reset an unrelated
	// peripheral and hang the WiFi bring-up.
}

//export espradio_hal_read_mac_go
func espradio_hal_read_mac_go(mac *C.uchar, iftype C.uint) C.int {
	if mac == nil {
		return -1
	}

	// ESP32 MAC address is in EFUSE BLK0:
	//   BLK0_RDATA1: MAC[31:0] (bytes 5..2 in big-endian order)
	//   BLK0_RDATA2: MAC[47:32] in lower 16 bits (bytes 1..0)
	w0 := esp.EFUSE.BLK0_RDATA1.Get()
	w1 := esp.EFUSE.BLK0_RDATA2.Get()

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
