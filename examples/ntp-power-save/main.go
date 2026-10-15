// This example stops Wi-Fi between NTP synchronization cycles.
// It reuses the network device and stack after each Start call.
//
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" -monitor ./examples/ntp-power-save
package main

import (
	"context"
	"net/netip"
	"runtime"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/dns"
	"github.com/soypat/lneto/x/xnet"
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

	hw, err := nd.HardwareAddr6()
	if err != nil {
		failure("mac failed: " + err.Error())
	}

	println("creating lneto stack...")
	stack := new(xnet.Netstack)
	err = stack.Reset(xnet.NetstackConfig{
		StackConfig: xnet.StackConfig{
			Hostname:          ssid,
			RandSeed:          time.Now().UnixNano() | 1,
			HardwareAddress:   hw,
			MTU:               1500,
			MaxActiveUDPPorts: 2, // DNS + NTP
			MaxActiveTCPPorts: 1,
		},
		BackoffStack: pollBackoff,
		TCPPoolConfig: xnet.TCPPoolConfig{
			NewBackoff: func() lneto.BackoffStrategy { return pollBackoff },
		},
	})
	if err != nil {
		failure("stack failed: " + err.Error())
	}

	go func() {
		failure("stack stopped: " + espradio.RunStack(context.Background(), nd, stack).Error())
	}()

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
func syncTime(stack *xnet.Netstack) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	addr, _, _, err := stack.EnableDHCP(ctx, true, netip.Addr{})
	cancel()
	if err != nil {
		println("DHCP failed:", err.Error())
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
func ntpSync(stack *xnet.Netstack) {
	println("resolving ntp host:", ntpHost)
	rstack := stack.StackAsync().StackRetrying(pollBackoff)

	// Lookups fail when the reply has more addresses than fit, so leave room.
	var addrs [8]netip.Addr
	n, err := rstack.DoLookupIP(addrs[:], dns.MustNewName(ntpHost), 5*time.Second, 3)
	if n == 0 {
		println("DNS lookup failed:", err)
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

func failure(msg string) {
	for {
		println("stopped after failure:", msg)
		time.Sleep(1 * time.Second)
	}
}
