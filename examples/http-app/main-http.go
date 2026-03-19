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

	"github.com/soypat/lneto/http/httpraw"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
)

const (
	wifiSSID = "Kracozabra"
	wifiPass = "09655455"

	ntpHost    = "pool.ntp.org"
	pollTime   = 5 * time.Millisecond
	maxConns   = 4
	httpBuf    = 1024
	listenPort = 80

	templateActionMarker = "<!--A-->"
)

var (
	//go:embed template.html
	htmlTemplate  []byte
	htmlActionIdx = bytes.Index(htmlTemplate, []byte(templateActionMarker)) + len(templateActionMarker)
)

func setLED(lightOn bool) {
	// machine.LED.Set(lightOn)
}

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelNone,
	})
	if err != nil {
		println("could not enable radio:", err)
		return
	}

	println("starting radio...")
	err = espradio.Start()
	if err != nil {
		println("could not start radio:", err)
		return
	}

	println("connecting to", wifiSSID, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     wifiSSID,
		Password: wifiPass,
	})
	if err != nil {
		println("connect failed:", err)
		return
	}
	println("connected to", wifiSSID, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		println("netdev failed:", err)
		return
	}

	println("creating lneto stack...")
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    "espradio",
		MaxUDPPorts: 2,
		MaxTCPPorts: 1,
	})
	if err != nil {
		println("stack failed:", err)
		return
	}

	// Start the poll loop in the background.
	// VERY IMPORTANT TO START BEFORE USING STACK!
	go loopForeverStack(espstack)

	println("starting DHCP...")
	dhcp, err := espstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err)
		return
	}
	println("got IP:", dhcp.AssignedAddr.String())

	lstack := espstack.LnetoStack()
	rstack := lstack.StackRetrying(pollTime)
	gatewayHW, err := rstack.DoResolveHardwareAddress6(dhcp.Router, 500*time.Millisecond, 4)
	if err != nil {
		panic("ARP resolve failed: " + err.Error())
	}
	lstack.SetGateway6(gatewayHW)

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
		NewUserData: func() any {
			cs := new(connState)
			cs.hdr.Reset(cs.httpBuf[:])
			return cs
		},
	})
	if err != nil {
		panic("tcppool create: " + err.Error())
	}

	listenAddr := netip.AddrPortFrom(dhcp.AssignedAddr, listenPort)
	var listener tcp.Listener
	err = listener.Reset(listenPort, tcpPool)
	if err != nil {
		panic("listener reset: " + err.Error())
	}
	err = lstack.RegisterListener(&listener)
	if err != nil {
		panic("listener register: " + err.Error())
	}

	print("listening", "http://"+listenAddr.String())
	lstack.Debug("init-complete")

	// Pre-allocate worker goroutines so stacks are allocated once at startup
	// instead of per-connection. Maintains full concurrency up to maxConns.
	jobCh := make(chan connJob, maxConns)
	for range maxConns {
		go connWorker(jobCh)
	}
	lstack.Debug("goroutines allocated")
	for {
		if listener.NumberOfReadyToAccept() == 0 {
			time.Sleep(pollTime)
			tcpPool.CheckTimeouts()
			continue
		}

		conn, userData, err := listener.TryAccept()
		if err != nil {
			println("err listener accept:", err.Error())
			time.Sleep(time.Second)
			continue
		}
		jobCh <- connJob{conn: conn, cs: userData.(*connState), stack: lstack}
	}
}

// connState holds all per-connection buffers, pre-allocated during pool init.
// Eliminates per-connection heap escapes of local arrays (buf, dynBuf, csBuf)
// and the make([]byte, httpBuf) that exceeds TinyGo's 256-byte stack limit.
type connState struct {
	hdr     httpraw.Header
	httpBuf [httpBuf]byte
	buf     [128]byte
	dynBuf  [256]byte
	csBuf   [9]byte
}

type connJob struct {
	conn  *tcp.Conn
	cs    *connState
	stack *xnet.StackAsync
}

type page uint8

const (
	pageNotExists page = iota
	pageLanding
	pageToggleLED
)

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

var state ServerState

func (s *ServerState) RecordToggle(callsign []byte) {
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

func parseCallsignValue(query []byte) []byte {
	const key = "callsign="
	idx := bytes.Index(query, []byte(key))
	if idx < 0 || (idx > 0 && query[idx-1] != '&') {
		return nil
	}
	val := query[idx+len(key):]
	if end := bytes.IndexByte(val, '&'); end >= 0 {
		val = val[:end]
	}
	return val
}

func sanitizeCallsign(dst, raw []byte) []byte {
	dst = dst[:0]
	for _, b := range raw {
		if (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') {
			break
		}
		dst = append(dst, b)
		if len(dst) >= 4 {
			break
		}
	}
	if len(dst) == 0 {
		dst = append(dst, "(unknown)"...)
	}
	return dst
}

func connWorker(ch <-chan connJob) {
	for job := range ch {
		handleConn(job.conn, job.cs, job.stack)
	}
}

func handleConn(conn *tcp.Conn, cs *connState, stack *xnet.StackAsync) {
	defer conn.Close()
	const AsRequest = false
	hdr := &cs.hdr
	hdr.Reset(nil)
	buf := cs.buf[:]

	stack.Debug("conn-start")
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	remoteAddr, _ := netip.AddrFromSlice(conn.RemoteAddr())
	print("incoming connection: ")
	printAddr(remoteAddr, buf[:0])
	println(" from port", conn.RemotePort())
	stack.Debug("post-deadline+println")

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			hdr.ReadFromBytes(buf[:n])
			needMoreData, err := hdr.TryParse(AsRequest)
			if err != nil && !needMoreData {
				println("parsing HTTP request:", err.Error())
				return
			}
			if !needMoreData {
				break
			}
		}
		if err != nil {
			println("read error:", err.Error())
			return
		}
		closed := conn.State() != tcp.StateEstablished
		if closed {
			break
		} else if hdr.BufferReceived() >= httpBuf {
			println("too much HTTP data")
			return
		}
	}
	// BEGIN PARSING REQUEST.
	uri := hdr.RequestURI()
	uriPath := uri
	var uriQuery []byte
	if qIdx := bytes.IndexByte(uri, '?'); qIdx >= 0 {
		uriPath = uri[:qIdx]
		uriQuery = uri[qIdx+1:]
	}

	var requestedPage page
	switch {
	case bytesEqual(uriPath, "/"):
		println("Got webpage request!")
		requestedPage = pageLanding
	case bytesEqual(uriPath, "/toggle-led"):
		println("got toggle led request")
		requestedPage = pageToggleLED
		callsign := sanitizeCallsign(cs.csBuf[:0], parseCallsignValue(uriQuery))
		state.RecordToggle(callsign)
	}

	stack.Debug("post-read-loop")
	// BEGIN RESPONSE.
	// Reuse header to write response.
	hdr.Reset(nil)
	stack.Debug("post-reset")
	hdr.SetProtocol("HTTP/1.1")
	stack.Debug("post-setproto")
	if requestedPage == pageNotExists {
		hdr.SetStatus("404", "Not Found")
	} else {
		hdr.SetStatus("200", "OK")
	}
	stack.Debug("post-setstatus")
	// We call Close() on exiting this function.
	// If we omit Connection:close in header we'll have notably slower paint times in browser.
	// One thing to keep in mind when using Connection:close is using Content-Length to prevent early browser close.
	hdr.Set("Connection", "close")
	stack.Debug("post-set-conn-close")
	switch requestedPage {
	case pageLanding:
		dynContent := state.AppendActionsHTML(cs.dynBuf[:0])
		contentLength := len(htmlTemplate) + len(dynContent)
		buf = strconv.AppendUint(buf[:0], uint64(contentLength), 10)
		stack.Debug("post-appendhtml")
		hdr.Set("Content-Type", "text/html")
		hdr.SetBytes("Content-Length", buf)
		responseHeader, err := hdr.AppendResponse(buf[:0])
		if err != nil {
			println("error appending:", err.Error())
		}
		conn.Write(responseHeader)
		conn.Write(htmlTemplate[:htmlActionIdx])
		conn.Write(dynContent)
		conn.Write(htmlTemplate[htmlActionIdx:])
		time.Sleep(pollTime)

	case pageToggleLED:
		hdr.Set("Content-Length", "0")
		responseHeader, err := hdr.AppendResponse(buf[:0])
		if err != nil {
			println("error appending:", err.Error())
		}
		conn.Write(responseHeader)

	default:
		responseHeader, err := hdr.AppendResponse(buf[:0])
		if err != nil {
			println("error appending:", err.Error())
		}
		conn.Write(responseHeader)
	}
	stack.Debug("pre-close")
}

// printAddr prints a netip.Addr without heap allocation by formatting into buf.
func printAddr(addr netip.Addr, buf []byte) {
	buf = addr.AppendTo(buf[:0])
	print(unsafe.String(&buf[0], len(buf)))
}

// bytesEqual compares a byte slice to a string without heap allocation.
func bytesEqual(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	if len(b) == 0 {
		return true
	}
	return unsafe.String(&b[0], len(b)) == s
}

func loopForeverStack(stack *espradio.Stack) {
	for {
		send, recv, _ := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
		}
	}
}
