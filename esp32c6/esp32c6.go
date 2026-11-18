//go:build esp32c6

package esp32c6

// #cgo CFLAGS: -I../blobs/include
// #cgo CFLAGS: -I../blobs/include/esp32c6
// #cgo CFLAGS: -I../blobs/include/local
// #cgo CFLAGS: -I..
// #cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
// #cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
import "C"

import (
	"device/esp"
	"sync/atomic"
	"unsafe"
)

const halWiFiClkMask = esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFI_APB_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIMAC_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_FE_APB_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_FE_CAL_160M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_FE_160M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_FE_80M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_160X1_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_80X1_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_40X1_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_80X_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_40X_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_80M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_44M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_40M_EN |
	esp.MODEM_SYSCON_CLK_CONF1_CLK_WIFIBB_22M_EN

const halLPClkMask = esp.MODEM_LPCON_CLK_CONF_CLK_WIFIPWR_EN |
	esp.MODEM_LPCON_CLK_CONF_CLK_COEX_EN

var halWiFiClockRefcnt atomic.Uint32

// initModemClocks follows init_clocks() in esp-hal
// esp-radio/src/radio_clocks/clocks_ll/esp32c6.rs.
func initModemClocks() {
	esp.PMU.SetHP_SLEEP_ICG_MODEM_HP_SLEEP_DIG_ICG_MODEM_CODE(0)
	esp.PMU.SetHP_MODEM_ICG_MODEM_HP_MODEM_DIG_ICG_MODEM_CODE(1)
	esp.PMU.SetHP_ACTIVE_ICG_MODEM_HP_ACTIVE_DIG_ICG_MODEM_CODE(2)
	esp.PMU.IMM_MODEM_ICG.Set(esp.PMU_IMM_MODEM_ICG_UPDATE_DIG_ICG_MODEM_EN)
	esp.PMU.IMM_SLEEP_SYSCLK.Set(esp.PMU_IMM_SLEEP_SYSCLK_UPDATE_DIG_ICG_SWITCH)

	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_MODEM_APB_ST_MAP(6)
	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_MODEM_PERI_ST_MAP(4)
	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_WIFI_ST_MAP(6)
	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_BT_ST_MAP(6)
	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_FE_ST_MAP(6)
	esp.MODEM_SYSCON.SetCLK_CONF_POWER_ST_CLK_ZB_ST_MAP(6)

	esp.MODEM_LPCON.SetCLK_CONF_POWER_ST_CLK_LP_APB_ST_MAP(6)
	esp.MODEM_LPCON.SetCLK_CONF_POWER_ST_CLK_I2C_MST_ST_MAP(6)
	esp.MODEM_LPCON.SetCLK_CONF_POWER_ST_CLK_COEX_ST_MAP(6)
	esp.MODEM_LPCON.SetCLK_CONF_POWER_ST_CLK_WIFIPWR_ST_MAP(6)

	esp.MODEM_LPCON.SetWIFI_LP_CLK_CONF_CLK_WIFIPWR_LP_SEL_OSC_SLOW(1)
	esp.MODEM_LPCON.SetWIFI_LP_CLK_CONF_CLK_WIFIPWR_LP_SEL_OSC_FAST(1)
	esp.MODEM_LPCON.SetWIFI_LP_CLK_CONF_CLK_WIFIPWR_LP_SEL_XTAL32K(1)
	esp.MODEM_LPCON.SetWIFI_LP_CLK_CONF_CLK_WIFIPWR_LP_SEL_XTAL(1)
	esp.MODEM_LPCON.SetWIFI_LP_CLK_CONF_CLK_WIFIPWR_LP_DIV_NUM(0)
	esp.MODEM_LPCON.CLK_CONF.SetBits(esp.MODEM_LPCON_CLK_CONF_CLK_WIFIPWR_EN)
}

//export espradio_hal_init_clocks_go
func espradio_hal_init_clocks_go() {
	if halWiFiClockRefcnt.Add(1) != 1 {
		return
	}
	initModemClocks()
	esp.MODEM_SYSCON.CLK_CONF1.SetBits(halWiFiClkMask)
	esp.MODEM_LPCON.CLK_CONF.SetBits(halLPClkMask)
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
	esp.MODEM_SYSCON.CLK_CONF1.ClearBits(halWiFiClkMask)
	esp.MODEM_LPCON.CLK_CONF.ClearBits(halLPClkMask)
}

// The modem has no RTC isolation control on this chip, as in ESP-IDF
// components/esp_wifi/esp32c6/esp_adapter.c.
//
//export espradio_hal_wifi_rtc_enable_iso_go
func espradio_hal_wifi_rtc_enable_iso_go() {}

//export espradio_hal_wifi_rtc_disable_iso_go
func espradio_hal_wifi_rtc_disable_iso_go() {}

//export espradio_hal_reset_wifi_mac_go
func espradio_hal_reset_wifi_mac_go() {
	esp.MODEM_SYSCON.MODEM_RST_CONF.SetBits(esp.MODEM_SYSCON_MODEM_RST_CONF_RST_WIFIMAC)
	esp.MODEM_SYSCON.MODEM_RST_CONF.ClearBits(esp.MODEM_SYSCON_MODEM_RST_CONF_RST_WIFIMAC)
}

// espradio_hal_enable_phy_clocks_go follows enable_phy() in esp-hal
// esp-phy/src/phy_clocks/phy_clocks_ll_esp32c6.rs.
//
//export espradio_hal_enable_phy_clocks_go
func espradio_hal_enable_phy_clocks_go(en C.int) {
	v := uint32(0)
	if en != 0 {
		v = 1
	}
	esp.MODEM_LPCON.SetCLK_CONF_CLK_I2C_MST_EN(v)
	esp.MODEM_LPCON.SetI2C_MST_CLK_CONF_CLK_I2C_MST_SEL_160M(v)
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
