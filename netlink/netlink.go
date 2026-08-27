package netlink

import (
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/xnet"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

const defaultHostname = "tinygo-espradio"

const pollTime = 5 * time.Millisecond

var pollBackoff = lneto.BackoffStrategy(func(_ uint) time.Duration {
	return pollTime
})

// Esplink implements the Netlinker interface for the ESP32-C3's WiFi interface, using the espradio package and an lneto Stack.
type Esplink struct {
	params   *nl.ConnectParams
	notifyCb func(nl.Event)
	cbMu     sync.Mutex

	ssid     string
	password string

	eventCh      chan espradio.ConnEvent
	eventOnce    sync.Once
	lastReason   uint8
	lastReasonMu sync.Mutex

	netstack  *espradio.Stack
	gostack   xnet.StackGo
	berkeley  xnet.StackBerkeley
	stackloop sync.Once

	// ArenaPoolSize overrides the default arena pool size (bytes). Zero uses target default.
	ArenaPoolSize int
}

func (n *Esplink) rstack() xnet.StackRetrying {
	return n.netstack.LnetoStack().StackRetrying(pollBackoff)
}

// NetConnect device to network
func (n *Esplink) NetConnect(params *nl.ConnectParams) error {
	if len(params.Ssid) == 0 {
		return nl.ErrMissingSSID
	}

	n.ssid = params.Ssid
	n.password = params.Passphrase
	n.wireConnectEvents()

	if debug {
		println("Esplink NetConnect: ssid:", params.Ssid)
	}

	err := espradio.Enable(espradio.Config{
		Logging:       espradio.LogLevelError,
		ArenaPoolSize: n.ArenaPoolSize,
	})
	if err != nil {
		if debug {
			println("could not enable radio:", err)
		}
		return err
	}

	if debug {
		println("Esplink NetConnect: starting radio")
	}
	err = espradio.Start()
	if err != nil {
		if debug {
			println("could not start radio:", err)
		}
		return err
	}

	if debug {
		println("Esplink NetConnect: connecting to WiFi")
	}
	err = espradio.Connect(espradio.STAConfig{
		SSID:     params.Ssid,
		Password: params.Passphrase,
	})
	if err != nil {
		if debug {
			println("connect failed:", err)
		}
		return err
	}

	if debug {
		println("Esplink NetConnect: connected to WiFi, starting stack")
	}
	nd, err := espradio.StartNetDev()
	if err != nil {
		if debug {
			println("netdev failed:", err)
		}
		return err
	}

	if len(params.Hostname) == 0 {
		params.Hostname = defaultHostname
	}
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:     params.Hostname,
		MaxUDPPorts:  2,
		MaxTCPPorts:  1,
		PassivePeers: 64,
	})
	if err != nil {
		if debug {
			println("stack failed:", err)
		}
		return err
	}

	n.netstack = espstack

	n.cbMu.Lock()
	cb := n.notifyCb
	n.cbMu.Unlock()
	if cb != nil {
		cb(nl.EventNetUp)
	}

	if debug {
		println("Esplink NetConnect: stack started")
	}
	n.stackloop.Do(func() {
		// Start stack goroutine once.
		n.gostack = n.netstack.LnetoStack().StackGo(pollBackoff, xnet.StackGoConfig{
			ListenerPoolConfig: xnet.TCPPoolConfig{
				PoolSize:           4,
				QueueSize:          4,
				TxBufSize:          4096,
				RxBufSize:          1024,
				EstablishedTimeout: 2 * time.Second,
				ClosingTimeout:     2 * time.Second,
				NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
			},
		})

		n.berkeley = *xnet.NewBerkeleyStack(n.gostack.Socket)
		go handleStack(espstack)
	})
	_, err = n.netstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		if debug {
			println("DHCP failed:", err)
		}
		return err
	}
	return nil
}

func (n *Esplink) StackGo() xnet.StackGo { return n.gostack }

// NetDisconnect device from network
func (n *Esplink) NetDisconnect() {
	// TODO: implement this.  For now, just do nothing and let the connection time out.
}

// NetNotify to register callback for network events
func (n *Esplink) NetNotify(cb func(nl.Event)) {
	n.cbMu.Lock()
	n.notifyCb = cb
	n.cbMu.Unlock()
	n.wireConnectEvents()
}

// wireConnectEvents registers a non-blocking bridge from the espradio STA
// connect/disconnect events to the netlink notification callback, so a
// runtime drop of the WiFi link surfaces as nl.EventNetDown. It starts a
// single drain goroutine (idempotent via eventOnce) and re-registers the
// espradio channel each time in case the previous registration was cleared.
func (n *Esplink) wireConnectEvents() {
	n.eventOnce.Do(func() {
		n.eventCh = make(chan espradio.ConnEvent, 4)
		go n.drainConnectEvents()
	})
	espradio.SetConnectEventChan(n.eventCh)
}

func (n *Esplink) drainConnectEvents() {
	for ev := range n.eventCh {
		if !ev.Connected {
			n.lastReasonMu.Lock()
			n.lastReason = ev.Reason
			n.lastReasonMu.Unlock()
		}
		n.cbMu.Lock()
		cb := n.notifyCb
		n.cbMu.Unlock()
		if cb == nil {
			continue
		}
		if ev.Connected {
			cb(nl.EventNetUp)
		} else {
			cb(nl.EventNetDown)
		}
	}
}

// LastDisconnectReason returns the WIFI_REASON_* code of the most recent
// STA disconnect, or 0 if none has occurred. Useful for logging why the link
// dropped before reconnecting.
func (n *Esplink) LastDisconnectReason() uint8 {
	n.lastReasonMu.Lock()
	defer n.lastReasonMu.Unlock()
	return n.lastReason
}

// Reconnect re-establishes the WiFi association and DHCP assignment after the
// link dropped, reusing the already-created netstack. It must NOT be called
// from the NetNotify callback directly (that runs in event context); spawn a
// goroutine instead. Only the WiFi association and DHCP are re-run: Enable and
// Start are one-shot and must not be repeated.
func (n *Esplink) Reconnect() error {
	if debug {
		println("Esplink Reconnect: connecting to WiFi")
	}
	if err := espradio.Connect(espradio.STAConfig{
		SSID:     n.ssid,
		Password: n.password,
	}); err != nil {
		if debug {
			println("reconnect failed:", err)
		}
		return err
	}
	if n.netstack == nil {
		return errNotConnected
	}
	if debug {
		println("Esplink Reconnect: acquiring DHCP")
	}
	if _, err := n.netstack.SetupWithDHCP(espradio.DHCPConfig{}); err != nil {
		if debug {
			println("reconnect DHCP failed:", err)
		}
		return err
	}
	n.cbMu.Lock()
	cb := n.notifyCb
	n.cbMu.Unlock()
	if cb != nil {
		cb(nl.EventNetUp)
	}
	return nil
}

// GetHardwareAddr returns device MAC address
func (n *Esplink) GetHardwareAddr() (net.HardwareAddr, error) {
	if debug {
		println("GetHardwareAddr")
	}
	hw := n.netstack.LnetoStack().HardwareAddr()
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
	} else if name[0] >= '0' && name[0] <= '9' {
		// Special case to try for IPv4 addresses.
		addr, err := netip.ParseAddr(name)
		if err != nil {
			return netip.Addr{}, err
		}

		return addr, nil
	}
	rstack := n.rstack()
	addrs, err := rstack.DoLookupIP(name, 5*time.Second, 3)
	if err != nil {
		if debug {
			println("DNS lookup failed:", err)
		}
		return netip.Addr{}, err
	}
	return addrs[0], nil
}

// Addr returns IP address assigned to the interface, either by
// DHCP or statically
func (n *Esplink) Addr() (netip.Addr, error) {
	if debug {
		println("Addr")
	}
	addr4 := n.netstack.LnetoStack().Addr4()
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

func handleStack(stack *espradio.Stack) {
	for {
		send, recv, err := stack.RecvAndSend()
		if err != nil && debug {
			// A TX failure here is already unrecoverable: EgressEthernet dequeued
			// the frame before the send was attempted, so there is nothing left to
			// retry with.  The retry that can still help runs inside
			// espradio_netif_tx, against the blob's TX-done signal.  With debug
			// off, ReadStats is the record: TxFailNoMem, TxFailOther,
			// TxNotConnected.
			println("handleStack: RecvAndSend:", err.Error())
		}
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
		}
	}
}
