package netlink

import (
	"net"
	"net/netip"
	"time"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

const pollTime = 5 * time.Millisecond

// Esplink implements the Netlinker interface for the ESP32-C3's WiFi interface, using the espradio package and an lneto Stack.
type Esplink struct {
	params   *nl.ConnectParams
	notifyCb func(nl.Event)

	netstack *espradio.Stack
}

// NetConnect device to network
func (n *Esplink) NetConnect(params *nl.ConnectParams) error {
	if len(params.Ssid) == 0 {
		return nl.ErrMissingSSID
	}

	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
	})
	if err != nil {
		println("could not enable radio:", err)
		return err
	}

	println("starting radio...")
	err = espradio.Start()
	if err != nil {
		println("could not start radio:", err)
		return err
	}

	println("connecting to", params.Ssid, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     params.Ssid,
		Password: params.Passphrase,
	})
	if err != nil {
		println("connect failed:", err)
		return err
	}
	println("connected to", params.Ssid, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		println("netdev failed:", err)
		return err
	}

	println("creating lneto stack...")
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    params.Ssid,
		MaxUDPPorts: 2,
		MaxTCPPorts: 1,
	})
	if err != nil {
		println("stack failed:", err)
		return err
	}

	n.netstack = espstack

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	go handleStack(espstack)

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
	return nil, nil
}

// GetHostByName returns the IP address of either a hostname or IPv4
// address in standard dot notation
func (n *Esplink) GetHostByName(name string) (netip.Addr, error) {
	dhcp, err := n.netstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err)
		return netip.Addr{}, err
	}

	lstack := n.netstack.LnetoStack()
	rstack := lstack.StackRetrying(pollTime)
	gatewayHW, err := rstack.DoResolveHardwareAddress6(dhcp.Router, 500*time.Millisecond, 4)
	if err != nil {
		println("ARP resolve failed: " + err.Error())
		return netip.Addr{}, err
	}
	lstack.SetGateway6(gatewayHW)

	addrs, err := rstack.DoLookupIP(name, 5*time.Second, 3)
	if err != nil {
		println("DNS lookup failed:", err)
		return netip.Addr{}, err
	}
	if len(addrs) == 0 {
		println("DNS lookup failed: no addresses found")
		return netip.Addr{}, err
	}

	return addrs[0], nil
}

// Addr returns IP address assigned to the interface, either by
// DHCP or statically
func (n *Esplink) Addr() (netip.Addr, error) {
	dhcp, err := n.netstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err)
		return netip.Addr{}, err
	}

	return dhcp.AssignedAddr, nil
}

// Berkely Sockets-like interface, Go-ified.  See man page for socket(2), etc.
func (n *Esplink) Socket(domain int, stype int, protocol int) (int, error) {
	return 0, nil
}
func (n *Esplink) Bind(sockfd int, ip netip.AddrPort) error {
	return nil
}
func (n *Esplink) Connect(sockfd int, host string, ip netip.AddrPort) error {
	return nil
}

func (n *Esplink) Listen(sockfd int, backlog int) error {
	return nil
}

func (n *Esplink) Accept(sockfd int) (int, netip.AddrPort, error) {
	return 0, netip.AddrPort{}, nil
}

func (n *Esplink) Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	return 0, nil
}

func (n *Esplink) Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	return 0, nil
}

func (n *Esplink) Close(sockfd int) error {
	return nil
}

func (n *Esplink) SetSockOpt(sockfd int, level int, opt int, value interface{}) error {
	return nil
}

func handleStack(stack *espradio.Stack) {
	for {
		send, recv, _ := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
		}
	}
}
