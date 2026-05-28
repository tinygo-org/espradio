package cesp

import "unsafe"

// LogLevel controls WiFi blob verbosity.
type LogLevel uint8

const (
	LogLevelNone    LogLevel = logLevelNone
	LogLevelError   LogLevel = logLevelError
	LogLevelWarning LogLevel = logLevelWarning
	LogLevelInfo    LogLevel = logLevelInfo
	LogLevelDebug   LogLevel = logLevelDebug
	LogLevelVerbose LogLevel = logLevelVerbose
)

// WifiMode is the operating mode of the WiFi driver.
type WifiMode uint32

const (
	WifiModeNone WifiMode = wifiModeNone
	WifiModeSTA  WifiMode = wifiModeSTA
	WifiModeAP   WifiMode = wifiModeAP
)

// WifiEvent carries parsed data from the blob's WiFi event callback.
type WifiEvent struct {
	ID      int32
	SSID    string
	Channel uint8
	Reason  uint8
}

const (
	WifiEventSTAConnected    = wifiEventSTAConnected
	WifiEventSTADisconnected = wifiEventSTADisconnected
	WifiEventSTAStart        = wifiEventSTAStart
)

// APRecord is a single result from a WiFi scan.
type APRecord struct {
	SSID string
	RSSI int8
}

// WifiInit initializes the WiFi driver and blob library.
func WifiInit() error { return wifiInit() }

// WifiInitCompleted signals to the blob that init is complete.
func WifiInitCompleted() { wifiInitCompleted() }

// WifiEspStart starts the WiFi driver (posts START to ppTask).
func WifiEspStart() error { return wifiEspStart() }

// WifiSetBlobLogLevel sets the WiFi blob's internal log level.
func WifiSetBlobLogLevel(level LogLevel) { wifiSetBlobLogLevel(level) }

// WifiSetPS enables (false) or disables (true) power-save mode.
func WifiSetPS(disablePS bool) { wifiSetPS(disablePS) }

// WifiGetMode returns the current WiFi operating mode.
func WifiGetMode() (WifiMode, error) { return wifiGetMode() }

// WifiSetMode sets the WiFi operating mode.
func WifiSetMode(mode WifiMode) error { return wifiSetMode(mode) }

// WifiSetCountryEU configures the EU regulatory domain.
func WifiSetCountryEU() error { return wifiSetCountryEU() }

// WifiEnsureOSIPtr ensures the OSI adapter pointer is valid.
func WifiEnsureOSIPtr() { wifiEnsureOSIPtr() }

// WifiRestoreROMPtrs restores critical ROM pointer variables.
func WifiRestoreROMPtrs() { wifiRestoreROMPtrs() }

// WifiSaveROMPtrs snapshots critical ROM pointer variables.
func WifiSaveROMPtrs() { wifiSaveROMPtrs() }

// HalInitClocksGo initializes hardware clocks via the chip-specific shim.
func HalInitClocksGo() { halInitClocksGo() }

// WifiSetSTAConfig sets SSID and password for station mode.
func WifiSetSTAConfig(ssid, password string) error { return wifiSetSTAConfig(ssid, password) }

// WifiConnect initiates association with the configured AP.
func WifiConnect() error { return wifiConnect() }

// WifiScan performs an active scan and returns discovered access points.
func WifiScan() ([]APRecord, error) { return wifiScan() }

// WifiSetAPConfig configures soft-AP mode parameters.
func WifiSetAPConfig(ssid, password string, channel uint8, authOpen bool) error {
	return wifiSetAPConfig(ssid, password, channel, authOpen)
}

// SniffBegin starts passive packet capture on the given channel.
func SniffBegin(channel uint8) error { return sniffBegin(channel) }

// SniffCount returns the number of packets captured since SniffBegin.
func SniffCount() uint32 { return sniffCount() }

// SniffEnd stops packet capture.
func SniffEnd() error { return sniffEnd() }

// ParseWifiEvent extracts Go-typed event data from the C blob event callback.
// data must be the unsafe.Pointer received by the espradio_on_wifi_event //export callback.
func ParseWifiEvent(eventID int32, data unsafe.Pointer) WifiEvent {
	return parseWifiEvent(eventID, data)
}
