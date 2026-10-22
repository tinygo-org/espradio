package netlink

import (
	"net/netip"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/dhcp/dhcpv4"
	"github.com/soypat/lneto/ipv4"
	"github.com/soypat/lneto/x/xnet"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
)

// APConnectParams configures AP mode for NetConnectAP.
type APConnectParams struct {
	// APConfig sets the AP SSID, password, channel, and auth mode.
	APConfig espradio.APConfig
	// StaticAddr is the IP address assigned to the AP interface.
	// Defaults to 192.168.4.1 if not set.
	StaticAddr netip.Addr
	// DHCPSubnet is the subnet used by the DHCP server to assign addresses to
	// connecting clients. Defaults to the /24 that holds StaticAddr.
	// Ignored when EnableDHCPServer is false.
	DHCPSubnet netip.Prefix
	// EnableDHCPServer enables the built-in DHCPv4 server so connecting
	// clients receive IP addresses automatically. Set to true for most
	// deployments; set to false if clients use static addresses.
	EnableDHCPServer bool
	// Hostname for the network stack. Defaults to APConfig.SSID.
	Hostname     string
	MaxUDPPorts  int
	MaxTCPPorts  int
	PassivePeers int
}

// withDefaults returns a copy of params with unset fields populated with their
// default values. StaticAddr defaults to 192.168.4.1, DHCPSubnet to the /24 that
// holds StaticAddr, Hostname to the SSID or defaultHostname, PassivePeers to 64.
func (params APConnectParams) withDefaults() APConnectParams {
	if !params.StaticAddr.IsValid() || !params.StaticAddr.Is4() {
		params.StaticAddr = netip.MustParseAddr("192.168.4.1")
	}
	if !params.DHCPSubnet.IsValid() {
		params.DHCPSubnet = netip.PrefixFrom(params.StaticAddr, 24).Masked()
	}
	if params.Hostname == "" {
		if params.APConfig.SSID != "" {
			params.Hostname = params.APConfig.SSID
		} else {
			params.Hostname = defaultHostname
		}
	}
	if params.PassivePeers == 0 {
		params.PassivePeers = 64
	}
	return params
}

// NetConnectAP starts the device as a soft access point with a static IP address.
// If EnableDHCPServer is true, clients are assigned addresses from DHCPSubnet,
// otherwise clients must use static addresses in the same subnet.
func (n *Esplink) NetConnectAP(params APConnectParams) error {
	params = params.withDefaults()

	if debug {
		println("Esplink NetConnectAP: ssid:", params.APConfig.SSID)
	}

	err := espradio.Enable(espradio.Config{
		Logging:       espradio.LogLevelError,
		ArenaPoolSize: n.ArenaPoolSize,
		MaxTxPower:    n.MaxTxPower,
	})
	if err != nil {
		if debug {
			println("could not enable radio:", err)
		}
		return err
	}

	if debug {
		println("Esplink NetConnectAP: starting AP")
	}
	err = espradio.StartAP(params.APConfig)
	if err != nil {
		if debug {
			println("could not start AP:", err)
		}
		return err
	}

	if debug {
		println("Esplink NetConnectAP: starting netdev (AP)")
	}
	nd, err := espradio.StartNetDevAP()
	if err != nil {
		if debug {
			println("netdev failed:", err)
		}
		return err
	}

	udpPorts := params.MaxUDPPorts
	if params.EnableDHCPServer && udpPorts < 1 {
		udpPorts = 1 // reserve one slot for the DHCP server
	}
	err = n.startStack(nd, xnet.StackConfig{
		Hostname:            params.Hostname,
		StaticAddress4:      params.StaticAddr.As4(),
		MaxActiveUDPPorts:   uint16(udpPorts),
		MaxActiveTCPPorts:   uint16(params.MaxTCPPorts),
		PassivePeers:        params.PassivePeers,
		AcceptIPv4Broadcast: true,
	}, xnet.TCPPoolConfig{
		PoolSize:           2,
		QueueSize:          4,
		TxBufSize:          4096,
		RxBufSize:          1024,
		EstablishedTimeout: 10 * time.Second,
		ClosingTimeout:     5 * time.Second,
		NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
	})
	if err != nil {
		if debug {
			println("stack failed:", err)
		}
		return err
	}
	stack := n.ns.StackAsync()
	stack.SetSubnet4(params.StaticAddr.As4(), 24)

	// In AP mode there is no upstream gateway. Set gwmac to a non-broadcast
	// dummy value so patchEgressMAC does not bail out early on its
	// IsBroadcast() check and actually patches the destination MAC from the
	// passively-learned client ARP table.
	stack.SetGatewayHardwareAddr([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01})

	if params.EnableDHCPServer {
		if err := n.startDHCPServer(params.StaticAddr, params.DHCPSubnet); err != nil {
			if debug {
				println("Esplink NetConnectAP: DHCP server failed:", err)
			}
			return err
		}
	}

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	if debug {
		println("Esplink NetConnectAP: stack started, addr:", params.StaticAddr.String())
	}
	return nil
}

// startDHCPServer serves DHCPv4 leases from subnet.
func (n *Esplink) startDHCPServer(serverAddr netip.Addr, subnet netip.Prefix) error {
	err := n.dhcpSrv.Configure(dhcpv4.ServerConfig{
		ServerAddr: serverAddr.As4(),
		Gateway:    serverAddr.As4(),
		Subnet:     ipv4.PrefixFromNetip(subnet),
	})
	if err != nil {
		return err
	}
	// A zero remote address accepts clients that have no IP yet.
	return n.ns.StackAsync().RegisterUDP4(&n.dhcpSrv, [4]byte{}, dhcpv4.DefaultClientPort)
}
