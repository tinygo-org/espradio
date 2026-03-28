package netlink

import (
	"net"
	"net/netip"
	"time"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

// Netlinker is TinyGo's OSI L2 data link layer interface.  Network device
// drivers implement Netlinker to expose the device's L2 functionality.

type Esplink struct {
	params   *nl.ConnectParams
	notifyCb func(nl.Event)
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

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	return nil
}

// NetDisconnect device from network
func (n *Esplink) NetDisconnect() {
	// No-op: the blob doesn't have a concept of disconnecting from the network, and
	// the blob's deinit is a no-op, so we don't have anything to do here.
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
	return netip.Addr{}, nil
}

// Addr returns IP address assigned to the interface, either by
// DHCP or statically
func (n *Esplink) Addr() (netip.Addr, error) {
	return netip.Addr{}, nil
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
