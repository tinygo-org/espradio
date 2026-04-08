package main

import (
	"io"
	"log"
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
	// use ESP32 radio
	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("Connecting to WiFi...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		log.Fatal(err)
	}

	println("Connected to WiFi.")

	// now setup the web server using the Go "net/http" package:
	http.HandleFunc("/", hello)

	h, _ := link.Addr()
	host := h.String()
	println("HTTP server listening on http://" + host + port)
	err = http.ListenAndServe(host+port, nil)
	for err != nil {
		println("error:", err.Error())
		time.Sleep(5 * time.Second)
	}
}

func hello(w http.ResponseWriter, r *http.Request) {
	println(r.Method, r.URL.Path)
	w.Header().Set(`Content-Type`, `text/plain; charset=UTF-8`)
	io.WriteString(w, "hello")
}
