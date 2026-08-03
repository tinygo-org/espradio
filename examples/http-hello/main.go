package main

import (
	"time"

	"github.com/soypat/lneto/http/httphi"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	port     uint16 = 80
)

func main() {
	link := &link.Esplink{}
	netdev.UseNetdev(link)

	println("Connecting to WiFi...")
	failIfErr("connect", link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	}))

	println("Connected to WiFi.")
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
	print("Hosting webserver on http://", addr.String(), ":", port, "\n")
	err = link.ListenAndServe(&router, port) // Blocks as long as Router can serve connections.
	failIfErr("Esplink.ListenAndServe", err)
}

func failIfErr(action string, err error) {
	for err != nil {
		println("fail " + action + ": " + err.Error())
		time.Sleep(1 * time.Second)
	}
}
