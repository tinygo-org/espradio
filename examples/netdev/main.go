// This example connects with espradio.Link and runs an lneto stack using the
// lneto x/netdev Interface and Runner.
// tinygo flash -target xiao-esp32c3 -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" ./examples/netdev
package main

import (
	"context"
	"net/netip"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/netdev"
	"github.com/soypat/lneto/x/xnet"
	"tinygo.org/x/espradio"
)

var (
	ssid     string
	password string
)

func main() {
	time.Sleep(time.Second)

	params := espradio.STAConfig{SSID: ssid, Password: password}
	link := espradio.Link{Config: espradio.Config{Logging: espradio.LogLevelError}}
	link.LinkNotify(func(connected bool) (int, espradio.STAConfig) {
		println("link connected:", connected)
		if !connected {
			return 3, params
		}
		return 0, espradio.STAConfig{}
	})

	println("connecting to", ssid, "...")
	failIfErr("connect (ensure antenna connected)", link.LinkConnect(params))
	dev := link.NetDev()

	hw, err := dev.HardwareAddr6()
	failIfErr("hardware addr", err)

	var stack xnet.Netstack
	err = stack.Reset(xnet.NetstackConfig{
		StackConfig: xnet.StackConfig{
			RandSeed:          time.Now().UnixNano() | 1,
			Hostname:          "espradio-netdev",
			MaxActiveTCPPorts: 4,
			MaxActiveUDPPorts: 4,
			ICMPQueueLimit:    1,
			MTU:               1500,
			PassivePeers:      4,
			HardwareAddress:   hw,
		},
		BackoffStack: backoff,
		TCPPoolConfig: xnet.TCPPoolConfig{
			PoolSize:           4,
			QueueSize:          4,
			TxBufSize:          2048,
			RxBufSize:          512,
			NewBackoff:         func() lneto.BackoffStrategy { return backoff },
			EstablishedTimeout: 5 * time.Second,
			ClosingTimeout:     3 * time.Second,
		},
	})
	failIfErr("stack reset", err)
	failIfErr("icmp enable", stack.EnableICMP(true))

	var iface netdev.Interface[espradio.STAConfig]
	failIfErr("init iface", iface.Init(&link, dev, netdev.InterfaceConfig{}))

	var runner netdev.Runner[espradio.STAConfig]
	failIfErr("runner config", runner.Configure(netdev.RunnerConfig[espradio.STAConfig]{
		Buffers: iface.RunnerBuffers(2),
		Backoff: backoff,
		Flags:   netdev.RunnerInterfacePoll,
	}))
	go func() {
		failIfErr("runner", runner.Run(context.Background(), &iface, &stack))
	}()

	assigned, gateway, subnetBits, err := stack.EnableDHCP(context.Background(), true, netip.Addr{})
	failIfErr("dhcp", err)
	println("assigned", assigned.String(), "gateway", gateway.String(), "subnet", subnetBits)
	println("ready, ping", assigned.String())

	select {}
}

func backoff(consecutiveBackoffs uint) time.Duration {
	return 5 * time.Millisecond
}

func failIfErr(msg string, err error) {
	if err == nil {
		return
	}
	for {
		println("stopped after failure:", msg, err.Error())
		time.Sleep(time.Second)
	}
}
