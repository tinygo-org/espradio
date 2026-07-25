// This example listens on port :80 serving a web page.  Multiple clients
// may connect and be serviced at the same time.  IPv4 only.  HTTP only.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/webserver
package main

import (
	_ "embed"
	"io"
	"net/http"
	"strconv"
	"time"

	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio/httputil"
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

func main() {
	// wait a bit for serial
	time.Sleep(2 * time.Second)

	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("could not connect to WiFi: " + err.Error())
	}

	http.Handle("/", logRequest(root))
	http.Handle("/hello", logRequest(hello))
	http.Handle("/cnt", logRequest(cnt))
	http.Handle("/6", logRequest(sixlines))
	http.Handle("/off", logRequest(LED_OFF))
	http.Handle("/on", logRequest(LED_ON))

	h, _ := link.Addr()
	host := h.String()
	println("HTTP server listening on http://" + host + port)
	err = http.ListenAndServe(host+port, httputil.ThrottleHandler(link.TCPPoolSize(), nil))
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
