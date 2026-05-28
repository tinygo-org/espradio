//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package cesp

/*
#cgo CFLAGS: -fno-short-enums
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#include "espradio.h"
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"unsafe"
)

// ── LogLevel constants ──────────────────────────────────────────────────────

const (
	logLevelNone    = LogLevel(C.WIFI_LOG_NONE)
	logLevelError   = LogLevel(C.WIFI_LOG_ERROR)
	logLevelWarning = LogLevel(C.WIFI_LOG_WARNING)
	logLevelInfo    = LogLevel(C.WIFI_LOG_INFO)
	logLevelDebug   = LogLevel(C.WIFI_LOG_DEBUG)
	logLevelVerbose = LogLevel(C.WIFI_LOG_VERBOSE)
)

// ── WiFi mode constants ─────────────────────────────────────────────────────

const (
	wifiModeNone = WifiMode(C.WIFI_MODE_NULL)
	wifiModeSTA  = WifiMode(C.WIFI_MODE_STA)
	wifiModeAP   = WifiMode(C.WIFI_MODE_AP)
)

// ── WiFi event constants ────────────────────────────────────────────────────

const (
	wifiEventSTAConnected    = int32(C.WIFI_EVENT_STA_CONNECTED)
	wifiEventSTADisconnected = int32(C.WIFI_EVENT_STA_DISCONNECTED)
	wifiEventSTAStart        = int32(C.WIFI_EVENT_STA_START)
)

// ── Lifecycle ───────────────────────────────────────────────────────────────

func wifiInit() error {
	return codeToErr(C.espradio_wifi_init())
}

func wifiInitCompleted() {
	C.espradio_wifi_init_completed()
}

func wifiEspStart() error {
	return codeToErr(C.espradio_esp_wifi_start())
}

func wifiSetBlobLogLevel(level LogLevel) {
	C.espradio_set_blob_log_level(C.uint32_t(level))
}

func wifiSetPS(disablePS bool) {
	if disablePS {
		C.esp_wifi_set_ps(C.WIFI_PS_NONE)
	} else {
		C.esp_wifi_set_ps(C.WIFI_PS_MIN_MODEM)
	}
}

func wifiGetMode() (WifiMode, error) {
	var mode C.wifi_mode_t
	code := C.esp_wifi_get_mode(&mode)
	return WifiMode(mode), codeToErr(code)
}

func wifiSetMode(mode WifiMode) error {
	return codeToErr(C.esp_wifi_set_mode(C.wifi_mode_t(mode)))
}

func wifiSetCountryEU() error {
	return codeToErr(C.espradio_set_country_eu_manual())
}

func wifiEnsureOSIPtr() {
	C.espradio_ensure_osi_ptr()
}

func wifiRestoreROMPtrs() {
	C.espradio_restore_rom_ptrs()
}

func wifiSaveROMPtrs() {
	C.espradio_save_rom_ptrs()
}

func halInitClocksGo() {
	C.espradio_hal_init_clocks_go()
}

// ── Station ─────────────────────────────────────────────────────────────────

func wifiSetSTAConfig(ssid, password string) error {
	csssid := C.CString(ssid)
	cspwd := C.CString(password)
	code := C.espradio_sta_set_config(csssid, C.int(len(ssid)), cspwd, C.int(len(password)))
	C.free(unsafe.Pointer(csssid))
	C.free(unsafe.Pointer(cspwd))
	return codeToErr(code)
}

func wifiConnect() error {
	return codeToErr(C.esp_wifi_connect_internal())
}

// ── Scan ────────────────────────────────────────────────────────────────────

func wifiScan() ([]APRecord, error) {
	var scanCfg C.wifi_scan_config_t
	scanCfg.ssid = nil
	scanCfg.bssid = nil
	scanCfg.channel = 0
	scanCfg.show_hidden = false
	scanCfg.scan_type = C.WIFI_SCAN_TYPE_ACTIVE
	scanCfg.scan_time.active.min = 0
	scanCfg.scan_time.active.max = 300
	scanCfg.scan_time.passive = 500
	if code := C.esp_wifi_scan_start(&scanCfg, true); code != C.ESP_OK {
		return nil, codeToErr(code)
	}
	var num C.uint16_t
	if code := C.esp_wifi_scan_get_ap_num(&num); code != C.ESP_OK {
		return nil, codeToErr(code)
	}
	if num == 0 {
		return nil, nil
	}
	recs := make([]C.wifi_ap_record_t, int(num))
	if code := C.esp_wifi_scan_get_ap_records(&num, (*C.wifi_ap_record_t)(unsafe.Pointer(&recs[0]))); code != C.ESP_OK {
		return nil, codeToErr(code)
	}
	out := make([]APRecord, int(num))
	for i := range out {
		raw := C.GoBytes(unsafe.Pointer(&recs[i].ssid[0]), C.int(len(recs[i].ssid)))
		if idx := bytes.IndexByte(raw, 0); idx >= 0 {
			raw = raw[:idx]
		}
		out[i] = APRecord{SSID: string(raw), RSSI: int8(recs[i].rssi)}
	}
	return out, nil
}

// ── Soft-AP ─────────────────────────────────────────────────────────────────

func wifiSetAPConfig(ssid, password string, channel uint8, authOpen bool) error {
	auth := 0
	if authOpen {
		auth = 1
	}
	csssid := C.CString(ssid)
	cspwd := C.CString(password)
	code := C.espradio_ap_set_config(
		csssid, C.int(len(ssid)),
		cspwd, C.int(len(password)),
		C.uint8_t(channel), C.int(auth),
	)
	C.free(unsafe.Pointer(csssid))
	C.free(unsafe.Pointer(cspwd))
	return codeToErr(code)
}

// ── Sniffer ─────────────────────────────────────────────────────────────────

func sniffBegin(channel uint8) error {
	return codeToErr(C.espradio_sniff_begin(C.uint8_t(channel)))
}

func sniffCount() uint32 {
	return uint32(C.espradio_sniff_count())
}

func sniffEnd() error {
	return codeToErr(C.espradio_sniff_end())
}

// ── WiFi event parsing ───────────────────────────────────────────────────────

func parseWifiEvent(eventID int32, data unsafe.Pointer) WifiEvent {
	ev := WifiEvent{ID: eventID}
	switch eventID {
	case wifiEventSTAConnected:
		raw := (*C.wifi_event_sta_connected_t)(data)
		ssidLen := int(raw.ssid_len)
		if ssidLen > 32 {
			ssidLen = 32
		}
		ev.SSID = string(C.GoBytes(unsafe.Pointer(&raw.ssid[0]), C.int(ssidLen)))
		ev.Channel = uint8(raw.channel)
	case wifiEventSTADisconnected:
		raw := (*C.wifi_event_sta_disconnected_t)(data)
		ev.Reason = uint8(raw.reason)
	}
	return ev
}
