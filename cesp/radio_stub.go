//go:build !(esp32c3 || esp32c3_qemu_target || esp32s3)

package cesp

import "unsafe"

const (
	logLevelNone    = LogLevel(0)
	logLevelError   = LogLevel(1)
	logLevelWarning = LogLevel(2)
	logLevelInfo    = LogLevel(3)
	logLevelDebug   = LogLevel(4)
	logLevelVerbose = LogLevel(5)

	wifiModeNone = WifiMode(0)
	wifiModeSTA  = WifiMode(1)
	wifiModeAP   = WifiMode(2)

	wifiEventSTAConnected    = int32(4)
	wifiEventSTADisconnected = int32(5)
	wifiEventSTAStart        = int32(2)
)

func wifiInit() error                                          { panic("espradio: not an ESP32 target") }
func wifiInitCompleted()                                       { panic("espradio: not an ESP32 target") }
func wifiEspStart() error                                      { panic("espradio: not an ESP32 target") }
func wifiSetBlobLogLevel(_ LogLevel)                           { panic("espradio: not an ESP32 target") }
func wifiSetPS(_ bool)                                         { panic("espradio: not an ESP32 target") }
func wifiGetMode() (WifiMode, error)                           { panic("espradio: not an ESP32 target") }
func wifiSetMode(_ WifiMode) error                             { panic("espradio: not an ESP32 target") }
func wifiSetCountryEU() error                                  { panic("espradio: not an ESP32 target") }
func wifiEnsureOSIPtr()                                        { panic("espradio: not an ESP32 target") }
func wifiRestoreROMPtrs()                                      { panic("espradio: not an ESP32 target") }
func wifiSaveROMPtrs()                                         { panic("espradio: not an ESP32 target") }
func halInitClocksGo()                                         { panic("espradio: not an ESP32 target") }
func wifiSetSTAConfig(_, _ string) error                       { panic("espradio: not an ESP32 target") }
func wifiConnect() error                                       { panic("espradio: not an ESP32 target") }
func wifiScan() ([]APRecord, error)                            { panic("espradio: not an ESP32 target") }
func wifiSetAPConfig(_, _ string, _ uint8, _ bool) error       { panic("espradio: not an ESP32 target") }
func sniffBegin(_ uint8) error                                 { panic("espradio: not an ESP32 target") }
func sniffCount() uint32                                       { panic("espradio: not an ESP32 target") }
func sniffEnd() error                                          { panic("espradio: not an ESP32 target") }
func parseWifiEvent(_ int32, _ unsafe.Pointer) WifiEvent       { panic("espradio: not an ESP32 target") }
