//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package cesp

/*
#cgo CFLAGS: -fno-short-enums
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#include "espradio.h"
*/
import "C"
import "unsafe"

func timerPollDue(maxFire int) int {
	return int(C.espradio_timer_poll_due(C.int(maxFire)))
}

func espTimerPollDue(maxFire int) int {
	return int(C.espradio_esp_timer_poll_due(C.int(maxFire)))
}

func timerFire(timer unsafe.Pointer) {
	C.espradio_timer_fire(timer)
}

func eventLoopRunOnce() {
	C.espradio_event_loop_run_once()
}

func eventRegisterDefaultCB() {
	C.espradio_event_register_default_cb()
}

// osiTimeBlocking is the sentinel value meaning "block forever" in OSI queue/semaphore calls.
const osiTimeBlocking = uint32(C.OSI_FUNCS_TIME_BLOCKING)
