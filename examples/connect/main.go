package main

import (
	"time"

	"tinygo.org/x/espradio"
)

const (
	wifiSSID = "Kracozabra"
	wifiPass = "09655455"
)

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

	println("starting TCP/IP stack (DHCP)...")
	ns, err := espradio.NewNetStack(nd, espradio.NetStackConfig{Debug: false})
	if err != nil {
		println("netstack failed:", err)
		return
	}
	println("got IP:", fmtIP(ns.IP()))

	// --- HTTP GET ---
	println("resolving httpbin.org...")
	addr, err := ns.Resolve("httpbin.org")
	if err != nil {
		println("DNS failed:", err)
		return
	}
	println("httpbin.org =", fmtIP(addr))

	println("TCP connecting to", fmtIP(addr), ":80 ...")
	fd, err := ns.TCPDial(addr, 80)
	if err != nil {
		println("TCP connect failed:", err)
		return
	}
	println("TCP connected! fd=", fd)

	req := "GET /ip HTTP/1.1\r\nHost: httpbin.org\r\nConnection: close\r\n\r\n"
	println("sending HTTP request...")
	_, err = ns.Send(fd, []byte(req))
	if err != nil {
		println("send failed:", err)
		return
	}

	println("reading response...")
	buf := make([]byte, 1024)
	for {
		n, err := ns.Recv(fd, buf)
		if n > 0 {
			println(string(buf[:n]))
		}
		if err != nil {
			break
		}
	}

	ns.CloseSock(fd)
	println("done!")

	for {
		time.Sleep(1 * time.Second)
		println("alive")
	}
}

func fmtIP(ip [4]byte) string {
	d := func(b byte) string {
		if b < 10 {
			return string(rune('0' + b))
		}
		if b < 100 {
			return string(rune('0'+b/10)) + string(rune('0'+b%10))
		}
		return string(rune('0'+b/100)) + string(rune('0'+(b/10)%10)) + string(rune('0'+b%10))
	}
	return d(ip[0]) + "." + d(ip[1]) + "." + d(ip[2]) + "." + d(ip[3])
}
