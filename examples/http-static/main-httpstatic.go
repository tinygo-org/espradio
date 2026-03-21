// This example shows how to create a simple HTTP server that serves a static webpage. The server listens for incoming HTTP requests and responds with the contents of the embedded `index.html` file. You can test it by connecting to the same Wi-Fi network as the ESP32 and navigating to the IP address assigned to the ESP32 in your browser.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor -stack-size 8kb ./examples/http-static
package main

import (
	_ "embed"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/soypat/lneto/http/httpraw"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

const (
	pollTime   = 5 * time.Millisecond
	maxConns   = 4
	httpBuf    = 1024
	listenPort = 80
)

var (
	//go:embed index.html
	webPage      []byte
	lastLEDState bool
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

	println("connecting to", ssid, "...")
	err = espradio.Connect(espradio.STAConfig{
		SSID:     ssid,
		Password: password,
	})
	if err != nil {
		println("connect failed:", err)
		return
	}
	println("connected to", ssid, "!")

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		println("netdev failed:", err)
		return
	}

	println("creating lneto stack...")
	espstack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    ssid,
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

	dhcpResults, err := espstack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		panic("DHCP failed:" + err.Error())
	}
	tcpPool, err := xnet.NewTCPPool(xnet.TCPPoolConfig{
		PoolSize:           maxConns,
		QueueSize:          3,
		TxBufSize:          len(webPage) + 128,
		RxBufSize:          256,
		EstablishedTimeout: 5 * time.Second,
		ClosingTimeout:     5 * time.Second,
		NewUserData: func() any {
			var hdr httpraw.Header
			buf := make([]byte, httpBuf)
			hdr.Reset(buf)
			return &hdr
		},
	})
	if err != nil {
		panic("tcppool create:" + err.Error())
	}

	lstack := espstack.LnetoStack()
	listenAddr := netip.AddrPortFrom(dhcpResults.AssignedAddr, listenPort)

	// Create and register TCP listener.
	var listener tcp.Listener
	err = listener.Reset(listenPort, tcpPool)
	if err != nil {
		panic("listener reset:" + err.Error())
	}
	err = lstack.RegisterListener(&listener)
	if err != nil {
		panic("listener register:" + err.Error())
	}
	println("listening on", "http://"+listenAddr.String())

	for {
		if listener.NumberOfReadyToAccept() == 0 {
			time.Sleep(5 * time.Millisecond)
			tcpPool.CheckTimeouts()
			continue
		}

		conn, httpBuf, err := listener.TryAccept()
		if err != nil {
			println("listener accept err", err.Error())
			time.Sleep(time.Second)
			continue
		}
		// Beware, this allocates a stack on the heap on
		// every connection. See http-app example on
		// how to allocate a pool of connections up front
		// and avoid heap allocations.
		go handleConn(conn, httpBuf.(*httpraw.Header))
	}
}

type page uint8

const (
	pageNotExits  page = iota
	pageLanding        // /
	pageToggleLED      // /toggle-led
)

func handleConn(conn *tcp.Conn, hdr *httpraw.Header) {
	defer conn.Close()
	const AsRequest = false
	var buf [64]byte
	hdr.Reset(nil)

	remoteAddr, _ := netip.AddrFromSlice(conn.RemoteAddr())
	println("incoming connection:", remoteAddr.String(), "from port", conn.RemotePort())

	for {
		n, err := conn.Read(buf[:])
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
		closed := err == net.ErrClosed || conn.State() != tcp.StateEstablished
		if closed {
			break
		} else if hdr.BufferReceived() >= httpBuf {
			println("too much HTTP data")
			return
		}
	}
	// Check requested requestedPage URI.
	var requestedPage page
	uri := hdr.RequestURI()
	switch string(uri) {
	case "/":
		println("Got webpage request!")
		requestedPage = pageLanding
	case "/toggle-led":
		println("got toggle led request")
		requestedPage = pageToggleLED
		lastLEDState = !lastLEDState
		setLED(lastLEDState)
	}

	// Prepare response with same buffer.
	hdr.Reset(nil)
	hdr.SetProtocol("HTTP/1.1")
	if requestedPage == pageNotExits {
		hdr.SetStatus("404", "Not Found")
	} else {
		hdr.SetStatus("200", "OK")
	}
	var body []byte
	switch requestedPage {
	case pageLanding:
		body = webPage
		hdr.Set("Content-Type", "text/html")
	}
	if len(body) > 0 {
		hdr.Set("Content-Length", strconv.Itoa(len(body)))
	}
	responseHeader, err := hdr.AppendResponse(buf[:0])
	if err != nil {
		println("error appending:", err.Error())
	}
	conn.Write(responseHeader)
	if len(body) > 0 {
		_, err := conn.Write(body)
		if err != nil {
			println("writing body:", err.Error())
		}
		time.Sleep(pollTime)
	}
	// connection closed automatically by defer.
}

func loopForeverStack(stack *espradio.Stack) {
	for {
		send, recv, _ := stack.RecvAndSend()
		if send == 0 && recv == 0 {
			time.Sleep(pollTime)
		}
	}
}
