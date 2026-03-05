package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelInfo,
	})
	if err != nil {
		println("could not enable radio:", err)
		return
	}

	println("scanning WiFi...")
	aps, err := espradio.Scan()
	if err != nil {
		println("could not scan wifi:", err)
		return
	}

	for _, ap := range aps {
		println("AP:", ap.SSID, "RSSI", ap.RSSI)
	}

	println("scan finished")
	for {
		time.Sleep(5 * time.Second)
	}
}
