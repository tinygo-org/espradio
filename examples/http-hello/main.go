package main

import (
	"time"

	"github.com/soypat/lneto/http/httphi"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string // = "yourssid"
	password string // = "yourpassword"
)

// Limit TX power for stability. Valid range is 8-84; zero keeps the blob default.
var maxTxPower int8 = 0

const port uint16 = 80

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	link := &link.Esplink{
		MaxTxPower: maxTxPower,
	}
	netdev.UseNetdev(link)

	println("Connecting to WiFi", ssid+"...")
	failIfErr("connect (ensure antenna connected)", link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	}))
	println("Connected to WiFi")

	var http httphi.MuxSlice

	http.Handle("/", func(exch *httphi.Exchange) {
		exch.RespondString(httphi.StatusOK, "application/json", `{"message":"hello"}`)
	})

	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(4, 2048, http.MaxPathValues())
	failIfErr("configuring Router", router.Configure(&http, cfg))
	defer router.Shutdown() // Despawns goroutines.

	addr, err := link.Addr()
	failIfErr("Esplink.Addr()", err)
	println("Hosting webserver on http://", addr.String(), ":", port)

	err = link.ListenAndServe(&router, port) // Blocks as long as Router can serve connections.
	failIfErr("Esplink.ListenAndServe", err)
}

func failIfErr(action string, err error) {
	for err != nil {
		println("Stopped after failure:", action+":", err.Error())
		time.Sleep(1 * time.Second)
	}
}
