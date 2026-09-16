// This example stops Wi-Fi between NTP synchronization cycles.
// It reuses the network device and stack after each Start call.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/ntp-power-save
package main

import (
	"net/netip"
	"runtime"
	"time"

	"github.com/soypat/lneto"
	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

const (
	ntpHost      = "pool.ntp.org"
	syncInterval = time.Minute
	pollTime     = 5 * time.Millisecond
)

var pollBackoff = lneto.BackoffStrategy(func(_ uint) time.Duration {
	return pollTime
})

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	// Enable the driver once. Later cycles only call Start.
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
	})
	if err != nil {
		failure("could not enable radio: " + err.Error())
	}

	println("starting radio...")
	if err := espradio.Start(); err != nil {
		failure("could not start radio: " + err.Error())
	}

	println("starting L2 netdev...")
	nd, err := espradio.StartNetDev()
	if err != nil {
		failure("netdev failed: " + err.Error())
	}

	println("creating lneto stack...")
	stack, err := espradio.NewStack(nd, espradio.StackConfig{
		Hostname:    ssid,
		MaxUDPPorts: 2, // DNS + NTP
		MaxTCPPorts: 1,
	})
	if err != nil {
		failure("stack failed: " + err.Error())
	}

	go stackLoop(stack)

	for cycle := 1; ; cycle++ {
		println("--- sync cycle", cycle, "---")
		syncTime(stack)
		println("radio is down; sleeping", syncInterval.String(), "until next sync")
		time.Sleep(syncInterval)

		println("starting radio...")
		if err := espradio.Start(); err != nil {
			failure("could not restart radio: " + err.Error())
		}
	}
}

// syncTime updates the clock and then stops Wi-Fi.
func syncTime(stack *espradio.Stack) {
	println("connecting to", ssid, "...")
	err := espradio.Connect(espradio.STAConfig{
		SSID:     ssid,
		Password: password,
	})
	if err != nil {
		println("connect failed:", err.Error())
		stopRadio()
		return
	}
	println("connected to", ssid, "!")

	println("starting DHCP...")
	dhcp, err := stack.SetupWithDHCP(espradio.DHCPConfig{})
	if err != nil {
		println("DHCP failed:", err.Error())
		stopRadio()
		return
	}
	addr, ok := netip.AddrFromSlice(dhcp.AssignedAddr4[:])
	if !ok {
		println("invalid IP address")
		stopRadio()
		return
	}
	println("got IP:", addr.String())

	ntpSync(stack)

	println("stopping radio...")
	stopRadio()
	println("radio stopped.")
}

func stopRadio() {
	if err := espradio.Stop(); err != nil {
		failure("could not stop radio: " + err.Error())
	}
}

// ntpSync gets the time from the NTP server and updates the runtime clock.
func ntpSync(stack *espradio.Stack) {
	println("resolving ntp host:", ntpHost)
	rstack := stack.LnetoStack().StackRetrying(pollBackoff)

	addrs, err := rstack.DoLookupIP(ntpHost, 5*time.Second, 3)
	if err != nil {
		println("DNS lookup failed:", err.Error())
		return
	}

	offset, err := rstack.DoNTP(addrs[0], 5*time.Second, 3)
	if err != nil {
		println("NTP query failed:", err.Error())
		return
	}

	runtime.AdjustTimeOffset(int64(offset))
	println("NTP success:", time.Now().String())
}

func stackLoop(stack *espradio.Stack) {
	for {
		send, recv, err := stack.RecvAndSend()
		if err != nil {
			println("poll err:", err.Error())
			time.Sleep(pollTime)
			continue
		}
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
