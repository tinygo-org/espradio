// This example shows how to create a simple HTTP server that serves a static webpage.
// The server listens for incoming HTTP requests and responds with the contents of the
// embedded `index.html` file. You can test it by connecting to the same Wi-Fi network
// as the ESP32 and navigating to the IP address assigned to the ESP32 in your browser.
//
// Requests are served by a httphi.Router: it allocates its exchanges (request
// header buffer, response header buffer) and its worker goroutines once at
// Configure time, so serving requests costs no allocation and memory does not
// grow with load. Accepting connections is still this program's job.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/http-no-allocs-static
package main

import (
	"context"
	_ "embed"
	"net/netip"
	"sync"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

const (
	pollTime       = 5 * time.Millisecond
	maxConns       = 4
	kB             = 1 << 10 // kilobyte
	httpConnMemory = 2 * kB
	listenPort     = 80

	// connDeadline fails the reads/writes of a peer that opens a connection and
	// then stalls, instead of it holding a router goroutine forever.
	connDeadline = 8 * time.Second
)

var (
	//go:embed index.html
	webPage []byte
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelNone,
	})
	if err != nil {
		failure("could not enable radio: " + err.Error())
	}

	println("starting radio...")
	err = espradio.Start()
	if err != nil {
		failure("could not start radio: " + err.Error())
	}

	println("connecting to", ssid, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     ssid,
		Password: password,
	})
	if err != nil {
		failure("connect failed: " + err.Error())
	}
	println("connected to", ssid, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		failure("netdev failed: " + err.Error())
	}

	hw, err := nd.HardwareAddr6()
	if err != nil {
		failure("mac failed: " + err.Error())
	}

	println("creating lneto stack...")
	var stack xnet.Netstack
	err = stack.Reset(xnet.NetstackConfig{
		StackConfig: xnet.StackConfig{
			Hostname:          ssid,
			RandSeed:          time.Now().UnixNano() | 1,
			HardwareAddress:   hw,
			MTU:               1500,
			MaxActiveUDPPorts: 2,
			MaxActiveTCPPorts: 1,
			PassivePeers:      64,
		},
		BackoffStack: pollBackoff,
		TCPPoolConfig: xnet.TCPPoolConfig{
			NewBackoff: func() lneto.BackoffStrategy { return pollBackoff },
		},
	})
	if err != nil {
		failure("stack failed: " + err.Error())
	}

	// Start the stack in the background before using it.
	go func() {
		failure("stack stopped: " + espradio.RunStack(context.Background(), nd, &stack).Error())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	addr, _, _, err := stack.EnableDHCP(ctx, true, netip.Addr{})
	cancel()
	if err != nil {
		failure("DHCP failed: " + err.Error())
	}
	lstack := stack.StackAsync()
	tcpPool, err := lstack.NewTCPPool(xnet.TCPPoolConfig{
		PoolSize:           maxConns,
		QueueSize:          3,
		TxBufSize:          len(webPage) + 128,
		RxBufSize:          256,
		EstablishedTimeout: 5 * time.Second,
		ClosingTimeout:     5 * time.Second,
		NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
	})
	if err != nil {
		failure("tcppool create: " + err.Error())
	}

	println("got IP:", addr.String())
	listenAddr := netip.AddrPortFrom(addr, listenPort)

	// Create and register TCP listener.
	var listener tcp.Listener
	err = listener.Reset(listenPort, tcpPool)
	if err != nil {
		failure("listener reset: " + err.Error())
	}
	err = lstack.RegisterListenerTCP(&listener)
	if err != nil {
		failure("listener register: " + err.Error())
	}

	var mux httphi.MuxSlice
	var server Server
	server.InitAndRegister(&mux)
	// Router allocates its exchanges and goroutines here and never again.
	var router httphi.Router
	cfg := httphi.DefaultRouterConfig(maxConns, httpConnMemory, mux.MaxPathValues())
	err = router.Configure(&mux, cfg)
	if err != nil {
		failure("router configure: " + err.Error())
	}
	defer router.Shutdown() // Despawns goroutines.

	println("listening on", "http://"+listenAddr.String())

	for {
		if listener.NumberOfReadyToAccept() == 0 {
			time.Sleep(pollTime)
			tcpPool.CheckTimeouts()
			continue
		}

		conn, err := listener.TryAccept()
		if err != nil {
			println("listener accept err", err.Error())
			time.Sleep(time.Second)
			continue
		}
		remote := conn.RemoteAddrPort()
		println("incoming connection:", remote.Addr().String(), "from port", remote.Port())
		conn.SetDeadline(time.Now().Add(connDeadline))
		err = router.Handle(conn)
		if err != nil {
			// Every router goroutine is busy and the queue is full. Dropping the
			// connection is the backpressure that keeps memory bounded.
			println("dropped connection:", err.Error())
			conn.Close()
		}
	}
}

var pollBackoff = lneto.BackoffStrategy(func(_ uint) time.Duration {
	return pollTime
})

// Server holds the state the handlers share. Handlers run on the router's
// goroutines, up to maxConns of them at a time, so state they touch is guarded.
// Both responses here are static, so unlike the http-no-allocs example this server
// needs no scratch buffer pool: nothing is rendered per request.
type Server struct {
	mu       sync.Mutex
	ledState bool
}

func (sv *Server) InitAndRegister(mux *httphi.MuxSlice) {
	mux.Handle("GET /", sv.handleLanding)
	mux.Handle("GET /toggle-led", sv.handleToggleLED)
}

func (sv *Server) handleLanding(exch *httphi.Exchange) {
	println("Got webpage request!")
	exch.Respond(httphi.StatusOK, "text/html", webPage)
}

func (sv *Server) handleToggleLED(exch *httphi.Exchange) {
	println("got toggle led request")
	sv.mu.Lock()
	sv.ledState = !sv.ledState
	setLED(sv.ledState)
	sv.mu.Unlock()
	exch.Respond(httphi.StatusOK, "", nil)
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
