// This example shows how to connect to a Wi-Fi network and get an IP address with DHCP.
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" ./examples/connect-and-dhcp
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
	})
	if err != nil {
		println("could not enable radio:", err)
		return
	}

	println("starting radio...")
	err = espradio.Start()
	if err != nil {
		println("could not start radio:", err)
		return
	}

	println("connecting to", ssid, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     ssid,
		Password: password,
	})
	if err != nil {
		println("connect failed:", err)
		return
	}
	println("connected to", ssid, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		println("netdev failed:", err)
		return
	}

	println("creating lneto stack...")
	stack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    ssid,
		MaxUDPPorts: 2,
		MaxTCPPorts: 1,
	})
	if err != nil {
		println("stack failed:", err)
		return
	}

	// Start the poll loop in the background.
	// VERY IMPORTANT TO START BEFORE USING STACK!
	go stackLoop(stack)

	println("starting DHCP...")
	dhcp, err := stack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err)
		return
	}
	println("got IP:", dhcp.AssignedAddr.String())
	println("gateway:", dhcp.Router.String())
	if len(dhcp.DNSServers) > 0 {
		println("DNS:", dhcp.DNSServers[0].String())
	}
	println("done!")
	for {
		time.Sleep(1 * time.Second)
		println("alive")
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
