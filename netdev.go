package espradio

import "github.com/soypat/lneto/x/netdev"

var _ netdev.DevEthernet = (*NetDev)(nil)

// MaxFrameSize is the largest Ethernet frame NetDev handles, including the header.
const MaxFrameSize = 1518

// NetDev provides raw Ethernet frame I/O over the WiFi interface.
type NetDev struct {
	rxHandler func(rxEthframe []byte)
	rxBuf     [MaxFrameSize]byte
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
	if !onCore0Direct() {
		var err error
		onCore0(func() { err = nd.sendOffsetEthFrame(frame) })
		return err
	}
	return nd.sendOffsetEthFrame(frame)
}

// SetEthRecvHandler sets the callback that EthPoll calls for each received frame,
// as in [netdev.DevEthernet]. It only runs inside EthPoll, so it is quiescent once this returns.
func (nd *NetDev) SetEthRecvHandler(handler func(rxEthframe []byte)) {
	nd.rxHandler = handler
}

// EthPoll pops one received frame following [netdev.DevEthernet]. With no handler the
// frame goes into buf. With a handler buf may be nil and is unused, and the handler gets the frame.
func (nd *NetDev) EthPoll(buf []byte) (ethFrameOff, ethernetBytes int, err error) {
	if !onCore0Direct() {
		onCore0(func() { ethFrameOff, ethernetBytes, err = nd.ethPoll(buf) })
		return
	}
	return nd.ethPoll(buf)
}

// HardwareAddr6 returns the 6-byte MAC address of the WiFi interface.
func (nd *NetDev) HardwareAddr6() (v [6]byte, err error) {
	onCore0(func() { v, err = nd.hardwareAddr6() })
	return
}

// MaxFrameSizeAndOffset returns the maximum Ethernet frame size including the
// header and the frame offset, which is always 0.
func (nd *NetDev) MaxFrameSizeAndOffset() (maxFrameSize, frameOff int) {
	return MaxFrameSize, 0
}

// NetifRxStats returns (callback_count, drop_count) from the C ring buffer.
func NetifRxStats() (uint32, uint32) {
	return netifRxStats()
}
