package netlink

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"time"

	"github.com/soypat/lneto/x/xnet"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

const pollTime = 5 * time.Millisecond

// Esplink implements the Netlinker interface for the ESP32-C3's WiFi interface, using the espradio package and an lneto Stack.
type Esplink struct {
	params   *nl.ConnectParams
	notifyCb func(nl.Event)

	netstack  *espradio.Stack
	berkeley  xnet.StackBerkeley
	stackloop sync.Once

	// ArenaPoolSize overrides the default arena pool size (bytes). Zero uses target default.
	ArenaPoolSize int
}

func (n *Esplink) rstack() xnet.StackRetrying {
	return n.netstack.LnetoStack().StackRetrying(pollTime)
}

// NetConnect device to network
func (n *Esplink) NetConnect(params *nl.ConnectParams) error {
	if len(params.Ssid) == 0 {
		return nl.ErrMissingSSID
	}

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

	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    params.Ssid,
		MaxUDPPorts: 2,
		MaxTCPPorts: 1,
	})
	if err != nil {
		if debug {
			println("stack failed:", err)
		}
		return err
	}

	n.netstack = espstack

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	if debug {
		println("Esplink NetConnect: stack started")
	}
	n.stackloop.Do(func() {
		// Start stack goroutine once.
		gostack := n.netstack.LnetoStack().StackGo(pollTime, xnet.StackGoConfig{
			ListenerPoolConfig: xnet.TCPPoolConfig{
				PoolSize:           2,
				QueueSize:          4,
				TxBufSize:          4096,
				RxBufSize:          1024,
				EstablishedTimeout: 2 * time.Second,
				ClosingTimeout:     2 * time.Second,
			},
		})
		n.berkeley = *xnet.NewBerkeleyStack(gostack.Socket)
		go handleStack(espstack)
	})
	err = n.doDHCP()
	if err != nil {
		if debug {
			println("debug: doDHCP failed:", err.Error())
		}
		return err
	}
	return nil
}

func (n *Esplink) doDHCP() error {
	_, err := n.netstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		if debug {
			println("DHCP failed:", err)
		}
		return err
	}
	return nil
}

// NetDisconnect device from network
func (n *Esplink) NetDisconnect() {
	// TODO: implement this.  For now, just do nothing and let the connection time out.
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
	hw := n.netstack.LnetoStack().HardwareAddress()
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
		return netip.Addr{}, errors.New("empty name")
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
	return n.netstack.LnetoStack().Addr(), nil
}

// Berkely Sockets-like interface, Go-ified.  See man page for socket(2), etc.
func (n *Esplink) Socket(domain int, stype int, protocol int) (int, error) {
	if debug {
		println("Socket:", domain, stype, protocol)
	}

	return n.berkeley.Socket(domain, stype, protocol)
}

func (n *Esplink) Bind(sockfd int, ip netip.AddrPort) error {
	if debug {
		println("Bind: sockfd", sockfd, "ip", ip.String())
	}

	return n.berkeley.Bind(sockfd, ip)
}

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

func (n *Esplink) Listen(sockfd int, backlog int) error {
	if debug {
		println("Listen: sockfd", sockfd, "backlog", backlog)
	}

	return n.berkeley.Listen(sockfd, backlog)
}

func (n *Esplink) Accept(sockfd int) (int, netip.AddrPort, error) {
	if debug {
		println("Accept: sockfd", sockfd)
	}

	return n.berkeley.Accept(sockfd)
}

func (n *Esplink) Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	if debug {
		println("Send: sockfd", sockfd, "len", len(buf), "flags", flags, "deadline", deadline.String())
	}

	return n.berkeley.Send(sockfd, buf, flags, deadline)
}

func (n *Esplink) Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	if debug {
		println("Recv: sockfd", sockfd, "len", len(buf), "flags", flags, "deadline", deadline.String())
	}

	return n.berkeley.Recv(sockfd, buf, flags, deadline)
}

func (n *Esplink) Close(sockfd int) error {
	if debug {
		println("Close: sockfd", sockfd)
	}

	return n.berkeley.Close(sockfd)
}

func (n *Esplink) SetSockOpt(sockfd int, level int, opt int, value interface{}) error {
	if debug {
		println("SetSockOpt: sockfd", sockfd, "level", level, "opt", opt, "value", value)
	}

	return n.berkeley.SetSockOpt(sockfd, level, opt, value)
}

func handleStack(stack *espradio.Stack) {
	for {
		send, recv, _ := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
			runtime.Gosched()
		}
	}
}
