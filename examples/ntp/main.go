// This example connects with Esplink and sets the clock with NTP once per minute.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/ntp
package main

import (
	"runtime"
	"time"

	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	link "tinygo.org/x/espradio/netlink"
)

var (
	ssid     string
	password string
)

const (
	ntpHost      = "pool.ntp.org"
	syncInterval = time.Minute
)

func main() {
	time.Sleep(time.Second)

	link := link.Esplink{}
	netdev.UseNetdev(&link)

	println("connecting to", ssid, "...")
	err := link.NetConnect(&nl.ConnectParams{
		Ssid:       ssid,
		Passphrase: password,
	})
	if err != nil {
		failure("connect failed (ensure antenna connected): " + err.Error())
	}
	println("connected. time is", time.Now().String())

	for {
		offset, err := link.NTP(ntpHost, 5*time.Second, 3)
		if err != nil {
			println("NTP failed:", err.Error())
		} else {
			runtime.AdjustTimeOffset(int64(offset))
			println("NTP offset", offset.String(), "time is", time.Now().String())
		}
		time.Sleep(syncInterval)
	}
}

func failure(msg string) {
	for {
		println("stopped after failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
