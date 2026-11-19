//go:build esp32c3 || esp32c6 || esp32s3 || esp32

package espradio

import "runtime/interrupt"

var wifiISR interrupt.Interrupt

func enableWiFiISR() {
	wifiISR = interrupt.New(wifiCPUInterrupt, wifiISRHandler)
	wifiISR.Enable()
}
