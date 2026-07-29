// This example demonstrates how to scan for available Wi-Fi access points using the ESP32 radio.
// It initializes the radio, starts it, and then continuously scans for Wi-Fi networks every
// 10 seconds, printing the SSID and RSSI of each detected access point.
//
// tinygo flash -target xiao-esp32c3 -monitor ./examples/scan
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{})
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

	for {
		println("scanning WiFi...")
		aps, err := espradio.Scan()
		if err != nil {
			println("could not scan wifi:", err)
			return
		}

		for _, ap := range aps {
			println("AP:", ap.SSID, "RSSI", ap.RSSI)
		}

		// Driver counters.  Most of these count something being dropped, so a
		// non-zero value is the only evidence it happened.
		espradio.DebugStats().Print()
		println()

		time.Sleep(10 * time.Second)
	}
}
