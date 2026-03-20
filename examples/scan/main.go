package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelVerbose,
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

	println("scanning WiFi...")
	aps, err := espradio.Scan()
	if err != nil {
		println("could not scan wifi:", err)
		return
	}

	for _, ap := range aps {
		println("AP:", ap.SSID, "RSSI", ap.RSSI)
	}

	c := 1
	for {
		println("scan finished", c)
		c++
		time.Sleep(1 * time.Second)
	}
}
