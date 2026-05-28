//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

func arenaInit(_ []byte)               { panic("espradio: not an ESP32 target") }
func arenaStats() (uint32, uint32)     { panic("espradio: not an ESP32 target") }
