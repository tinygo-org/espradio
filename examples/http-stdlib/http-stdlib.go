package main

import (
	"io"
	"net/http"
	"time"

	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	port     string = ":80"
)

func main() {
	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("connect failed: " + err.Error())
	}

	println("Connected to WiFi.")

	// now setup the web server using the Go "net/http" package:
	http.HandleFunc("/", hello)

	h, _ := link.Addr()
	host := h.String()
	println("HTTP server listening on http://" + host + port)
	err = http.ListenAndServe(host+port, nil)
	if err != nil {
		failure("HTTP server error: " + err.Error())
	}
}

func hello(w http.ResponseWriter, r *http.Request) {
	println(r.Method, r.URL.Path)
	w.Header().Set(`Content-Type`, `text/plain; charset=UTF-8`)
	io.WriteString(w, "hello")
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
