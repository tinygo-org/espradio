// This example listens on port :80 serving a web page.  Multiple clients
// may connect and be serviced at the same time.  IPv4 only.  HTTP only.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/webserver
package main

import (
	_ "embed"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

//go:embed index.html
var indexHTML string

//go:embed sixlines.html
var sixlinesHTML string

// indexBefore and indexAfter are the two halves of index.html split at the
// access counter insertion point.  Populated by init().
var indexBefore, indexAfter string

const accessMarker = "{{ACCESS}}"

func init() {
	for i := 0; i <= len(indexHTML)-len(accessMarker); i++ {
		if indexHTML[i:i+len(accessMarker)] == accessMarker {
			indexBefore = indexHTML[:i]
			indexAfter = indexHTML[i+len(accessMarker):]
			return
		}
	}
	indexBefore = indexHTML
}

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
		log.Fatal(err)
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
	err = http.ListenAndServe(host+port, nil)
	for err != nil {
		println("error:", err.Error())
		time.Sleep(5 * time.Second)
	}
}

func logRequest(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		println(r.Method, r.URL.Path)
		h(w, r)
	})
}

func root(w http.ResponseWriter, r *http.Request) {
	access := 1

	cookie, err := r.Cookie("access")
	if err != nil {
		if err == http.ErrNoCookie {
			cookie = &http.Cookie{
				Name:  "access",
				Value: "1",
			}
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		v, err := strconv.ParseInt(cookie.Value, 10, 0)
		if err != nil {
			http.Error(w, "invalid cookie.Value : "+cookie.Value, http.StatusBadRequest)
			return
		}
		cookie.Value = strconv.Itoa(int(v) + 1)
		access = int(v) + 1
	}
	http.SetCookie(w, cookie)
	w.WriteHeader(http.StatusOK)

	io.WriteString(w, indexBefore)
	io.WriteString(w, strconv.Itoa(access))
	io.WriteString(w, indexAfter)
}

func sixlines(w http.ResponseWriter, r *http.Request) {
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
