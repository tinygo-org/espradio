//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

func netifStartRx(_ int) error           { panic("espradio: not an ESP32 target") }
func netifTx(_ []byte) error             { panic("espradio: not an ESP32 target") }
func netifRxAvailable() bool             { panic("espradio: not an ESP32 target") }
func netifRxPop(_ []byte) int            { panic("espradio: not an ESP32 target") }
func netifGetMAC() ([6]byte, error)      { panic("espradio: not an ESP32 target") }
func netifRxStats() (uint32, uint32)     { panic("espradio: not an ESP32 target") }
func netifSetConnected(_ bool)           { panic("espradio: not an ESP32 target") }
func netifInitNetstackCB()               { panic("espradio: not an ESP32 target") }
