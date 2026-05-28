//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

import "unsafe"

func timerPollDue(_ int) int        { panic("espradio: not an ESP32 target") }
func espTimerPollDue(_ int) int     { panic("espradio: not an ESP32 target") }
func timerFire(_ unsafe.Pointer)    { panic("espradio: not an ESP32 target") }
func eventLoopRunOnce()             { panic("espradio: not an ESP32 target") }
func eventRegisterDefaultCB()       { panic("espradio: not an ESP32 target") }

const osiTimeBlocking = uint32(0xFFFFFFFF)
