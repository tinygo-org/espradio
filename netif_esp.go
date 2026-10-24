package espradio

/*
#cgo CFLAGS: -fno-short-enums
#include "espradio.h"
*/
import "C"

import (
	"unsafe"
)

var netDevInstance NetDev

func startNetDev(apMode int) (*NetDev, error) {
	var code C.esp_err_t
	onCore0(func() { code = C.espradio_netif_start_rx(C.int(apMode)) })
	if code != C.ESP_OK {
		return nil, makeError(code)
	}
	netDevInstance = NetDev{}
	return &netDevInstance, nil
}

func (nd *NetDev) sendOffsetEthFrame(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	if pcapdebug {
		printPacket("OUT", frame)
	}
	code := C.espradio_netif_tx(unsafe.Pointer(&frame[0]), C.uint16_t(len(frame)))
	if code != 0 {
		return makeError(C.esp_err_t(code))
	}
	return nil
}

func (nd *NetDev) ethPoll(buf []byte) (ethFrameOff, ethernetBytes int, err error) {
	if C.espradio_netif_rx_available() == 0 {
		return 0, 0, nil
	}
	if nd.rxHandler != nil {
		buf = nd.rxBuf[:]
	} else if len(buf) == 0 {
		return 0, 0, nil
	}
	// Zero means the ring was empty or the frame did not fit, counted as RxOversize.
	n := int(C.espradio_netif_rx_pop(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf))))
	if n == 0 {
		return 0, 0, nil
	}
	if pcapdebug {
		printPacket("IN", buf[:n])
	}
	if nd.rxHandler != nil {
		nd.rxHandler(buf[:n])
		return 0, 0, nil
	}
	return 0, n, nil
}

func (nd *NetDev) hardwareAddr6() (mac [6]byte, _ error) {
	code := C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return mac, makeError(code)
	}
	return mac, nil
}

func netifRxStats() (uint32, uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}
