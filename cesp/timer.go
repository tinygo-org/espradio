package cesp

import "unsafe"

// TimerPollDue fires up to maxFire pending soft-timers. Returns how many fired.
func TimerPollDue(maxFire int) int { return timerPollDue(maxFire) }

// EspTimerPollDue fires up to maxFire pending esp_timer callbacks. Returns how many fired.
func EspTimerPollDue(maxFire int) int { return espTimerPollDue(maxFire) }

// TimerFire invokes the C-side timer callback for timer.
func TimerFire(timer unsafe.Pointer) { timerFire(timer) }

// EventLoopRunOnce drains one pending WiFi event from the blob event loop.
func EventLoopRunOnce() { eventLoopRunOnce() }

// EventRegisterDefaultCB installs the default WiFi event handler.
func EventRegisterDefaultCB() { eventRegisterDefaultCB() }

// OSITimeBlocking is the sentinel block-time value meaning "wait forever".
const OSITimeBlocking = osiTimeBlocking
