package espradio

import (
	"errors"
	"net/netip"
	"time"

	"github.com/soypat/lneto/ethernet"
	"github.com/soypat/lneto/x/xnet"
)

// Stack wraps an lneto async network stack on top of a NetDev (EthernetDevice).
type Stack struct {
	s       xnet.StackAsync
	dev     *NetDev
	rxtxBuf []byte
}

// StackConfig configures the lneto-based network stack.
type StackConfig struct {
	StaticAddress netip.Addr
	DNSServer     netip.Addr
	NTPServer     netip.Addr
	Hostname      string
	MaxTCPPorts   int
	MaxUDPPorts   int
	RandSeed      int64
}

// DHCPConfig configures DHCP address acquisition.
type DHCPConfig struct {
	RequestedAddr netip.Addr
}

// NewStack creates a new lneto-based TCP/IP stack on top of the given NetDev.
// The NetDev must already be started (WiFi joined, StartNetDev called).
func NewStack(dev *NetDev, cfg StackConfig) (*Stack, error) {
	if cfg.Hostname == "" {
		return nil, errors.New("empty hostname")
	}
	mac, err := dev.HardwareAddr6()
	if err != nil {
		return nil, err
	}

	stack := &Stack{dev: dev}
	const MTU = MaxFrameSize - ethernet.MaxOverheadSize + 4 // CRC not included:+4
	err = stack.s.Reset(xnet.StackConfig{
		StaticAddress:   cfg.StaticAddress,
		DNSServer:       cfg.DNSServer,
		NTPServer:       cfg.NTPServer,
		Hostname:        cfg.Hostname,
		MaxTCPConns:     cfg.MaxTCPPorts,
		MaxUDPConns:     cfg.MaxUDPPorts,
		RandSeed:        time.Now().UnixNano() ^ cfg.RandSeed,
		HardwareAddress: mac,
		MTU:             MTU,
	})
	if err != nil {
		return nil, err
	}
	dev.SetEthRecvHandler(func(pkt []byte) error {
		return stack.s.Demux(pkt, 0)
	})
	stack.rxtxBuf = make([]byte, MTU+ethernet.MaxOverheadSize)
	return stack, nil
}

// LnetoStack returns the underlying lneto async stack for advanced use.
func (stack *Stack) LnetoStack() *xnet.StackAsync {
	return &stack.s
}

// Hostname returns the hostname configured on the stack.
func (stack *Stack) Hostname() string {
	return stack.s.Hostname()
}

// RecvAndSend polls the device for received frames and sends any pending
// outgoing frames. Returns the number of bytes sent and received.
func (stack *Stack) RecvAndSend() (send, recv int, err error) {
	gotRecv, errrecv := stack.dev.EthPoll(stack.rxtxBuf)
	if gotRecv {
		recv = 1 // At least one frame was processed.
	}

	send, err = stack.s.Encapsulate(stack.rxtxBuf, -1, 0)
	if err != nil {
		return send, recv, err
	} else if errrecv != nil {
		err = errrecv
	}
	if send == 0 {
		return send, recv, err
	}

	err = stack.dev.SendEthFrame(stack.rxtxBuf[:send])
	return send, recv, err
}

// SetupWithDHCP performs DHCPv4 to obtain an IP address and configures the
// stack with the results. Blocks until complete or timeout.
func (stack *Stack) SetupWithDHCP(cfg DHCPConfig) (*xnet.DHCPResults, error) {
	var reqaddr [4]byte
	if cfg.RequestedAddr.IsValid() {
		if !cfg.RequestedAddr.Is4() {
			return nil, errors.New("IPv6 DHCP unsupported")
		}
		reqaddr = cfg.RequestedAddr.As4()
	}

	lstack := stack.LnetoStack()
	const pollTime = 50 * time.Millisecond
	rstack := lstack.StackRetrying(pollTime)

	dhcpResults, err := rstack.DoDHCPv4(reqaddr, 3*time.Second, 3)
	if err != nil {
		return dhcpResults, err
	}
	err = lstack.AssimilateDHCPResults(dhcpResults)
	if err != nil {
		return dhcpResults, err
	}

	gatewayHW, err := rstack.DoResolveHardwareAddress6(dhcpResults.Router, 500*time.Millisecond, 4)
	if err != nil {
		return dhcpResults, err
	}
	lstack.SetGateway6(gatewayHW)
	return dhcpResults, nil
}
