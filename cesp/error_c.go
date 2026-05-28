//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package cesp

/*
#cgo CFLAGS: -fno-short-enums
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#include "espradio.h"
*/
import "C"

func codeToErr(c C.esp_err_t) error {
	if c == C.ESP_OK {
		return nil
	}
	return Error(c)
}

const (
	espErrWifiBase    = int32(C.ESP_ERR_WIFI_BASE)
	espErrMemprotBase = int32(C.ESP_ERR_MEMPROT_BASE)
	espErrHwCrypto    = int32(C.ESP_ERR_HW_CRYPTO_BASE)
	espErrFlash       = int32(C.ESP_ERR_FLASH_BASE)
	espErrMesh        = int32(C.ESP_ERR_MESH_BASE)
	espErrNoMem       = int32(C.ESP_ERR_NO_MEM)
	espErrInvalidArg  = int32(C.ESP_ERR_INVALID_ARG)
	espErrTimeout     = int32(C.ESP_ERR_TIMEOUT)
)
