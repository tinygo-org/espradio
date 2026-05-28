//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

import "unsafe"

func isrIntsOff(_ uint32)                        { panic("espradio: not an ESP32 target") }
func isrWifiUnmask()                             { panic("espradio: not an ESP32 target") }
func isrCallWifiISR()                            { panic("espradio: not an ESP32 target") }
func isrRingTail() uint32                        { panic("espradio: not an ESP32 target") }
func isrRingHead() uint32                        { panic("espradio: not an ESP32 target") }
func isrRingEntryQueue(_ uint32) unsafe.Pointer  { panic("espradio: not an ESP32 target") }
func isrRingEntryItem(_ uint32) unsafe.Pointer   { panic("espradio: not an ESP32 target") }
func isrRingAdvanceTail()                        { panic("espradio: not an ESP32 target") }
func isrRingDrops() uint32                       { panic("espradio: not an ESP32 target") }
func isrGetWifiISRCount() uint32                 { panic("espradio: not an ESP32 target") }
func isrWifiIntRaisePriority()                   { panic("espradio: not an ESP32 target") }
func isrPrewireWifiInterrupts()                  { panic("espradio: not an ESP32 target") }
func isrWifiIntToLevel()                         { panic("espradio: not an ESP32 target") }
