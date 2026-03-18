package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("ap: enabling radio...")
	if err := espradio.Enable(espradio.Config{Logging: espradio.LogLevelInfo}); err != nil {
		println("ap: enable err:", err)
		return
	}

	println("ap: starting AP...")
	err := espradio.StartAP(espradio.APConfig{
		SSID:     "espradio-ap",
		Password: "",
		Channel:  6,
		AuthOpen: true,
	})
	if err != nil {
		println("ap: start err:", err)
		return
	}

	println("ap: starting L2 netdev (AP)...")
	nd, err := espradio.StartNetDevAP()
	if err != nil {
		println("ap: netdev err:", err)
		return
	}

	println("ap: starting TCP/IP stack (static IP)...")
	ns, err := espradio.NewNetStack(nd, espradio.NetStackConfig{
		Debug:    true,
		StaticIP: [4]byte{192, 168, 4, 1},
		Mask:     [4]byte{255, 255, 255, 0},
	})
	if err != nil {
		println("ap: netstack err:", err)
		return
	}

	println("ap: starting DHCP server...")
	go ns.RunDHCPServer()

	println("ap: AP is running on", fmtIP(ns.IP()), "— connect to espradio-ap")
	for {
		time.Sleep(3 * time.Second)
		rxCb, rxDrop := espradio.NetifRxStats()
		println("ap: rx_cb=", rxCb, "rx_drop=", rxDrop)
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
