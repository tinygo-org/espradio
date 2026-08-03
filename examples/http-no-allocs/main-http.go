// This example shows how to create a simple HTTP server that serves a webpage with
// a button to toggle an LED and a list of recent actions. The server keeps track of
// the last 16 actions performed, including the callsign of the user who performed
// the action and whether they turned the LED on or off. The webpage displays this
// information in a list, along with how long ago each action was performed.
// You can test it by connecting to the same Wi-Fi network as the ESP32 and
// navigating to the IP address assigned to the ESP32 in your browser.
// Click the "Toggle LED" button to see the LED state change and the action recorded on the webpage.
//
// Requests are served by a httphi.Router: it allocates its exchanges (request
// header buffer, response header buffer) and its worker goroutines once at
// Configure time, so serving requests costs no allocation and memory does not
// grow with load. Accepting connections is still this program's job.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/http-no-allocs
package main

import (
	"bytes"
	_ "embed"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

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
	ntpHost        = "pool.ntp.org"
	pollTime       = 5 * time.Millisecond
	maxConns       = 4
	kB             = 1 << 10 // kilobyte
	httpConnMemory = 4 * kB
	listenPort     = 80
	connDeadline   = 8 * time.Second

	templateActionMarker = "<!--A-->"
)

var (
	//go:embed template.html
	htmlTemplate  []byte
	htmlActionIdx = bytes.Index(htmlTemplate, []byte(templateActionMarker)) + len(templateActionMarker)
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
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

	println("creating lneto stack...")
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:     ssid,
		MaxUDPPorts:  2,
		MaxTCPPorts:  2,
		PassivePeers: 64,
	})
	if err != nil {
		failure("stack failed: " + err.Error())
	}

	// Start the poll loop in the background.
	go loopForeverStack(espstack)
	println("starting DHCP...")
	dhcp, err := espstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		failure("DHCP failed: " + err.Error())
	}

	addr, ok := netip.AddrFromSlice(dhcp.AssignedAddr4[:])
	if !ok {
		failure("invalid IP address")
	}

	println("got IP:", addr.String())

	lstack := espstack.LnetoStack()
	rstack := lstack.StackRetrying(pollBackoff)
	gatewayHW, err := rstack.DoResolveHardwareAddress6(dhcp.Router, 500*time.Millisecond, 4)
	if err != nil {
		failure("ARP resolve failed: " + err.Error())
	}
	lstack.SetGatewayHardwareAddr(gatewayHW)

	// DNS lookup for NTP server and calculate time. If this fails just ignore.
	println("resolving ntp host:", ntpHost)
	addrs, err := rstack.DoLookupIP(ntpHost, 5*time.Second, 3)
	if err == nil {
		offset, err := rstack.DoNTP(addrs[0], 5*time.Second, 3)
		if err == nil {
			runtime.AdjustTimeOffset(int64(offset))
			println("NTP success:", time.Now().String())
		}
	}

	tcpPool, err := xnet.NewTCPPool(xnet.TCPPoolConfig{
		PoolSize:           maxConns,
		QueueSize:          3,
		TxBufSize:          len(htmlTemplate) + 1024,
		RxBufSize:          1024,
		EstablishedTimeout: 5 * time.Second,
		ClosingTimeout:     5 * time.Second,
		NewBackoff:         func() lneto.BackoffStrategy { return pollBackoff },
	})
	if err != nil {
		failure("tcppool create: " + err.Error())
	}

	listenAddr := netip.AddrPortFrom(addr, listenPort)
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
	lstack.Debug("init-complete")

	for {
		if listener.NumberOfReadyToAccept() == 0 {
			time.Sleep(pollTime)
			tcpPool.CheckTimeouts()
			continue
		}

		conn, _, err := listener.TryAccept()
		if err != nil {
			println("err listener accept:", err.Error())
			time.Sleep(time.Second)
			continue
		}
		printIncoming(conn)
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

type Server struct {
	state       ServerState
	scratchPool sync.Pool
}

// scratch holds the work buffers a handler needs for form, cookie, query parsing.
type scratch struct {
	dyn [1024]byte // Rendered action list, sized so append never grows it.
}

func (sv *Server) InitAndRegister(mux *httphi.MuxSlice) {
	sv.scratchPool.New = func() interface{} { return &scratch{} }
	mux.Handle("GET /", sv.handleLanding)
	mux.Handle("GET /toggle-led", sv.handleToggleLED)
}

func (sv *Server) handleLanding(exch *httphi.Exchange) {
	println("Got webpage request!")
	scratch := sv.scratchPool.Get().(*scratch)
	defer sv.scratchPool.Put(scratch)

	dynContent := sv.state.AppendActionsHTML(scratch.dyn[:0])
	if len(dynContent) > len(scratch.dyn) {
		println("[WARN] dynamic content outgrew scratch, heap allocated:", cap(dynContent))
	}
	exch.StageHeader("Content-Type", "text/html")
	exch.StageHeaderInt("Content-Length", int64(len(htmlTemplate)+len(dynContent)))
	// The router serves one exchange per connection and then closes it. Saying so
	// avoids notably slower paint times in the browser. Content-Length above is
	// what keeps the browser from treating the close as a truncated page.
	exch.StageHeader("Connection", "close")
	exch.WriteHeader(httphi.StatusOK)
	exch.WriteBody(htmlTemplate[:htmlActionIdx])
	exch.WriteBody(dynContent)
	exch.WriteBody(htmlTemplate[htmlActionIdx:])
	time.Sleep(pollTime)
}

func (sv *Server) handleToggleLED(exch *httphi.Exchange) {
	println("got toggle led request")
	rawCallsign, _ := exch.RequestQueryValue("callsign")
	sv.state.RecordToggle(sanitizeCallsign(rawCallsign))
	exch.Respond(httphi.StatusOK, "", nil)
}

// ServerState stores the state of the HTTP server. It has a ring buffer with last 8 actions
// performed. Every time a new action is performed it replaces the oldest action by advancing the ring buffer.
type ServerState struct {
	mu            sync.Mutex
	ActionRingBuf [16]Action
	LastAction    int
	LEDState      bool
}

type Action struct {
	Time        time.Time
	Callsign    [9]byte // fits max "(unknown)".
	CallsignLen uint8
	TurnedLEDOn bool
}

func (s *ServerState) RecordToggle(callsign []byte) {
	if len(callsign) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LEDState = !s.LEDState
	setLED(s.LEDState)
	idx := s.LastAction % len(s.ActionRingBuf)
	a := &s.ActionRingBuf[idx]
	a.Time = time.Now()
	a.TurnedLEDOn = s.LEDState
	n := copy(a.Callsign[:], callsign)
	a.CallsignLen = uint8(n)
	s.LastAction++
}

func (s *ServerState) AppendActionsHTML(buf []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.LastAction
	if count > len(s.ActionRingBuf) {
		count = len(s.ActionRingBuf)
	}
	if count == 0 {
		return buf
	}
	now := time.Now()
	buf = append(buf, "<ul>"...)
	for i := 0; i < count; i++ {
		idx := (s.LastAction - 1 - i) % len(s.ActionRingBuf)
		a := &s.ActionRingBuf[idx]
		buf = append(buf, "<li>"...)
		buf = append(buf, a.Callsign[:a.CallsignLen]...)
		if a.TurnedLEDOn {
			buf = append(buf, " turned led on "...)
		} else {
			buf = append(buf, " turned led off "...)
		}
		buf = appendDurationAgo(buf, now.Sub(a.Time))
		buf = append(buf, "</li>"...)
	}
	buf = append(buf, "</ul>"...)
	return buf
}

func appendDurationAgo(dst []byte, d time.Duration) []byte {
	var val int64
	var unit byte
	sec := int64(d / time.Second)
	switch {
	case sec < 60:
		val, unit = sec, 's'
	case sec < 3600:
		val, unit = sec/60, 'm'
	case sec < 86400:
		val, unit = sec/3600, 'h'
	default:
		val, unit = sec/86400, 'd'
	}
	dst = strconv.AppendInt(dst, val, 10)
	dst = append(dst, unit)
	dst = append(dst, " ago "...)
	return dst
}

func sanitizeCallsign(raw []byte) []byte {
	const maxCallsignLength = 4
	for i, b := range raw {
		isAlpha := b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
		validChar := isAlpha || b == '.'
		if i >= maxCallsignLength || !validChar {
			return raw[:i]
		}
	}
	return raw
}

// addrBuf formats remote addresses in printIncoming. Only the accept loop uses it.
var addrBuf [64]byte

func printIncoming(conn *tcp.Conn) {
	remoteAddr, _ := netip.AddrFromSlice(conn.RemoteAddr())
	print("incoming connection: ")
	printAddr(remoteAddr, addrBuf[:0])
	println(" from port", conn.RemotePort())
}

// printAddr prints a netip.Addr without heap allocation by formatting into buf.
func printAddr(addr netip.Addr, buf []byte) {
	buf = addr.AppendTo(buf[:0])
	print(unsafe.String(&buf[0], len(buf)))
}

func loopForeverStack(stack *espradio.Stack) {
	for {
		send, recv, _ := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
		}
	}
}

func failure(msg string) {
	for {
		println("failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
