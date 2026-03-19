package espradio

/*
#include "espradio.h"
*/
import "C"

import (
	"net"
	"unsafe"
)

var _ EthernetDevice = (*NetDev)(nil)

// NetDev provides raw Ethernet frame I/O over the WiFi STA interface.
type NetDev struct {
	rxHandler func(pkt []byte) error
}

func startNetDev(apMode int) (*NetDev, error) {
	if code := C.espradio_netif_start_rx(C.int(apMode)); code != C.ESP_OK {
		return nil, makeError(code)
	}
	return &NetDev{}, nil
}

// StartNetDev registers the STA RX callback and starts the receive pump.
func StartNetDev() (*NetDev, error) {
	return startNetDev(0)
}

// StartNetDevAP registers the AP RX callback and starts the receive pump.
func StartNetDevAP() (*NetDev, error) {
	return startNetDev(1)
}

func (nd *NetDev) SendEthFrame(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	code := C.espradio_netif_tx(unsafe.Pointer(&frame[0]), C.uint16_t(len(frame)))
	if code != 0 {
		return makeError(C.esp_err_t(code))
	}
	return nil
}

func (nd *NetDev) SetEthRecvHandler(handler func(pkt []byte) error) {
	nd.rxHandler = handler
}

func (nd *NetDev) EthPoll(buf []byte) (bool, error) {
	if C.espradio_netif_rx_available() == 0 {
		return false, nil
	}
	n := C.espradio_netif_rx_pop(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf)))
	if n == 0 {
		return false, nil
	}
	if nd.rxHandler != nil {
		nd.rxHandler(buf[:n])
	}
	return true, nil
}

func (nd *NetDev) HardwareAddr6() (mac [6]byte, _ error) {
	code := C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return mac, makeError(code)
	}
	return mac, nil
}

func (nd *NetDev) MaxFrameSize() int {
	return MaxFrameSize
}

func (nd *NetDev) NetFlags() net.Flags {
	return net.FlagUp | net.FlagBroadcast | net.FlagMulticast
}

// NetifRxStats returns (callback_count, drop_count) from the C ring buffer.
func NetifRxStats() (uint32, uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}
