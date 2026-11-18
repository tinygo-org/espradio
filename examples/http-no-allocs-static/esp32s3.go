//go:build esp32s3 || esp32 || esp32c6

package main

import (
	"machine"
)

func init() {
	machine.LED.Configure(machine.PinConfig{Mode: machine.PinOutput})
}

func setLED(lightOn bool) {
	machine.LED.Set(lightOn)
}
