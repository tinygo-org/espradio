//go:build esp32c3

package esp32c3

// #cgo CFLAGS: -I../blobs/include
// #cgo CFLAGS: -I../blobs/include/esp32c3
// #cgo CFLAGS: -I../blobs/include/local
// #cgo CFLAGS: -I../blobs/headers
// #cgo CFLAGS: -I..
// #cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
// #cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
import "C"

import (
	"device/esp"
	"sync/atomic"
	"unsafe"
)

const (
	halSystemWiFiClkEn = 0x00FB9FCF
	halWiFiBbRstBit    = 1 << 0
	halWiFiFeRstBit    = 1 << 1
	halWiFiMacRstBit   = 1 << 2
	halWiFiResetMask   = halWiFiBbRstBit | halWiFiFeRstBit | halWiFiMacRstBit
)

var halWiFiClockRefcnt atomic.Uint32

//export espradio_hal_init_clocks_go
func espradio_hal_init_clocks_go() {
	if halWiFiClockRefcnt.Add(1) != 1 {
		return
	}

	esp.RTC_CNTL.DIG_PWC.ClearBits(esp.RTC_CNTL_DIG_PWC_WIFI_FORCE_PD)
	esp.APB_CTRL.WIFI_RST_EN.SetBits(halWiFiResetMask)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(halWiFiResetMask)
	esp.RTC_CNTL.DIG_ISO.ClearBits(esp.RTC_CNTL_DIG_ISO_WIFI_FORCE_ISO)
	cur := esp.APB_CTRL.GetWIFI_CLK_EN()
	esp.APB_CTRL.SetWIFI_CLK_EN(cur | halSystemWiFiClkEn)
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

	cur := esp.APB_CTRL.GetWIFI_CLK_EN()
	esp.APB_CTRL.SetWIFI_CLK_EN(cur &^ halSystemWiFiClkEn)
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
	esp.APB_CTRL.WIFI_RST_EN.SetBits(halWiFiMacRstBit)
	esp.APB_CTRL.WIFI_RST_EN.ClearBits(halWiFiMacRstBit)
}

//export espradio_hal_read_mac_go
func espradio_hal_read_mac_go(mac *C.uchar, iftype C.uint) C.int {
	if mac == nil {
		return -1
	}

	w0 := esp.EFUSE.GetRD_MAC_SPI_SYS_0()
	w1 := esp.EFUSE.GetRD_MAC_SPI_SYS_1_MAC_1()

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
