package netlink

import (
	"net/netip"
	"time"

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
	// Hostname for the network stack. Defaults to APConfig.SSID.
	Hostname     string
	MaxUDPPorts  int
	MaxTCPPorts  int
	PassivePeers int
}

// NetConnectAP starts the device as a soft access point with a static IP address.
// Clients must be configured with static addresses in the same subnet.
func (n *Esplink) NetConnectAP(params APConnectParams) error {
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
		params.PassivePeers = 255
	}

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

	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:      params.Hostname,
		StaticAddress: params.StaticAddr,
		MaxUDPPorts:   params.MaxUDPPorts,
		MaxTCPPorts:   params.MaxTCPPorts,
		PassivePeers:  params.PassivePeers,
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

	n.netstack = espstack

	if n.notifyCb != nil {
		n.notifyCb(nl.EventNetUp)
	}

	if debug {
		println("Esplink NetConnectAP: stack started, addr:", params.StaticAddr.String())
	}
	n.stackloop.Do(func() {
		gostack := n.netstack.LnetoStack().StackGo(pollBackoff, xnet.StackGoConfig{
			ListenerPoolConfig: xnet.TCPPoolConfig{
				PoolSize:           2,
				QueueSize:          4,
				TxBufSize:          4096,
				RxBufSize:          1024,
				EstablishedTimeout: 10 * time.Second,
				ClosingTimeout:     5 * time.Second,
			},
		})
		n.berkeley = *xnet.NewBerkeleyStack(gostack.Socket)
		go handleStack(espstack)
	})
	return nil
}
