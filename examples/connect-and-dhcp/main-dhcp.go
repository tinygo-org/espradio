// This example shows how to connect to a Wi-Fi network and get an IP address with DHCP.
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" ./examples/connect-and-dhcp
package main

import (
	"context"
	"net/netip"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/xnet"
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
		failure("could not enable radio: " + err.Error())
	}

	println("starting radio...")
	err = espradio.Start()
	if err != nil {
		failure("could not start radio: " + err.Error())
	}

	println("connecting to", ssid, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     ssid,
		Password: password,
	})
	if err != nil {
		failure("connect failed: " + err.Error())
	}
	println("connected to", ssid, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		failure("netdev failed: " + err.Error())
	}

	hw, err := nd.HardwareAddr6()
	if err != nil {
		failure("mac failed: " + err.Error())
	}

	println("creating lneto stack...")
	var stack xnet.Netstack
	err = stack.Reset(xnet.StackConfig{
		Hostname:          ssid,
		RandSeed:          time.Now().UnixNano() | 1,
		HardwareAddress:   hw,
		MTU:               1500,
		MaxActiveUDPPorts: 2,
		MaxActiveTCPPorts: 1,
	}, backoff, xnet.TCPPoolConfig{
		NewBackoff: func() lneto.BackoffStrategy { return backoff },
	})
	if err != nil {
		failure("stack failed: " + err.Error())
	}

	// The stack must be running before it is used.
	go func() {
		failure("stack stopped: " + espradio.RunStack(context.Background(), nd, &stack).Error())
	}()

	println("starting DHCP...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	addr, gateway, _, err := stack.EnableDHCP(ctx, true, netip.Addr{})
	cancel()
	if err != nil {
		failure("DHCP failed: " + err.Error())
	}

	println("got IP:", addr.String())
	println("gateway:", gateway.String())
	println("done!")
	for {
		time.Sleep(1 * time.Second)
		println("alive")
	}
}

func backoff(uint) time.Duration { return 5 * time.Millisecond }

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
