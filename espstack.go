package espradio

import (
	"errors"
	"net/netip"
	"time"

	"github.com/soypat/lneto"
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
	RandSeed      int64
	Hostname      string
	MaxTCPPorts   int
	MaxUDPPorts   int
	PassivePeers  int
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
		DNSServer:         cfg.DNSServer,
		NTPServer:         cfg.NTPServer,
		Hostname:          cfg.Hostname,
		MaxActiveTCPPorts: uint16(cfg.MaxTCPPorts),
		MaxActiveUDPPorts: uint16(cfg.MaxUDPPorts),
		RandSeed:          time.Now().UnixNano() ^ cfg.RandSeed,
		HardwareAddress:   mac,
		MTU:               MTU,
		PassivePeers:      cfg.PassivePeers,
	})
	if err != nil {
		return nil, err
	}
	dev.SetEthRecvHandler(func(pkt []byte) error {
		return stack.s.IngressEthernet(pkt)
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
	recv, errrecv := stack.dev.EthPoll(stack.rxtxBuf)
	if pcapdebug && recv > 0 {
		printPacket("IN", stack.rxtxBuf[:recv])
	}
	send, err = stack.s.EgressEthernet(stack.rxtxBuf)
	if err != nil {
		return send, recv, err
	} else if errrecv != nil {
		err = errrecv
	}
	if send == 0 {
		return send, recv, err
	} else if pcapdebug {
		printPacket("OUT", stack.rxtxBuf[:send])
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
	rstack := lstack.StackRetrying(lneto.BackoffStrategy(func(_ uint) time.Duration {
		return 50 * time.Millisecond
	}))

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
	lstack.SetGatewayHardwareAddr(gatewayHW)
	return dhcpResults, nil
}
