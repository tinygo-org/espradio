//go:build esp32s3 || esp32

package main

import (
	"machine"
)

func initLED() {
	machine.LED.Configure(machine.PinConfig{Mode: machine.PinOutput})
	machine.LED.Set(false)
}

func setLED(lightOn bool) {
	machine.LED.Set(lightOn)
}
