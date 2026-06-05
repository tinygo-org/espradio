// This example shows how to set up an AP with a DHCP server. Connect to the AP with the SSID and password
// defined in the ssid and password variables, and you should get an IP address in the 192.168.4.x range.
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" ./examples/ap
package main

import (
	"net/netip"
	"time"

	"github.com/soypat/lneto/dhcp/dhcpv4"
	"github.com/soypat/lneto/ipv4"
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
		AuthOpen: true,
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

	println("ap: creating lneto stack...")
	stack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:      ssid,
		StaticAddress: addr,
		MaxUDPPorts:   2,
	})
	if err != nil {
		failure("ap: stack err: " + err.Error())
	}

	println("ap: configuring DHCP server...")
	err = dhcpServer.Configure(dhcpv4.ServerConfig{
		ServerAddr: addr.As4(),
		Gateway:    addr.As4(),
		Subnet:     ipv4.PrefixFromNetip(subnet),
	})
	if err != nil {
		failure("ap: dhcp server configure err: " + err.Error())
	}

	err = stack.LnetoStack().RegisterUDP4(&dhcpServer, addr.As4(), dhcpv4.DefaultClientPort)
	if err != nil {
		failure("ap: dhcp server register err: " + err.Error())
	}

	println("ap: AP is running on", apIP, "- connect to", ssid)
	go stackLoop(stack)
	for {
		time.Sleep(1 * time.Second)
		rxCb, rxDrop := espradio.NetifRxStats()
		println("ap: rx_cb=", rxCb, "rx_drop=", rxDrop)
	}
}

func stackLoop(stack *espradio.Stack) {
	for {
		send, recv, err := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		if err != nil {
			println("poll err:", err.Error())
		}
		_ = send
		_ = recv
	}
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
