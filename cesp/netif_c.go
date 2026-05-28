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
import "unsafe"

func netifStartRx(apMode int) error {
	return codeToErr(C.espradio_netif_start_rx(C.int(apMode)))
}

func netifTx(buf []byte) error {
	code := C.espradio_netif_tx(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf)))
	return codeToErr(C.esp_err_t(code))
}

func netifRxAvailable() bool {
	return C.espradio_netif_rx_available() != 0
}

func netifRxPop(dst []byte) int {
	return int(C.espradio_netif_rx_pop(unsafe.Pointer(&dst[0]), C.uint16_t(len(dst))))
}

func netifGetMAC() ([6]byte, error) {
	var mac [6]byte
	return mac, codeToErr(C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0]))))
}

func netifRxStats() (cbCount, dropCount uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}

func netifSetConnected(connected bool) {
	v := 0
	if connected {
		v = 1
	}
	C.espradio_netif_set_connected(C.int(v))
}

func netifInitNetstackCB() {
	C.espradio_netif_init_netstack_cb()
}
