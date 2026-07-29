package espradio

/*
#cgo CFLAGS: -fno-short-enums
#include "espradio.h"
*/
import "C"

import (
	"net"
	"sync/atomic"
	"unsafe"
)

var _ EthernetDevice = (*NetDev)(nil)

// NetDev provides raw Ethernet frame I/O over the WiFi STA interface.
type NetDev struct {
	rxHandler func(pkt []byte) error
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

// SendEthFrame sends a raw Ethernet frame out the WiFi interface.  The frame must
// include the Ethernet header and be at least 60 bytes (including CRC, which is not
// included in the frame).  SendEthFrame returns an error if the frame is too short
// or too long, or if the driver is not ready to send.
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

// SetEthRecvHandler sets the callback to be called when a new Ethernet frame is received.
func (nd *NetDev) SetEthRecvHandler(handler func(pkt []byte) error) {
	nd.rxHandler = handler
}

// EthPoll checks for a received Ethernet frame and calls the receive handler if one is available.
// EthPoll returns (true, nil) if a frame was received and the handler was called, (false, nil)
// if no frame was available, or (false, err) if an error occurred.
func (nd *NetDev) EthPoll(buf []byte) (int, error) {
	if C.espradio_netif_rx_available() == 0 {
		return 0, nil
	}
	n := C.espradio_netif_rx_pop(unsafe.Pointer(&buf[0]), C.uint16_t(len(buf)))
	if n == 0 {
		// Either the ring was empty, or the frame did not fit and was dropped
		// whole rather than truncated.  Both mean "no frame for this caller";
		// the drop is counted in DebugStats as RxOversize.
		return 0, nil
	}
	if nd.rxHandler != nil {
		// Count rather than propagate.  The handler is the upper stack's ingress
		// path, and it returns an error for any frame the stack does not handle --
		// IPv6, STP, stray broadcast -- which is ordinary traffic for a netdev, not
		// a device failure.  Returning it here would report EthPoll as failing on a
		// call where it successfully delivered a frame.
		//
		// The original defect was that these were invisible, not that they were
		// unpropagated; DebugStats().RxIngressErrors fixes that without giving
		// normal traffic an error path.
		if err := nd.rxHandler(buf[:n]); err != nil {
			atomic.AddUint32(&rxIngressErrors, 1)
		}
	}
	return int(n), nil
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

// MaxFrameSize returns the maximum Ethernet frame size supported by the driver,
// including the Ethernet header but excluding CRC.
func (nd *NetDev) MaxFrameSize() int {
	return MaxFrameSize
}

// NetFlags returns the network interface flags for this device.  The flags indicate
// that the interface is up and supports broadcast and multicast.
func (nd *NetDev) NetFlags() net.Flags {
	return net.FlagUp | net.FlagBroadcast | net.FlagMulticast
}

// NetifRxStats returns (callback_count, drop_count) from the C ring buffer.
func NetifRxStats() (uint32, uint32) {
	return uint32(C.espradio_netif_rx_cb_count()), uint32(C.espradio_netif_rx_cb_drop())
}
