package netlink

import (
	"net/netip"
	"time"

	"github.com/soypat/lneto"
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
	// connecting clients. Defaults to 192.168.4.0/24 when zero.
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
// default values: StaticAddr defaults to 192.168.4.1, Hostname falls back to the
// AP SSID (or defaultHostname when the SSID is empty), and PassivePeers defaults
// to 255.
func (params APConnectParams) withDefaults() APConnectParams {
	if !params.StaticAddr.IsValid() || !params.StaticAddr.Is4() {
		params.StaticAddr = netip.MustParseAddr("192.168.4.1")
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
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:         params.Hostname,
		StaticAddress:    params.StaticAddr,
		MaxUDPPorts:      udpPorts,
		MaxTCPPorts:      params.MaxTCPPorts,
		PassivePeers:     params.PassivePeers,
		AcceptBroadcast4: true,
	})
	if err != nil {
		if debug {
			println("stack failed:", err)
		}
		return err
	}

	// In AP mode there is no upstream gateway. Set gwmac to a non-broadcast
	// dummy value so patchEgressMAC does not bail out early on its
	// IsBroadcast() check and actually patches the destination MAC from the
	// passively-learned client ARP table.
	espstack.LnetoStack().SetGatewayHardwareAddr([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01})

	if params.EnableDHCPServer {
		subnet := params.DHCPSubnet
		if !subnet.IsValid() {
			subnet = netip.MustParsePrefix("192.168.4.0/24")
		}
		if err := espstack.SetupWithDHCPServer(subnet); err != nil {
			if debug {
				println("Esplink NetConnectAP: DHCP server failed:", err)
			}
			return err
		}
	}

	n.netstack = espstack

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	if debug {
		println("Esplink NetConnectAP: stack started, addr:", params.StaticAddr.String())
	}
	n.stackloop.Do(func() {
		poolCfg := xnet.TCPPoolConfig{
			PoolSize:           2,
			QueueSize:          4,
			TxBufSize:          4096,
			RxBufSize:          1024,
			EstablishedTimeout: 10 * time.Second,
			ClosingTimeout:     5 * time.Second,
			NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
		}
		gostack := n.netstack.LnetoStack().StackGo(pollBackoff, xnet.StackGoConfig{
			ListenerPoolConfig: poolCfg,
		})
		n.tcpPoolSize = int(poolCfg.PoolSize)
		n.berkeley = *xnet.NewBerkeleyStack(gostack.Socket)
		go handleStack(espstack)
	})
	return nil
}
