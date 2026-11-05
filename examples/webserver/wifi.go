package main

import (
	"net/netip"

	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
	"tinygo.org/x/espradio/netlink"
)

func connectSTA(link *netlink.Esplink) error {
	println("[Wi-Fi] Connecting to", ssid)

	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		return err
	}

	println("[Wi-Fi] Successfully connected")
	return nil
}

func createAP(link *netlink.Esplink) error {
	println("[Wi-Fi] Starting AP", ssid)

	err := link.NetConnectAP(netlink.APConnectParams{
		APConfig: espradio.APConfig{
			SSID:     ssid,
			Password: password,
			Channel:  apChannel,
		},
		StaticAddr:       netip.MustParseAddr(apIP),
		EnableDHCPServer: true,
		MaxUDPPorts:      2,
		MaxTCPPorts:      4,
		PassivePeers:     64,
	})
	if err != nil {
		return err
	}

	println("[Wi-Fi] AP successfully started")
	return nil
}
