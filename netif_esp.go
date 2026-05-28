package espradio

import (
	"net"

	"tinygo.org/x/espradio/cesp"
)

var _ EthernetDevice = (*NetDev)(nil)

// NetDev provides raw Ethernet frame I/O over the WiFi STA interface.
type NetDev struct {
	rxHandler func(pkt []byte) error
}

func startNetDev(apMode int) (*NetDev, error) {
	if err := cesp.NetifStartRx(apMode); err != nil {
		return nil, err
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

// SendEthFrame sends a raw Ethernet frame out the WiFi interface.  The frame must
// include the Ethernet header and be at least 60 bytes (including CRC, which is not
// included in the frame).  SendEthFrame returns an error if the frame is too short
// or too long, or if the driver is not ready to send.
func (nd *NetDev) SendEthFrame(frame []byte) error {
	if len(frame) == 0 {
		return nil
	}
	return cesp.NetifTx(frame)
}

// SetEthRecvHandler sets the callback to be called when a new Ethernet frame is received.
func (nd *NetDev) SetEthRecvHandler(handler func(pkt []byte) error) {
	nd.rxHandler = handler
}

// EthPoll checks for a received Ethernet frame and calls the receive handler if one is available.
// EthPoll returns (true, nil) if a frame was received and the handler was called, (false, nil)
// if no frame was available, or (false, err) if an error occurred.
func (nd *NetDev) EthPoll(buf []byte) (int, error) {
	if !cesp.NetifRxAvailable() {
		return 0, nil
	}
	n := cesp.NetifRxPop(buf)
	if n == 0 {
		return 0, nil
	}
	if nd.rxHandler != nil {
		nd.rxHandler(buf[:n])
	}
	return n, nil
}

// HardwareAddr6 returns the 6-byte MAC address of the WiFi interface.
func (nd *NetDev) HardwareAddr6() (mac [6]byte, _ error) {
	return cesp.NetifGetMAC()
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
	return cesp.NetifRxStats()
}
