// This example shows how to set up an AP with a DHCP server. Connect to the AP with the SSID and password
// defined in the ssid and password variables, and you should get an IP address in the 192.168.4.x range.
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" ./examples/ap
package main

import (
	"context"
	"net/netip"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/dhcp/dhcpv4"
	"github.com/soypat/lneto/ipv4"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

var dhcpServer dhcpv4.Server

func main() {
	time.Sleep(time.Second)

	println("ap: enabling radio...")
	if err := espradio.Enable(espradio.Config{Logging: espradio.LogLevelError}); err != nil {
		failure("ap: enable err: " + err.Error())
	}

	println("ap: starting AP...")
	err := espradio.StartAP(espradio.APConfig{
		SSID:     ssid,
		Password: password,
		Channel:  6,
	})
	if err != nil {
		failure("ap: start err: " + err.Error())
	}

	println("ap: starting L2 netdev (AP)...")
	nd, err := espradio.StartNetDevAP()
	if err != nil {
		failure("ap: netdev err: " + err.Error())
	}

	const apIP = "192.168.4.1"
	addr := netip.MustParseAddr(apIP)
	subnet := netip.MustParsePrefix("192.168.4.0/24")

	hw, err := nd.HardwareAddr6()
	if err != nil {
		failure("ap: mac err: " + err.Error())
	}

	println("ap: creating lneto stack...")
	var stack xnet.Netstack
	err = stack.Reset(xnet.NetstackConfig{
		StackConfig: xnet.StackConfig{
			Hostname:            ssid,
			RandSeed:            time.Now().UnixNano() | 1,
			HardwareAddress:     hw,
			MTU:                 1500,
			StaticAddress4:      addr.As4(),
			MaxActiveUDPPorts:   2,
			AcceptIPv4Broadcast: true,
		},
		BackoffStack: backoff,
		TCPPoolConfig: xnet.TCPPoolConfig{
			NewBackoff: func() lneto.BackoffStrategy { return backoff },
		},
	})
	if err != nil {
		failure("ap: stack err: " + err.Error())
	}
	stack.StackAsync().SetSubnet4(addr.As4(), uint8(subnet.Bits()))

	println("ap: configuring DHCP server...")
	err = dhcpServer.Configure(dhcpv4.ServerConfig{
		ServerAddr: addr.As4(),
		Gateway:    addr.As4(),
		Subnet:     ipv4.PrefixFromNetip(subnet),
	})
	if err != nil {
		failure("ap: dhcp server configure err: " + err.Error())
	}

	// A zero remote address accepts clients that have no IP yet.
	err = stack.StackAsync().RegisterUDP4(&dhcpServer, [4]byte{}, dhcpv4.DefaultClientPort)
	if err != nil {
		failure("ap: dhcp server register err: " + err.Error())
	}

	println("ap: AP is running on", apIP, "- connect to", ssid)
	go func() {
		failure("ap: stack stopped: " + espradio.RunStack(context.Background(), nd, &stack).Error())
	}()
	for {
		time.Sleep(1 * time.Second)
		rxCb, rxDrop := espradio.NetifRxStats()
		println("ap: rx_cb=", rxCb, "rx_drop=", rxDrop)
	}
}

func backoff(uint) time.Duration { return 5 * time.Millisecond }

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
