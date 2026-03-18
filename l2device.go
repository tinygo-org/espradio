package espradio

import "net"

const EthMTU = 1500

// L2Device is the interface a TCP/IP stack needs from a link-layer device.
//
//	espradio.L2Device  (L2: raw Ethernet frames)
//	       ↓
//	  NetStack          (L3/L4: ARP, DHCP, IP, TCP, UDP, DNS)
//	       ↓
//	  implements Netdever → Use() → net.Dial / http.Get work
type L2Device interface {
	SendEth(frame []byte) error
	RecvCh() <-chan []byte
	HardwareAddr() ([6]byte, error)
	MTU() int
}

// EthernetDevice is WIP of how ethernet device
// API design.
//
// Device-specific initialization (WiFi join, PHY auto-negotiation,
// firmware loading) must complete BEFORE the device is used as a stack endpoint.
type EthernetDevice interface {
	// SendEthFrame transmits a complete Ethernet frame.
	// The frame includes the Ethernet header but NOT the FCS/CRC
	// trailer (device or stack handles CRC as appropriate).
	// SendEthFrame blocks until the transmission is queued succesfully
	// or finished sending. Should not be called concurrently
	// unless user is sure the driver supports it.
	SendEthFrame(frame []byte) error

	// SetRecvHandler registers the function called when an Ethernet
	// frame is received. After the callback returns the buffer is reused.
	// The callback may or may not be called from an interrupt context
	// so the callback should return fast, ideally copy the packet
	// to a buffer to be processed outside the ISR.
	//
	// We don't use a channel for several reasons:
	//  - Near impossible for channel sender to know lifetime of the buffer;
	//    when is it finished being used?
	//  - Hard to determine best "channel full" semantics
	SetEthRecvHandler(handler func(pkt []byte) error)

	// EthPoll services the device. For poll-based devices (e.g. CYW43439
	// over SPI), reads from the bus and invokes the handler for each
	// received frame. Behaviour for interrupt driven devices is undefined
	// at the moment.
	EthPoll() (bool, error)

	// HardwareAddr6 returns the device's 6-byte MAC address.
	// For PHY-only devices, returns the MAC provided at configuration.
	HardwareAddr6() ([6]byte, error)

	// MaxFrameSize returns the max complete Ethernet frame size
	// (including headers and any overhead) for buffer allocation.
	// MTU can be calculated doing:
	//  // mfu-(14+4+4) for:
	//  // ethernet header+ethernet CRC if present+ethernet VLAN overhead for VLAN support.
	//  mtu := dev.MaxFrameSize() - ethernet.MaxOverheadSize
	MaxFrameSize() int

	// NetFlags offers ability to provide user with notice of the device state.
	// May be also used to encode functioning such as if the device needs FCS/CRC encoding appended
	// to the ethernet packet. WIP.
	NetFlags() net.Flags
}
