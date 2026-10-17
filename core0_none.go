//go:build (esp32c3 || esp32c3_qemu_target || esp32s3 || esp32) && !scheduler.cores

package espradio

import "time"

func pinCore0() {}

func unpinCore0() {}

func onCore0Direct() bool { return true }

func onCore0(fn func()) { fn() }

var isrKick chan struct{}

var schedTicker *time.Ticker

func initKick() {
	isrKick = make(chan struct{}, 1)
	schedTicker = time.NewTicker(schedTickerMs * time.Millisecond)
}

func kickSched() {
	select {
	case isrKick <- struct{}{}:
	default:
	}
}

// waitSchedKick waits for a kick or for one scheduler tick.
func waitSchedKick() {
	select {
	case <-schedTicker.C:
	case <-isrKick:
	}
}
