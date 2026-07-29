// This example starts the device as a WiFi access point and serves a web page
// on port :80. Connect to the AP with the SSID and password defined below.
// Clients receive IP addresses automatically via the built-in DHCP server.
// Browse to http://192.168.4.1 once connected.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/apwebserver
package main

import (
	_ "embed"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"tinygo.org/x/drivers/netdev"
	"tinygo.org/x/espradio"
	link "tinygo.org/x/espradio/netlink"
)

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

	lnk := link.Esplink{}
	netdev.UseNetdev(&lnk)

	println("Starting AP...")
	err := lnk.NetConnectAP(link.APConnectParams{
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
	})
	if err != nil {
		failure("could not start AP: " + err.Error())
	}

	http.Handle("/", logRequest(root))
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))

	// Driver counters, printed while traffic is flowing.  Most of these count
	// something being dropped, so a non-zero value is the only evidence it
	// happened.
	go func() {
		for {
			time.Sleep(10 * time.Second)
			var stats espradio.Stats
			espradio.ReadStats(&stats)
			stats.Print()
		}
	}()

	println("HTTP server listening on http://" + apIP + port)
	err = http.ListenAndServe(apIP+port, nil)
	if err != nil {
		failure("http.ListenAndServe: " + err.Error())
	}
}

func logRequest(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		println(r.Method, r.URL.Path)
		h(w, r)
	})
}

func root(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, indexHTML)
}

func sixlines(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, sixlinesHTML)
}

func LED_ON(w http.ResponseWriter, r *http.Request) {
	setLED(true)
	w.Header().Set(`Content-Type`, `text/plain; charset=UTF-8`)
	io.WriteString(w, "led.High()")
}

func LED_OFF(w http.ResponseWriter, r *http.Request) {
	setLED(false)
	w.Header().Set(`Content-Type`, `text/plain; charset=UTF-8`)
	io.WriteString(w, "led.Low()")
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(`Content-Type`, `text/plain; charset=UTF-8`)
	io.WriteString(w, "hello")
}

var counter int

func cnt(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Method == "POST" {
		c := r.Form.Get("cnt")
		if c != "" {
			i64, _ := strconv.ParseInt(c, 0, 0)
			counter = int(i64)
		}
	}

	w.Header().Set(`Content-Type`, `application/json`)
	io.WriteString(w, `{"cnt": `)
	io.WriteString(w, strconv.Itoa(counter))
	io.WriteString(w, `}`)
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
