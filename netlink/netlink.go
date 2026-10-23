package netlink

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/dhcp/dhcpv4"
	"github.com/soypat/lneto/dns"
	"github.com/soypat/lneto/x/xnet"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

const defaultHostname = "tinygo-espradio"

const pollTime = 5 * time.Millisecond

const dhcpTimeout = 10 * time.Second

var pollBackoff = lneto.BackoffStrategy(func(_ uint) time.Duration {
	return pollTime
})

// Esplink implements the Netlinker interface for the ESP32 WiFi interface, using the espradio package and an lneto Netstack.
type Esplink struct {
	params   *nl.ConnectParams
	notifyCb func(nl.Event)

	ns       xnet.Netstack
	dhcpSrv  dhcpv4.Server
	berkeley xnet.StackBerkeley
	started  bool
	link     espradio.Link

	// ArenaPoolSize overrides the default arena pool size (bytes). Zero uses target default.
	ArenaPoolSize int

	// MaxTxPower caps the TX power in units of 0.25 dBm, range 8 to 84. Zero keeps the blob default.
	MaxTxPower int8
}

func (n *Esplink) rstack() xnet.StackRetrying {
	return n.ns.StackAsync().StackRetrying(pollBackoff)
}

// NetConnect device to network
func (n *Esplink) NetConnect(params *nl.ConnectParams) error {
	if len(params.Ssid) == 0 {
		return nl.ErrMissingSSID
	}

	if debug {
		println("Esplink NetConnect: ssid:", params.Ssid)
	}

	n.link.Config = espradio.Config{
		Logging:       espradio.LogLevelError,
		ArenaPoolSize: n.ArenaPoolSize,
		MaxTxPower:    n.MaxTxPower,
	}
	n.link.LinkNotify(n.onLink)
	err := n.link.LinkConnect(espradio.STAConfig{
		SSID:     params.Ssid,
		Password: params.Passphrase,
	})
	if err != nil {
		if debug {
			println("connect failed:", err)
		}
		return err
	}
	if n.started {
		// Reconnect keeps the running stack and only renews the lease.
		return n.setupDHCP()
	}

	if len(params.Hostname) == 0 {
		params.Hostname = defaultHostname
	}
	err = n.startStack(n.link.NetDev(), xnet.StackConfig{
		Hostname:          params.Hostname,
		MaxActiveUDPPorts: 2,
		MaxActiveTCPPorts: 1,
		PassivePeers:      64,
	}, xnet.TCPPoolConfig{
		PoolSize:           4,
		QueueSize:          4,
		TxBufSize:          4096,
		RxBufSize:          1024,
		EstablishedTimeout: 2 * time.Second,
		ClosingTimeout:     2 * time.Second,
		NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
	})
	if err != nil {
		if debug {
			println("stack failed:", err)
		}
		return err
	}
	return n.setupDHCP()
}

// startStack configures the lneto stack on nd and starts running it.
func (n *Esplink) startStack(nd *espradio.NetDev, cfg xnet.StackConfig, pool xnet.TCPPoolConfig) error {
	hw, err := nd.HardwareAddr6()
	if err != nil {
		return err
	}
	cfg.HardwareAddress = hw
	cfg.MTU = 1500
	cfg.RandSeed = time.Now().UnixNano() | 1
	err = n.ns.Reset(xnet.NetstackConfig{
		StackConfig:   cfg,
		BackoffStack:  pollBackoff,
		TCPPoolConfig: pool,
	})
	if err != nil {
		return err
	}
	n.berkeley = *xnet.NewBerkeleyStack(n.ns.StackGo().Socket)
	n.started = true
	go func() {
		err := espradio.RunStack(context.Background(), nd, &n.ns)
		if debug {
			println("RunStack exited:", err.Error())
		}
	}()
	if debug {
		println("Esplink: stack started")
	}
	return nil
}

func (n *Esplink) setupDHCP() error {
	ctx, cancel := context.WithTimeout(context.Background(), dhcpTimeout)
	defer cancel()
	_, _, _, err := n.ns.EnableDHCP(ctx, true, netip.Addr{})
	if err != nil {
		if debug {
			println("DHCP failed:", err)
		}
		return err
	}

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}
	return nil
}

func (n *Esplink) onLink(connected bool) (int, espradio.STAConfig) {
	if !connected && n.notifyCb != nil {
		n.notifyCb(nl.EventNetDown)
	}
	return 0, espradio.STAConfig{}
}

func (n *Esplink) StackGo() *xnet.StackGo       { return n.ns.StackGo() }
func (n *Esplink) StackAsync() *xnet.StackAsync { return n.ns.StackAsync() }

// NetDisconnect device from network
func (n *Esplink) NetDisconnect() {
	n.link.LinkDisconnect()
}

// NetNotify to register callback for network events
func (n *Esplink) NetNotify(cb func(nl.Event)) {
	n.notifyCb = cb
}

// GetHardwareAddr returns device MAC address
func (n *Esplink) GetHardwareAddr() (net.HardwareAddr, error) {
	if debug {
		println("GetHardwareAddr")
	}
	hw := n.ns.HardwareAddr()
	return hw[:], nil
}

// GetHostByName returns the IP address of either a hostname or IPv4
// address in standard dot notation
func (n *Esplink) GetHostByName(name string) (netip.Addr, error) {
	if debug {
		println("GetHostByName:", name)
	}
	if len(name) == 0 {
		if debug {
			println("GetHostByName: empty name")
		}
		return netip.Addr{}, errEmptyHostname
	}
	if addr, literal, err := parseIPLiteral(name); literal {
		return addr, err
	}
	var host dns.Name
	if err := host.Parse(name); err != nil {
		return netip.Addr{}, err
	}
	// Lookups fail when the reply has more addresses than fit, so leave room.
	var addrs [8]netip.Addr
	naddr, err := n.rstack().DoLookupIP(addrs[:], host, 5*time.Second, 3)
	if naddr == 0 {
		if err == nil {
			err = errNoAddress
		}
		if debug {
			println("DNS lookup failed:", err.Error())
		}
		return netip.Addr{}, err
	}
	return addrs[0], nil
}

// NTP gets the clock offset from the NTP server host.
// Pass the offset to runtime.AdjustTimeOffset to set the clock.
func (n *Esplink) NTP(host string, timeout time.Duration, retries int) (time.Duration, error) {
	addr, err := n.GetHostByName(host)
	if err != nil {
		return 0, err
	}
	return n.rstack().DoNTP(addr, timeout, retries)
}

// parseIPLiteral parses name as an IP address. It reports literal true when name
// is an address, or only digits and dots, so that names like 9gag.com go to DNS.
func parseIPLiteral(name string) (addr netip.Addr, literal bool, err error) {
	addr, err = netip.ParseAddr(name)
	if err == nil {
		return addr, true, nil
	}
	if strings.Trim(name, "0123456789.") == "" {
		return netip.Addr{}, true, err
	}
	return netip.Addr{}, false, nil
}

// Addr returns IP address assigned to the interface, either by
// DHCP or statically
func (n *Esplink) Addr() (netip.Addr, error) {
	if debug {
		println("Addr")
	}
	addr4 := n.ns.IPAddr4()
	addr, ok := netip.AddrFromSlice(addr4[:])
	if !ok {
		return netip.Addr{}, errInvalidIPAddress
	}
	return addr, nil
}

// Berkely Sockets-like interface, Go-ified.  See man page for socket(2), etc.
func (n *Esplink) Socket(domain int, stype int, protocol int) (int, error) {
	if debug {
		println("Socket:", domain, stype, protocol)
	}

	return n.berkeley.Socket(domain, stype, protocol)
}

// Bind binds a socket to an IP address and port.
func (n *Esplink) Bind(sockfd int, ip netip.AddrPort) error {
	if debug {
		println("Bind: sockfd", sockfd, "ip", ip.String())
	}

	return n.berkeley.Bind(sockfd, ip)
}

// Connect connects a socket to a remote host and port.
func (n *Esplink) Connect(sockfd int, host string, ip netip.AddrPort) error {
	if debug {
		println("Connect: sockfd", sockfd, "host", host, "ip", ip.String())
	}

	// TinyGo's DialTLS passes the hostname in `host` with an invalid/zero IP,
	// expecting the netdev to resolve it.
	if (!ip.Addr().IsValid() || ip.Addr().IsUnspecified()) && host != "" {
		resolved, err := n.GetHostByName(host)
		if err != nil {
			return err
		}
		ip = netip.AddrPortFrom(resolved, ip.Port())
		if debug {
			println("Connect: resolved", host, "to", ip.String())
		}
	}

	return n.berkeley.Connect(sockfd, host, ip)
}

// Listen marks a socket as listening for incoming connections.
func (n *Esplink) Listen(sockfd int, backlog int) error {
	if debug {
		println("Listen: sockfd", sockfd, "backlog", backlog)
	}

	return n.berkeley.Listen(sockfd, backlog)
}

// Accept accepts a new incoming connection on a listening socket, returning a new socket and the remote address.
func (n *Esplink) Accept(sockfd int) (int, netip.AddrPort, error) {
	if debug {
		println("Accept: sockfd", sockfd)
	}

	return n.berkeley.Accept(sockfd)
}

// Send sends data on a connected socket.
func (n *Esplink) Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	if debug {
		println("Send: sockfd", sockfd, "len", len(buf), "flags", flags, "deadline", deadline.String())
	}

	return n.berkeley.Send(sockfd, buf, flags, deadline)
}

// Recv receives data from a connected socket.
func (n *Esplink) Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	if debug {
		println("Recv: sockfd", sockfd, "len", len(buf), "flags", flags, "deadline", deadline.String())
	}

	return n.berkeley.Recv(sockfd, buf, flags, deadline)
}

// Close closes a socket.
func (n *Esplink) Close(sockfd int) error {
	if debug {
		println("Close: sockfd", sockfd)
	}

	return n.berkeley.Close(sockfd)
}

// SetSockOpt sets a socket option.
func (n *Esplink) SetSockOpt(sockfd int, level int, opt int, value interface{}) error {
	if debug {
		println("SetSockOpt: sockfd", sockfd, "level", level, "opt", opt, "value", value)
	}

	return n.berkeley.SetSockOpt(sockfd, level, opt, value)
}
