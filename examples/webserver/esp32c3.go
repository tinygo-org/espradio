//go:build esp32c3

package main

// do nothing because the Xiao ESP32-C3 doesn't have an onboard LED.

func initLED() {}

func setLED(lightOn bool) {}
