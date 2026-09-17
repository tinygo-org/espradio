// This example demonstrates how to send a raw 802.11 frame using the ESP32 radio.
// It beacons an open network from the board own MAC on channel 1, then prints the
// queue result and the hardware tx done counts. The beacon is a test frame and not
// a usable access point.
//
// tinygo flash -target esp32-mini32 -monitor ./examples/inject
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

var ssid = "tinygo"

// beacon builds a minimal open-network 802.11 beacon frame from mac (ref: IEEE 802.11-2020 9.3.3.3).
func beacon(mac [6]byte) []byte {
	f := make([]byte, 0, 64)

	f = append(f, 0x80, 0x00, 0x00, 0x00)
	f = append(f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	f = append(f, mac[:]...)
	f = append(f, mac[:]...)
	f = append(f, 0x00, 0x00)

	f = append(f, 0, 0, 0, 0, 0, 0, 0, 0)
	f = append(f, 0x64, 0x00)
	f = append(f, 0x01, 0x04)

	f = append(f, 0x00, byte(len(ssid)))
	f = append(f, ssid...)
	f = append(f, 0x01, 0x01, 0x82)

	return f
}

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

	if err := espradio.BeginMonitor(1); err != nil {
		println("could not enter monitor mode:", err)
		return
	}

	if err := espradio.StartRawTXTracking(); err != nil {
		println("could not register tx callback:", err)
		return
	}

	mac, err := espradio.StationMAC()
	if err != nil {
		println("could not read MAC:", err)
		return
	}
	frame := beacon(mac)
	println("beaconing SSID", ssid, "on channel 1")
	var queued, refused int
	for i := 0; ; i++ {
		// Bump the 802.11 sequence number in the sequence control field at byte 22.
		seq := uint16(i) << 4
		frame[22] = byte(seq)
		frame[23] = byte(seq >> 8)
		if err := espradio.SendRawFrame(frame); err != nil {
			refused++
		} else {
			queued++
		}
		if i%100 == 0 {
			println("queued:", queued, "refused:", refused,
				"hw sent:", int(espradio.RawFramesSent()), "hw failed:", int(espradio.RawFramesFailed()))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
