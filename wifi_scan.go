package espradio

/*
#include "include.h"
void espradio_event_register_default_cb(void);
*/
import "C"

import (
	"bytes"
	"unsafe"
)

type AccessPoint struct {
	SSID string
	RSSI int
}

// Scan performs a single Wi‑Fi scan pass and returns the list of discovered access points.
// Must be called after Enable.
func Scan() ([]AccessPoint, error) {
	println("wifi_scan: set_mode STA")
	if code := C.esp_wifi_set_mode(C.WIFI_MODE_STA); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_set_mode err", int32(code))
		return nil, makeError(code)
	}

	C.espradio_event_register_default_cb()
	println("wifi_scan: start")
	if code := C.esp_wifi_start(); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_start err", int32(code))
		return nil, makeError(code)
	}

	// Verify that the Wi‑Fi stack considers itself initialized and in the expected mode.
	var mode C.wifi_mode_t
	if code := C.esp_wifi_get_mode(&mode); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_get_mode err", int32(code))
	} else {
		println("wifi_scan: mode after start =", int(mode))
	}

	println("wifi_scan: scan_start")
	if code := C.esp_wifi_scan_start(nil, true); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_scan_start err", int32(code))
		return nil, makeError(code)
	}

	var num C.uint16_t
	println("wifi_scan: get_ap_num")
	if code := C.esp_wifi_scan_get_ap_num(&num); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_scan_get_ap_num err", int32(code))
		return nil, makeError(code)
	}
	println("wifi_scan: ap_num", int(num))
	if num == 0 {
		return nil, nil
	}

	recs := make([]C.wifi_ap_record_t, int(num))
	println("wifi_scan: get_ap_records")
	if code := C.esp_wifi_scan_get_ap_records(
		&num,
		(*C.wifi_ap_record_t)(unsafe.Pointer(&recs[0])),
	); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_scan_get_ap_records err", int32(code))
		return nil, makeError(code)
	}

	aps := make([]AccessPoint, int(num))
	for i := 0; i < int(num); i++ {
		raw := C.GoBytes(unsafe.Pointer(&recs[i].ssid[0]), C.int(len(recs[i].ssid)))
		if idx := bytes.IndexByte(raw, 0); idx >= 0 {
			raw = raw[:idx]
		}
		println("wifi_scan: found AP", string(raw), "RSSI", int(recs[i].rssi))
		aps[i] = AccessPoint{
			SSID: string(raw),
			RSSI: int(recs[i].rssi),
		}
	}

	println("wifi_scan: done")
	return aps, nil
}
