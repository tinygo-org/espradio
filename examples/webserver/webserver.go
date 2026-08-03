// This example listens on port :80 serving a web page.  Multiple clients
// may connect and be serviced at the same time.  IPv4 only.  HTTP only.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/webserver
package main

import (
	_ "embed"
	"strconv"
	"sync"
	"time"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/http/httpraw"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

//go:embed index.html
var indexHTML string

//go:embed sixlines.html
var sixlinesHTML string

var scratchPool sync.Pool // Stores []byte buffers to avoid allocations in HTTP handlers.

const scratchSize = 128
const port uint16 = 80 // HTTP listening port.

var (
	ssid     string
	password string
)

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	link := &link.Esplink{}

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	failIfErr("Connecting to WiFi", err)
	scratchPool.New = func() interface{} { return make([]byte, scratchSize) }
	var http httphi.MuxSlice
	http.Handle("/", logRequest(root))
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))

	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(4, 2048, http.MaxPathValues())
	err = router.Configure(&http, cfg)
	failIfErr("Configuring httphi.Router", err)
	defer router.Shutdown() // Despawns goroutines.
	addr, err := link.Addr()
	failIfErr("Esplink.Addr()", err)
	print("Hosting webserver on http://", addr.String(), ":", port, "\n")
	err = link.ListenAndServe(&router, port)
	failIfErr("Esplink.ListenAndServe", err)
	panic("unreachable")
}

func logRequest(h httphi.HandlerFunc) httphi.HandlerFunc {
	return func(exch *httphi.Exchange) {
		println(exch.RequestMethod().String(), " ", exch.MuxPattern())
		h(exch)
	}
}

func root(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, "text/html", indexHTML)
}

func sixlines(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, "text/html", indexHTML)
}

const textplain = "text/plain; charset=UTF-8"

func LED_ON(exch *httphi.Exchange) {
	setLED(true)
	exch.RespondString(httphi.StatusOK, textplain, "led.High()")
}

func LED_OFF(exch *httphi.Exchange) {
	setLED(false)
	exch.RespondString(httphi.StatusOK, textplain, "led.Low()")
}

func hello(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, textplain, "hello")
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
			exch.Respond(httphi.StatusInternalServerError, "", nil)
			return
		}
		c := form.Get("cnt")
		if len(c) > 0 {
			i64, _ := strconv.ParseInt(string(c), 0, 0)
			counter = int(i64)
			println("set counter", counter)
		}
	}
	json := append(scratch[:0], `{"cnt": `...)
	json = strconv.AppendInt(json, int64(counter), 10)
	json = append(json, '}')
	exch.Respond(httphi.StatusOK, "application/json", json)
}

func failIfErr(action string, err error) {
	for err != nil {
		println("fail " + action + ": " + err.Error())
		time.Sleep(1 * time.Second)
	}
}
