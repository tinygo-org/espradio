package main

import (
	"net/netip"
	"time"

	"github.com/soypat/lneto/dhcpv4"
	"tinygo.org/x/espradio"
)

var dhcpServer dhcpv4.Server

func main() {
	time.Sleep(time.Second)

	println("ap: enabling radio...")
	if err := espradio.Enable(espradio.Config{Logging: espradio.LogLevelInfo}); err != nil {
		println("ap: enable err:", err)
		return
	}

	println("ap: starting AP...")
	err := espradio.StartAP(espradio.APConfig{
		SSID:     "espradio-ap",
		Password: "",
		Channel:  6,
		AuthOpen: true,
	})
	if err != nil {
		println("ap: start err:", err)
		return
	}

	println("ap: starting L2 netdev (AP)...")
	nd, err := espradio.StartNetDevAP()
	if err != nil {
		println("ap: netdev err:", err)
		return
	}

	const apIP = "192.168.4.1"
	addr := netip.MustParseAddr(apIP)
	subnet := netip.MustParsePrefix("192.168.4.0/24")

	println("ap: creating lneto stack...")
	stack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:      "espradio-ap",
		StaticAddress: addr,
		MaxUDPPorts:   2,
	})
	if err != nil {
		println("ap: stack err:", err)
		return
	}

	println("ap: configuring DHCP server...")
	err = dhcpServer.Configure(dhcpv4.ServerConfig{
		ServerAddr: addr.As4(),
		Gateway:    addr.As4(),
		Subnet:     subnet,
	})
	if err != nil {
		println("ap: dhcp server configure err:", err)
		return
	}

	err = stack.LnetoStack().RegisterUDP(&dhcpServer, nil, dhcpv4.DefaultClientPort)
	if err != nil {
		println("ap: dhcp server register err:", err)
		return
	}

	println("ap: AP is running on", apIP, "— connect to espradio-ap")
	go stackLoop(stack)
	for {
		time.Sleep(3 * time.Second)
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
