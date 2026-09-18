// This example demonstrates how to count 802.11 frames on each channel using the
// ESP32 radio in monitor mode. It sweeps channels 1 to 11 and prints a per-channel
// frame total every pass.
//
// tinygo flash -target esp32-mini32 -monitor ./examples/sniff
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

func main() {
	time.Sleep(time.Second)

	println("initializing radio...")
	if err := espradio.Enable(espradio.Config{}); err != nil {
		println("could not enable radio:", err)
		return
	}

	println("starting radio...")
	if err := espradio.Start(); err != nil {
		println("could not start radio:", err)
		return
	}

	for {
		for ch := uint8(1); ch <= 11; ch++ {
			n, err := espradio.SniffCountOnChannel(ch, 800*time.Millisecond)
			if err != nil {
				println("ch", ch, "error:", err.Error())
				continue
			}
			println("ch", ch, "frames", n)
		}
		println("---")
		time.Sleep(time.Second)
	}
}
