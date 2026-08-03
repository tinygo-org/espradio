// This example starts the device as a WiFi access point and serves a web page
// on port :80. Connect to the AP with the SSID and password defined below.
// Clients receive IP addresses automatically via the built-in DHCP server.
// Browse to http://192.168.4.1 once connected.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/apwebserver
package main

import (
	"context"
	_ "embed"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/http/httpraw"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
	link "tinygo.org/x/espradio/netlink"
)

// Pages are embedded as bytes so serving them needs no string conversion,
// which would copy the page to the heap on every request.

//go:embed index.html
var indexHTML string

//go:embed sixlines.html
var sixlinesHTML string

var (
	ssid     string
	password string
	port     string = ":80"
)

const apIP = "192.168.4.1"

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	lnk := &link.Esplink{}

	println("Starting AP...")
	failIfErr("starting AP", lnk.NetConnectAP(link.APConnectParams{
		APConfig: espradio.APConfig{
			SSID:     ssid,
			Password: password,
			Channel:  6,
		},
		StaticAddr:       netip.MustParseAddr(apIP),
		EnableDHCPServer: true,
		MaxUDPPorts:      2,
		MaxTCPPorts:      4,
		PassivePeers:     64,
	}))

	var http httphi.MuxSlice
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))
	// A trailing slash is an anonymous wildcard, so "/" matches every path.
	// Registered last it serves what the paths above did not claim.
	http.Handle("/", logRequest(root))

	const maxConns, httpMemoryPerConn = 4, 2048
	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(maxConns, httpMemoryPerConn, http.MaxPathValues())
	failIfErr("configuring Router", router.Configure(&http, cfg))
	defer router.Shutdown() // Despawns goroutines.

	listener, err := Listen(lnk, port)
	failIfErr("listening to port", err)
	defer listener.Close()
	println("HTTP server listening on http://" + apIP + port)
	for {
		conn, err := listener.Accept()
		failIfErr("accepting conn", err)
		err = router.Handle(conn)
		if err != nil {
			conn.Close()
			println("failed to handle connection: ", err.Error())
		}
	}
}

func logRequest(h httphi.HandlerFunc) httphi.HandlerFunc {
	return func(exch *httphi.Exchange) {
		println(exch.RequestMethod().String(), " ", exch.MuxPattern())
		h(exch)
	}
}

func root(exch *httphi.Exchange) {
	exch.RespondString(200, "text/html", indexHTML)
}

func sixlines(exch *httphi.Exchange) {
	exch.RespondString(200, "text/html", sixlinesHTML)
}

func LED_ON(exch *httphi.Exchange) {
	setLED(true)
	exch.RespondString(200, "text/plain; charset=UTF-8", "led.High()")
}

func LED_OFF(exch *httphi.Exchange) {
	setLED(false)
	exch.RespondString(200, "text/plain; charset=UTF-8", "led.Low()")
}

func hello(exch *httphi.Exchange) {
	exch.RespondString(200, "text/plain; charset=UTF-8", "hello")
}

// counter is read and written from the router's goroutines, so several clients
// may be inside cnt at once.
var counter atomic.Int64

func cnt(exch *httphi.Exchange) {
	var scratch [64]byte
	switch exch.RequestMethod() {
	case httphi.MethPost: // POST
		var form httpraw.Form
		form.Reset(scratch[:], 2) // 2 query values max.
		const parseURL, prioritizeURL = true, false
		err := exch.RequestParseForm(&form, parseURL, prioritizeURL)
		if err != nil {
			exch.Respond(500, "", nil)
			return
		}
		c := form.Get("cnt")
		if len(c) > 0 {
			// Parsed before the form's memory is reused for the response below.
			i64, _ := strconv.ParseInt(string(c), 0, 0)
			counter.Store(i64)
			println("set counter", i64)
		}
	}
	json := append(scratch[:0], `{"cnt": `...)
	json = strconv.AppendInt(json, counter.Load(), 10)
	json = append(json, '}')
	exch.Respond(200, "application/json", json)
}

func failIfErr(action string, err error) {
	for err != nil {
		println("fail " + action + ": " + err.Error())
		time.Sleep(1 * time.Second)
	}
}

func Listen(lnk *link.Esplink, port string) (net.Listener, error) {
	// Listen by asking the lneto stack for a socket directly instead of going
	// through stdlib net.Listen and the netdev file descriptor layer due to a bug.
	stack := lnk.StackGo()
	laddr, err := netip.ParseAddrPort("0.0.0.0" + port)
	if err != nil {
		return nil, err
	}
	sock, err := stack.SocketNetip(context.Background(), "tcp4", xnet.AF_INET, xnet.SOCK_STREAM, laddr, netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	listener, ok := sock.(net.Listener)
	if !ok {
		return nil, errors.New("stack returned non-listener socket")
	}
	return listener, nil
}
