// This example connects with Esplink and listens to a destination and prints UDP packets received and responds to each once.
// It needs setting of the IP which it wants to "target" or listen to:
//
//	tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword -X main.target=192.168.1.xxx" -monitor ./examples/udp-no-allocs
//
// After flashing read output to find out the ip address of esp32
// and send a UDP packet and see what it answers:
//
//	echo hello | nc -u -p 7886 [esp32 ipv4 i.e:192.168.1.2] 7885
package main

import (
	"net/netip"
	"time"
	"unsafe"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/ipv4"
	"github.com/soypat/lneto/udp"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
	target   string // set to IPv4 addr i.e: 192.168.1.2
)

const (
	targetPort = 7886
	localPort  = 7885
)

func main() {
	time.Sleep(time.Second)
	targetAddr, err := netip.ParseAddr(target)
	if err != nil {
		failure("parse target IP "+target, err)
	} else if !targetAddr.Is4() {
		failure("example setup for IPv4, got "+target, nil)
	}
	println("start")
	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("connecting to", ssid, "...")
	err = link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("connect failed", err)
	}
	stack := link.StackAsync()
	println("connected as", string(ipv4.AppendFormatAddr(nil, stack.Addr4())), "port", localPort)

	mtu := stack.MTU()
	udpPktSize := mtu - 20 - 8 // remove IPv4 and UDP header size.
	packetQueue := 1
	println("config and open UDP...")
	var c udp.Conn
	err = c.Configure(udp.ConnConfig{
		RxBuf:       make([]byte, packetQueue*udpPktSize),
		TxBuf:       make([]byte, packetQueue*udpPktSize),
		RxQueueSize: packetQueue,
		TxQueueSize: packetQueue,
		RWBackoff:   backoff,
		MTU:         uint16(mtu),
	})
	if err != nil {
		failure("config udp", err)
	}
	println("dial UDP")

	err = stack.DialUDP4(&c, localPort, targetAddr.As4(), targetPort)
	if err != nil {
		failure("dial udp", err)
	}
	buf := make([]byte, udpPktSize)
	for {
		println("listen for next UDP packet...")
		n, err := c.Read(buf) // [udp.Conn.Read] blocks until message received using backoff.
		if err != nil {
			failure("udp read", err)
		}
		println("udp rx", n, ":", unsafe.String(&buf[0], n))
		// write response:
		n = copy(buf, "hello from esp32!")
		_, err = c.Write(buf[:n])
		if err != nil {
			// Can happen if stack has not sent last packet and packet queue full.
			println("udp write error:", err.Error())
		}
	}
}

func failure(msg string, err error) {
	if err != nil {
		msg = msg + ": " + err.Error()
	}
	for {
		println("stopped after failure:", msg)
		time.Sleep(1 * time.Second)
	}
}

func backoff(consecutiveBackoffs uint) (flagOrSleep time.Duration) {
	if consecutiveBackoffs < 5 {
		flagOrSleep = lneto.BackoffFlagGosched
	} else {
		flagOrSleep = 5 * time.Millisecond
	}
	return flagOrSleep
}
