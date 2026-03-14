//go:build esp32c3 || esp32 || esp32s3

package espradio

/*
#include "espradio.h"
*/
import "C"

import (
	"time"
	"unsafe"
)

var _ L2Device = (*NetDev)(nil)

// NetDev provides raw Ethernet frame I/O over the WiFi STA interface.
type NetDev struct {
	rxCh chan []byte
	done chan struct{}
}

func startNetDev(apMode int) (*NetDev, error) {
	if code := C.espradio_netif_start_rx(C.int(apMode)); code != C.ESP_OK {
		return nil, makeError(code)
	}
	nd := &NetDev{
		rxCh: make(chan []byte, 8),
		done: make(chan struct{}),
	}
	go nd.rxPump()
	return nd, nil
}

// StartNetDev registers the STA RX callback and starts the receive pump.
func StartNetDev() (*NetDev, error) {
	return startNetDev(0)
}

// StartNetDevAP registers the AP RX callback and starts the receive pump.
func StartNetDevAP() (*NetDev, error) {
	return startNetDev(1)
}

func (nd *NetDev) rxPump() {
	buf := make([]byte, 1600)
	for {
		select {
		case <-nd.done:
			return
		default:
		}
		if C.espradio_netif_rx_available() == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		n := C.espradio_netif_rx_pop(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf)))
		if n == 0 {
			continue
		}
		frame := make([]byte, int(n))
		copy(frame, buf[:n])
		select {
		case nd.rxCh <- frame:
		default:
		}
	}
}

func (nd *NetDev) RecvEth() ([]byte, error) {
	frame, ok := <-nd.rxCh
	if !ok {
		return nil, Error(C.ESP_ERR_INVALID_STATE)
	}
	return frame, nil
}

func (nd *NetDev) RecvCh() <-chan []byte {
	return nd.rxCh
}

func (nd *NetDev) SendEth(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	code := C.espradio_netif_tx(unsafe.Pointer(&frame[0]), C.uint16_t(len(frame)))
	if code != 0 {
		return makeError(C.esp_err_t(code))
	}
	return nil
}

func (nd *NetDev) HardwareAddr() ([6]byte, error) {
	var mac [6]byte
	code := C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return mac, makeError(code)
	}
	return mac, nil
}

func (nd *NetDev) MTU() int {
	return EthMTU
}

// NetifRxStats returns (callback_count, drop_count) from the C ring buffer.
func NetifRxStats() (uint32, uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}

func (nd *NetDev) Close() {
	select {
	case <-nd.done:
	default:
		close(nd.done)
	}
}
