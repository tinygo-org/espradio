package espradio

/*
#cgo CFLAGS: -fno-short-enums
#include "espradio.h"
*/
import "C"

import (
	"unsafe"

	"github.com/soypat/lneto/x/netdev"
)

var _ netdev.DevEthernet = (*NetDev)(nil)

// MaxFrameSize is the largest Ethernet frame NetDev handles, including the header.
const MaxFrameSize = 1518

// NetDev provides raw Ethernet frame I/O over the WiFi interface.
type NetDev struct {
	rxHandler func(rxEthframe []byte)
	rxBuf     [MaxFrameSize]byte
}

var netDevInstance NetDev

func startNetDev(apMode int) (*NetDev, error) {
	if code := C.espradio_netif_start_rx(C.int(apMode)); code != C.ESP_OK {
		return nil, makeError(code)
	}
	netDevInstance = NetDev{}
	return &netDevInstance, nil
}

// StartNetDev registers the STA RX callback and starts the receive pump.
func StartNetDev() (*NetDev, error) {
	return startNetDev(0)
}

// StartNetDevAP registers the AP RX callback and starts the receive pump.
func StartNetDevAP() (*NetDev, error) {
	return startNetDev(1)
}

// SendOffsetEthFrame sends a raw Ethernet frame out the WiFi interface. The frame
// starts at offset 0, includes the Ethernet header and excludes the CRC.
func (nd *NetDev) SendOffsetEthFrame(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	code := C.espradio_netif_tx(unsafe.Pointer(&frame[0]), C.uint16_t(len(frame)))
	if code != 0 {
		return makeError(C.esp_err_t(code))
	}
	return nil
}

// SetEthRecvHandler sets the callback that EthPoll calls for each received frame.
// The handler only runs inside EthPoll, so it is quiescent once this returns.
func (nd *NetDev) SetEthRecvHandler(handler func(rxEthframe []byte)) {
	nd.rxHandler = handler
}

// EthPoll pops one received frame. With no handler set the frame is written to buf
// and its length returned. With a handler set buf is unused and the handler gets the frame.
func (nd *NetDev) EthPoll(buf []byte) (ethFrameOff, ethernetBytes int, err error) {
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
	if nd.rxHandler != nil {
		nd.rxHandler(buf[:n])
		return 0, 0, nil
	}
	return 0, n, nil
}

// rxIngressErrors counts frames the upper stack declined to handle.
var rxIngressErrors uint32

// HardwareAddr6 returns the 6-byte MAC address of the WiFi interface.
func (nd *NetDev) HardwareAddr6() (mac [6]byte, _ error) {
	code := C.espradio_netif_get_mac((*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return mac, makeError(code)
	}
	return mac, nil
}

// MaxFrameSizeAndOffset returns the maximum Ethernet frame size including the
// header and the frame offset, which is always 0.
func (nd *NetDev) MaxFrameSizeAndOffset() (maxFrameSize, frameOff int) {
	return MaxFrameSize, 0
}

// NetifRxStats returns (callback_count, drop_count) from the C ring buffer.
func NetifRxStats() (uint32, uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}
