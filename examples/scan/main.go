// This example demonstrates how to scan for available Wi-Fi access points using the ESP32 radio. It initializes the radio, starts it, and then continuously scans for Wi-Fi networks every 10 seconds, printing the SSID and RSSI of each detected access point.
//
// To run this example, use the following command, replacing `YourSSID` and `YourPassword` with your Wi-Fi credentials:
//
// tinygo flash -target xiao-esp32c3 -monitor -stack-size 8kb ./examples/scan
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
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
		println()

		time.Sleep(10 * time.Second)
	}
}
