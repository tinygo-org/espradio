package espradio

/*
#include "include.h"
void espradio_alloc_stats(unsigned *out_alloc, unsigned *out_free);
*/
import "C"

import "strconv"

// Error is an error from the radio stack.
type Error C.esp_err_t

func (e Error) Error() string {
	u := uint32(e)
	if u >= 0x3FC00000 && u <= 0x40400000 {
		return "espradio: unexpected return value 0x" + strconv.FormatUint(uint64(u), 16) + " (looks like pointer, not esp_err_t?)"
	}
	switch {
	case e >= C.ESP_ERR_MEMPROT_BASE:
		return "espradio: memprot error " + strconv.FormatInt(int64(int32(e)), 10)
	case e >= C.ESP_ERR_HW_CRYPTO_BASE:
		return "espradio: unknown hw crypto error"
	case e >= C.ESP_ERR_FLASH_BASE:
		return "espradio: unknown flash error"
	case e >= C.ESP_ERR_MESH_BASE:
		return "espradio: unknown mesh error"
	case e >= C.ESP_ERR_WIFI_BASE:
		code := int32(e)
		switch code {
		case 0x3001: // ESP_ERR_WIFI_NOT_INIT
			return "espradio: wifi not initialized (driver was not installed by esp_wifi_init)"
		case 0x3002: // ESP_ERR_WIFI_NOT_STARTED
			return "espradio: wifi not started (call esp_wifi_start)"
		default:
			return "espradio: wifi error " + strconv.FormatInt(int64(code), 10)
		}
	default:
		switch e {
		case C.ESP_OK:
			return "espradio: no error" // invalid usage of the Error type
		case C.ESP_ERR_NO_MEM:
			var alloc, freed C.uint
			C.espradio_alloc_stats(&alloc, &freed)
			msg := "espradio: no memory (alloc=" + strconv.FormatUint(uint64(alloc), 10) + " free=" + strconv.FormatUint(uint64(freed), 10) + ", leak=" + strconv.FormatUint(uint64(alloc)-uint64(freed), 10) + ")"
			if alloc == freed && alloc <= 4 {
				msg += "; next alloc may be direct calloc() from blob — use blobs/esp32c3.ld in TinyGo (copy to targets/esp32c3.ld or custom -T script) to increase heap"
			}
			return msg
		case C.ESP_ERR_INVALID_ARG:
			return "espradio: invalid argument"
		default:
			return "espradio: unknown error"
		}
	}
}

// makeError returns an error (using the Error type) if the error code is
// non-zero, otherwise it returns nil.
func makeError(errCode C.esp_err_t) error {
	if errCode != 0 {
		return Error(errCode)
	}
	return nil
}

func isPointerLike(errCode C.esp_err_t) bool {
	u := uint32(errCode)
	return u >= 0x3FC00000 && u <= 0x40400000
}
