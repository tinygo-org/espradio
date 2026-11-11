//go:build !esp32s3 && !esp32

package main

func setLED(lightOn bool) {
	// do nothing because the Xiao ESP32-C3 doesn't have an onboard LED.
}
