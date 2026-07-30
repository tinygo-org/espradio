// This example listens on port :80 serving a web page.  Multiple clients
// may connect and be serviced at the same time.  IPv4 only.  HTTP only.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/webserver
package main

import (
	"context"
	_ "embed"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/http/httpraw"
	"github.com/soypat/lneto/x/xnet"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

// Pages are embedded as bytes so serving them needs no string conversion,
// which would copy the page to the heap on every request.

//go:embed index.html
var indexHTML string

//go:embed sixlines.html
var sixlinesHTML string

// Stores []byte buffers to avoid allocations in HTTP handlers.
var scratchPool sync.Pool

const scratchSize = 128

const (
	listenPort = 80
	port       = ":80"
)

var (
	ssid     string
	password string
)

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	link := link.Esplink{}

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("could not connect to WiFi: " + err.Error())
	}
	scratchPool.New = func() interface{} { return make([]byte, scratchSize) }
	var http httphi.MuxSlice
	http.Handle("/", logRequest(root))
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))

	var router httphi.Router
	err = router.Configure(httphi.RouterConfig{
		FixedNumGoroutines:          4,
		RequestHeaderBufferSize:     1024, // Google chrome requests are around 700 bytes in header size.
		ResponseHeaderMinBufferSize: 128,  // We won't be writing too much to headers. Unused request memory is reused on top of this.
		RequestNumHeaderKVCap:       16,   //
		MaxAwaitingConns:            4,
		Mux:                         &http,
	})
	if err != nil {
		failure("configure Router: " + err.Error())
	}
	defer router.Shutdown() // Despawns goroutines.
	// Listen by asking the lneto stack for a socket directly instead of going
	// through stdlib net.Listen and the netdev file descriptor layer. An
	// unspecified local address with no remote is a passive socket, so the
	// stack hands back a net.Listener over its own tcp.Listener and conn pool.
	stack := link.StackGo()
	laddr := netip.AddrPortFrom(netip.IPv4Unspecified(), listenPort)
	sock, err := stack.SocketNetip(context.Background(), "tcp4", xnet.AF_INET, xnet.SOCK_STREAM, laddr, netip.AddrPort{})
	if err != nil {
		failure("opening port: " + err.Error())
	}
	listener, ok := sock.(net.Listener)
	if !ok {
		failure("stack returned non-listener socket")
	}
	defer listener.Close()
	h, _ := link.Addr()
	host := h.String()
	println("HTTP server listening on http://" + host + port)
	for {
		conn, err := listener.Accept()
		if err != nil {
			failure("failed to accept conn: " + err.Error())
		}
		// conn.SetDeadline(time.Now().Add(time.Second))
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
	exch.RespondString(200, "text/html", indexHTML)
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

var counter int

func cnt(exch *httphi.Exchange) {
	scratch := scratchPool.Get().([]byte)
	defer scratchPool.Put(scratch)
	switch exch.RequestMethod() {
	case httphi.MethPost: // POST
		var form httpraw.Form
		form.Reset(scratch, 2) // 2 query values max.
		const parseURL, prioritizeURL = true, false
		err := exch.RequestParseForm(&form, parseURL, prioritizeURL)
		if err != nil {
			exch.Respond(500, "", nil)
			return
		}
		c := form.Get("cnt")
		if len(c) > 0 {
			i64, _ := strconv.ParseInt(string(c), 0, 0)
			counter = int(i64)
		}
	}
	json := append(scratch[:0], `{"cnt": `...)
	json = strconv.AppendInt(json, int64(counter), 10)
	json = append(json, '}')
	exch.Respond(200, "application/json", json)
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
