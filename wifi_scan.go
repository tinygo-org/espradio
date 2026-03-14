package espradio

/*
#include "include.h"
void espradio_ensure_osi_ptr(void);
int espradio_esp_wifi_start(void);
void espradio_event_register_default_cb(void);
static esp_err_t espradio_set_country_eu_manual(void) {
	wifi_country_t c;
	esp_err_t rc = esp_wifi_get_country(&c);
	if (rc != ESP_OK) {
		return rc;
	}
	c.cc[0] = 'E';
	c.cc[1] = 'U';
	c.cc[2] = ' ';
	c.schan = 1;
	c.nchan = 13;
	c.policy = WIFI_COUNTRY_POLICY_MANUAL;
	return esp_wifi_set_country(&c);
}
*/
import "C"

import (
	"bytes"
	"time"
	"unsafe"
)

type AccessPoint struct {
	SSID string
	RSSI int
}

func Start() error {
	var mode C.wifi_mode_t
	if code := C.esp_wifi_get_mode(&mode); code != C.ESP_OK {
		println("wifi_start: esp_wifi_get_mode err", int32(code))
	} else {
		println("wifi_start: mode before start =", int(mode))
	}
	if mode != C.WIFI_MODE_STA {
		println("wifi_start: set_mode STA")
		if code := C.esp_wifi_set_mode(C.WIFI_MODE_STA); code != C.ESP_OK {
			println("wifi_start: esp_wifi_set_mode err", int32(code))
			return makeError(code)
		}
	}

	if code := C.espradio_esp_wifi_start(); code != C.ESP_OK {
		println("wifi_start: esp_wifi_start err", int32(code))
		return makeError(code)
	}

	enableWiFiISR()

	return nil
}

// Scan performs a single Wi‑Fi scan pass and returns the list of discovered access points.
// Must be called after Enable.
func Scan() ([]AccessPoint, error) {
	// C.espradio_event_register_default_cb()
	// println("wifi_scan: start")
	// if code := C.esp_wifi_start(); code != C.ESP_OK {
	// 	println("wifi_scan: esp_wifi_start err", int32(code))
	// 	return nil, makeError(code)
	// }

	// Verify that the Wi‑Fi stack considers itself initialized and in the expected mode.
	println("wifi_scan: get_mode")
	var mode C.wifi_mode_t
	if code := C.esp_wifi_get_mode(&mode); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_get_mode err", int32(code))
	} else {
		println("wifi_scan: mode after start =", int(mode))
	}

	println("wifi_scan: scan_start")
	C.espradio_ensure_osi_ptr()
	if code := C.esp_wifi_set_ps(C.WIFI_PS_NONE); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_set_ps err", int32(code))
	}
	if code := C.espradio_set_country_eu_manual(); code != C.ESP_OK {
		println("wifi_scan: esp_wifi_set_country err", int32(code))
	} else {
		var ctry C.wifi_country_t
		if gc := C.esp_wifi_get_country(&ctry); gc == C.ESP_OK {
			println(
				"wifi_scan: country cc=", int32(ctry.cc[0]), int32(ctry.cc[1]), int32(ctry.cc[2]),
				"schan=", int(ctry.schan), "nchan=", int(ctry.nchan), "policy=", int(ctry.policy),
			)
		}
	}
	time.Sleep(250 * time.Millisecond)
	var scanCfg C.wifi_scan_config_t
	scanCfg.ssid = nil
	scanCfg.bssid = nil
	scanCfg.channel = 0
	scanCfg.show_hidden = false
	scanCfg.scan_type = C.WIFI_SCAN_TYPE_ACTIVE
	scanCfg.scan_time.active.min = 0
	scanCfg.scan_time.active.max = 300
	scanCfg.scan_time.passive = 500
	println(
		"wifi_scan: cfg ch=", int(scanCfg.channel),
		"hidden=", int32(boolToU8(bool(scanCfg.show_hidden))),
		"type=", int(scanCfg.scan_type),
		"active[min,max]=", int(scanCfg.scan_time.active.min), int(scanCfg.scan_time.active.max),
		"passive=", int(scanCfg.scan_time.passive),
	)
	if code := C.esp_wifi_scan_start(&scanCfg, true); code != C.ESP_OK {
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
	println("wifi_scan: get_ap_records returned num=", int(num))

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

func boolToU8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}
