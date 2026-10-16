package main

import (
	"time"

	"github.com/soypat/lneto/http/httphi"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
	link "tinygo.org/x/espradio/netlink"
)

var (
	// Wi-Fi Credentials
	ssid     string // = "yourssid"
	password string // = "yourpassword"

	// Max TX Power
	// For ESP32-C3 (XIAO/SuperMini/etc.), values between 45-55 are recommended for stability.
	// For ESP32-WROOM, default (0) is usually fine.
	maxTxPower int8 = 0

	port uint16 = 80
)

func main() {
	link := &link.Esplink{}
	netdev.UseNetdev(link)

	// Enable radio with specific configuration
	failIfErr("enable radio", espradio.Enable(espradio.Config{
		MaxTxPower: maxTxPower,
	}))

	println("Connecting to WiFi", ssid+"...")
	failIfErr("connect", link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	}))
	println("Connected to WiFi")

	var http httphi.MuxSlice

	// Routes are described here
	http.Handle("/", func(exch *httphi.Exchange) {
		exch.RespondString(httphi.StatusOK, "application/json", `{"message":"hello"}`)
	})

	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(4, 2048, http.MaxPathValues())
	failIfErr("configuring Router", router.Configure(&http, cfg))
	defer router.Shutdown() // Despawns goroutines.

	// Get IP address
	addr, err := link.Addr()
	failIfErr("Esplink.Addr()", err)
	println("Hosting webserver on http://", addr.String(), ":", port)

	// Start web server (blocks indefinitely)
	err = link.ListenAndServe(&router, port)
	failIfErr("Esplink.ListenAndServe", err)

	// `select {}` prevents the program from terminating
	select {}
}

func failIfErr(action string, err error) {
	for err != nil {
		println("Stopped after failure:", action+":", err.Error())
		time.Sleep(1 * time.Second)
	}
}
