// This example listens on port :80 serving a web page.  Multiple clients
// may connect and be serviced at the same time.  IPv4 only.  HTTP only.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/webserver
package main

import (
	"errors"
	"time"

	"github.com/soypat/lneto/http/httphi"
	link "tinygo.org/x/espradio/netlink"
)

// Wi-Fi operating mode: "sta" for station mode or "ap" for access point mode.
const wifiMode = "sta"

var (
	ssid     string // = "yourssid"
	password string // = "yourpassword"
)

const (
	apSsid    string = "tinygo-webserver"
	apIP      string = "192.168.4.1"
	apChannel uint8  = 6
)

// Limit TX power for stability. Valid range is 8-84; zero keeps the blob default.
var maxTxPower int8 = 0

const port uint16 = 80

func main() {
	link := &link.Esplink{
		MaxTxPower: maxTxPower,
	}

	switch wifiMode {
	case "sta":
		if ssid != "" {
			failIfErr("connect to STA", connectSTA(link))
		} else {
			println("SSID not set; creating an access point", apSsid)
			failIfErr("create AP", createAP(link))
		}
	case "ap":
		if ssid == "" {
			ssid = apSsid
		}
		failIfErr("create AP", createAP(link))
	default:
		failIfErr("setup Wi-Fi", errors.New("unknown wifi mode: "+wifiMode))
	}

	scratchPool.New = func() interface{} { return make([]byte, scratchSize) }

	var http httphi.MuxSlice

	http.Handle("/", logRequest(root))
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))

	const maxConns, httpMemoryPerConn = 4, 2048
	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(maxConns, httpMemoryPerConn, http.MaxPathValues())
	err := router.Configure(&http, cfg)
	failIfErr("Configuring httphi.Router", err)
	defer router.Shutdown() // Despawns goroutines.

	addr, err := link.Addr()
	failIfErr("Esplink.Addr()", err)
	print("Hosting webserver on http://", addr.String(), ":", port, "\n")

	err = link.ListenAndServe(&router, port) // Blocks as long as Router can serve connections.
	failIfErr("Esplink.ListenAndServe", err)
	panic("unreachable")
}

func failIfErr(action string, err error) {
	for err != nil {
		println("stopped after failure:", action+":", err.Error())
		time.Sleep(1 * time.Second)
	}
}
