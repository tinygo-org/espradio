package main

import (
	"time"

	"tinygo.org/x/espradio"
)

const (
	wifiSSID = "Kracozabra"
	wifiPass = "09655455"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelNone,
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

	println("connecting to", wifiSSID, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     wifiSSID,
		Password: wifiPass,
	})
	if err != nil {
		println("connect failed:", err)
		return
	}
	println("connected to", wifiSSID, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		println("netdev failed:", err)
		return
	}

	println("creating lneto stack...")
	stack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    "espradio",
		MaxUDPPorts: 2,
		MaxTCPPorts: 1,
	})
	if err != nil {
		println("stack failed:", err)
		return
	}

	// Start the poll loop in the background.
	go func() {
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
	}()

	println("starting DHCP...")
	dhcp, err := stack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err)
		return
	}
	println("got IP:", dhcp.AssignedAddr.String())
	println("gateway:", dhcp.Router.String())
	println("DNS:", dhcp.DNSServers[0].String())

	println("done!")
	for {
		time.Sleep(1 * time.Second)
		println("alive")
	}
}
