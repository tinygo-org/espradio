package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	port     string = ":80"
)

func main() {
	// use ESP32 radio
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
		exch.RespondString(200, "application/json", `{"message":"hello"}`)
	})
	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(4, 2048, http.MaxPathValues())
	failIfErr("configuring Router", router.Configure(&http, cfg))
	defer router.Shutdown() // Despawns goroutines.
	listener, err := Listen(link, port)
	failIfErr("listening to port", err)
	defer listener.Close()
	host, _ := link.Addr()
	println("HTTP server listening on http://" + host.String() + port)
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

func failIfErr(action string, err error) {
	for err != nil {
		println("fail " + action + ": " + err.Error())
		time.Sleep(1 * time.Second)
	}
}

func Listen(link *netlink.Esplink, port string) (net.Listener, error) {
	// Listen by asking the lneto stack for a socket directly instead of going
	// through stdlib net.Listen and the netdev file descriptor layer due to a bug.
	stack := link.StackGo()
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
