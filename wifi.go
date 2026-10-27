package espradio

import (
	"errors"
	"time"
)

// LogLevel sets how much the WiFi blob logs.
type LogLevel uint8

const (
	LogLevelNone LogLevel = iota
	LogLevelError
	LogLevelWarning
	LogLevelInfo
	LogLevelDebug
	LogLevelVerbose
)

func (l LogLevel) String() string {
	switch l {
	case LogLevelNone:
		return "NONE"
	case LogLevelError:
		return "ERROR"
	case LogLevelWarning:
		return "WARN"
	case LogLevelInfo:
		return "INFO"
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelVerbose:
		return "VERBOSE"
	default:
		return "?"
	}
}

// Config configures the radio and its driver.
type Config struct {
	Logging LogLevel
	// ArenaPoolSize overrides the default per-target arena pool size (bytes).
	// Zero means use the target default.
	ArenaPoolSize int
	// RxRingSize overrides the default per-target RX ring size (frames).
	// Zero means use the target default. ESP32 is capped at 6.
	RxRingSize int
	// MaxTxPower caps the TX power in units of 0.25 dBm, range 8 to 84.
	// Zero keeps the blob default. See SetMaxTxPower.
	MaxTxPower int8
}

// AccessPoint represents a Wi-Fi access point discovered during scanning.
type AccessPoint struct {
	SSID string
	RSSI int
}

// STAConfig configures station mode connection parameters.
type STAConfig struct {
	SSID     string
	Password string
}

// ConnectResult represents the result of a connection attempt.
type ConnectResult struct {
	Connected bool
	SSID      string
	Channel   uint8
	Reason    uint8
}

// APConfig configures soft-AP mode parameters.
type APConfig struct {
	SSID     string
	Password string
	Channel  uint8
	AuthOpen bool
}

// ErrAlreadyEnabled is returned by Enable when the radio is already enabled.
var ErrAlreadyEnabled = errors.New("espradio: radio already enabled")

// ErrEnableFailed is returned by Enable when an earlier call to it failed.  That
// failure is not recoverable in software: the driver has no teardown path, so the
// device has to be reset before the radio can be brought up again.
var ErrEnableFailed = errors.New("espradio: earlier Enable failed, reset required")

// ErrNotConnected is returned when the station is not connected to an AP.
var ErrNotConnected = errors.New("espradio: station not connected")

// Enable and configure the radio for WiFi.
//
// Enable is not idempotent and returns ErrAlreadyEnabled on a second call.  If an
// earlier call failed, every later one returns ErrEnableFailed: a partially
// initialised radio cannot be unwound, so the device must be reset.
func Enable(config Config) (err error) {
	onCore0(func() { err = enable(config) })
	return
}

// Start starts the Wi-Fi driver and connects to the AP if in station mode.
// Blocks until the driver is ready.  Start is separate from Enable to allow
// configuration (e.g. country code) before starting, and to allow scanning without
// starting the driver.  Start calls schedOnce in a loop to let the blob process
// its internal startup sequence (posting events, etc.) before Start returns.
func Start() (err error) {
	onCore0(func() { err = start() })
	return
}

// Stop stops the Wi-Fi driver and powers down the radio.
// It does nothing if the driver is not started.
func Stop() (err error) {
	onCore0(func() { err = stop() })
	return
}

// Scan performs a single Wi-Fi scan pass and returns the list of discovered access points.
func Scan() (v []AccessPoint, err error) {
	onCore0(func() { v, err = scan() })
	return
}

// Connect configures STA credentials and initiates association.
// Blocks until CONNECTED, DISCONNECTED or timeout.
func Connect(cfg STAConfig) (err error) {
	onCore0(func() { err = connect(cfg) })
	return
}

// Disconnect disconnects the station from its access point.
func Disconnect() (err error) {
	onCore0(func() { err = disconnect() })
	return
}

// RSSI returns the signal strength in dBm of the AP the station is connected to.
func RSSI() (v int, err error) {
	onCore0(func() { v, err = rssi() })
	return
}

// SetLinkNotify sets a callback for station connects and disconnects that happen
// outside a Connect call. The callback runs on its own goroutine.
func SetLinkNotify(cb func(connected bool)) {
	linkMu.Lock()
	defer linkMu.Unlock()
	linkNotifyCb = cb
	if linkEvents != nil {
		return
	}
	events := make(chan ConnectResult, 4)
	go func() {
		for res := range events {
			linkMu.Lock()
			cb := linkNotifyCb
			linkMu.Unlock()
			if cb != nil {
				cb(res.Connected)
			}
		}
	}()
	connectMu.Lock()
	linkEvents = events
	connectMu.Unlock()
}

// StartAP starts the radio in soft-AP mode with the given configuration.
func StartAP(cfg APConfig) (err error) {
	onCore0(func() { err = startAP(cfg) })
	return
}

// SetMaxTxPower sets the maximum TX power in units of 0.25 dBm. Wi-Fi must be started.
// Range is 8 to 84, see esp_wifi_set_max_tx_power in esp-wifi/c/include/esp_wifi.h.
func SetMaxTxPower(power int8) (err error) {
	onCore0(func() { err = setMaxTxPower(power) })
	return
}

// MaxTxPower returns the maximum TX power in units of 0.25 dBm.
// On ESP32 the blob can report less than was set for values above 56.
func MaxTxPower() (v int8, err error) {
	onCore0(func() { v, err = getMaxTxPower() })
	return
}

// SniffCountOnChannel counts the packets received on channel during duration.
func SniffCountOnChannel(channel uint8, duration time.Duration) (v uint32, err error) {
	onCore0(func() { v, err = sniffCountOnChannel(channel, duration) })
	return
}

// SetChannel sets the Wi-Fi channel, 1 to 13. Use it after Start and before ESP-NOW,
// so peers with Channel 0 send and receive there. See esp_wifi_set_channel in esp_wifi.h.
func SetChannel(channel uint8) (err error) {
	onCore0(func() { err = setChannel(channel) })
	return
}

// BeginMonitor enters promiscuous mode on channel (1 to 13). Do not scan or connect until EndMonitor.
func BeginMonitor(channel uint8) (err error) {
	onCore0(func() { err = beginMonitor(channel) })
	return
}

// EndMonitor leaves promiscuous mode. It does not restore the previous channel.
func EndMonitor() (err error) {
	onCore0(func() { err = endMonitor() })
	return
}

// StationMAC returns the 6-byte MAC of the station interface.
func StationMAC() ([6]byte, error) {
	mac, err := currentESPNowMAC(WiFiInterfaceSTA)
	return [6]byte(mac), err
}

// StartRawTXTracking registers the transmit-complete callback and zeroes the counters.
func StartRawTXTracking() (err error) {
	onCore0(func() { err = startRawTXTracking() })
	return
}

// RawFramesSent returns raw frames the MAC reported sent since the last
// StartRawTXTracking call.
func RawFramesSent() uint32 {
	return rawFramesSent()
}

// RawFramesFailed returns raw frames the MAC reported failed since the last
// StartRawTXTracking call.
func RawFramesFailed() uint32 {
	return rawFramesFailed()
}

// SendRawFrame queues a complete 802.11 MAC frame. Pass driverSeq true once
// connected so the driver owns the sequence field, false to set it yourself.
func SendRawFrame(frame []byte, driverSeq bool) (err error) {
	onCore0(func() { err = sendRawFrame(frame, driverSeq) })
	return
}

// LockToCore0 pins the calling goroutine to core 0, so its radio calls run directly instead of
// on a new goroutine each time. Call UnlockFromCore0 before the goroutine exits.
func LockToCore0() { pinCore0() }

// UnlockFromCore0 undoes LockToCore0. The goroutine stays on core 0.
func UnlockFromCore0() { unpinCore0() }

// ArenaStats returns the current arena usage and capacity in bytes.
func ArenaStats() (used, capacity uint32) {
	return arenaStats()
}

// DebugISRCount returns the number of WiFi ISR invocations (for debugging).
func DebugISRCount() uint32 {
	return debugISRCount()
}

// Stats is a snapshot of the driver's internal counters.
//
// Most of these count something being silently discarded, so a non-zero value is
// the only evidence that it happened.  A counter that stays zero is evidence
// too: it can refute the reason it was added, which is the point.
type Stats struct {
	// Scheduler.
	WiFiISRCount     uint32 // blob WiFi ISR invocations
	SchedReentries   uint32 // schedOnce entered while another pass was in flight
	EventCapHits     uint32 // event-loop drain used its whole budget
	TimerCapHits     uint32 // ets_timer drain used its whole budget
	ESPTimerCapHits  uint32 // esp_timer drain used its whole budget
	YieldsSuppressed uint32 // safeGosched returned without yielding
	// IntsOffNesting is wifiIntsOff as of this read.  Non-zero while the radio
	// is idle means a blob critical section leaked.
	IntsOffNesting uint32

	// SchedPasses is completed schedOnce passes, and SchedPassesPerSec that count
	// over elapsed time.  The nominal rate is 200 Hz; anything far above means the
	// kickSched path is driving the ticker rather than the 5 ms timer.
	SchedPasses       uint32
	SchedPassesPerSec uint32

	// Yield-driven scheduler wakes delivered and suppressed by the throttle, and
	// the interval in force.  A large suppressed count against a modest
	// SchedPassesPerSec is the throttle working.
	KicksDelivered  uint32
	KicksSuppressed uint32
	KickThrottleUs  uint32

	// HWISRCount is real hardware WiFi interrupts, and HWISRPerSec that over
	// elapsed time.  Distinct from WiFiISRCount, which counts invocations of the
	// blob ISR and so tracks scheduler passes on S3 and ESP32.
	//
	// A rate near SchedPassesPerSec on an idle radio means the interrupt is
	// re-firing because something it is routed to is still asserting: the handler
	// masks the line, the pass unmasks it, and a level-triggered source that
	// nothing acked fires again at once.
	HWISRCount  uint32
	HWISRPerSec uint32

	// WiFiISRSlots is the bitmask of ISR slots the blob registered, and
	// WiFiISRHandlerCalls the total blob handler invocations.  The prewiring
	// points several peripheral sources at one CPU interrupt, so fewer registered
	// slots than routed sources means some source has no handler to ack it.
	WiFiISRSlots        uint32
	WiFiISRHandlerCalls uint32

	// UnmaskIntervalUs is the minimum spacing between re-enables of the WiFi CPU
	// interrupt, and UnmaskSuppressed how many passes ended without re-enabling
	// it.  Zero interval means no limit; the ESP32-C3 reports zero because it does
	// not need one.
	UnmaskIntervalUs uint32
	UnmaskSuppressed uint32

	// WiFiISRInstalled is the bitmask of slots that actually hold a handler.
	// Handlers arrive via ets_isr_attach, which writes only the handler table,
	// while WiFiISRSlots is written only by set_intr -- and the dispatcher
	// iterates WiFiISRSlots.  So a bit set here but not there is a handler that
	// exists and is never called, leaving its source unacked.
	WiFiISRInstalled uint32

	// Bring-up pumps.  StartReadyAfterPass and ConnectReadyAfterPass are the pass
	// on which the blob's ROM pointers became fully valid; 0 means the pump never
	// ran, readyNeverSentinel that it ran and they never did.
	StartReadyAfterPass   uint32
	ConnectReadyAfterPass uint32

	// RomPtrsSavedUnready counts save calls that could not latch every pointer.
	// RomPtrsMissing is which ones are still unlatched, as ROM_PTR_* bits:
	// 1 pTxRx, 2 our_tx_eb, 4 our_wait_eb, 8 lmacConfMib_ptr, 16 g_osi_funcs_p.
	RomPtrsSavedUnready uint32
	RomPtrsMissing      uint32

	// Queues and the ISR ring.
	ISRRingDrops    uint32 // ISR could not push: ring full
	ISRRingSendFail uint32 // drained item dropped: destination queue full
	QueueSendFull   uint32 // any queue send rejected because the queue was full

	// RX path.
	RxCallbacks uint32 // frames handed over by the blob
	RxDrops     uint32 // frames dropped because the RX ring was full
	RxOversize  uint32 // frames dropped because they exceeded the read buffer

	// TX path.
	TxAttempts     uint32 // frames handed to esp_wifi_internal_tx
	TxFailNoMem    uint32 // rejected with ESP_ERR_NO_MEM (blob TX buffers gone)
	TxFailOther    uint32 // rejected for any other reason
	TxNotConnected uint32 // refused before the blob: STA not associated
	// TxDoneCB counts TX-done callbacks, i.e. buffers the blob released.  It is
	// not a per-frame acknowledgement: it also fires for frames the blob sends on
	// its own, so it runs well ahead of TxAttempts.
	TxDoneCB    uint32
	TxRetries   uint32 // NO_MEM rejections retried rather than dropped
	TxBusyWaits uint32 // senders that waited for another sender to finish

	// Arena.  Shared by WiFi and the BT controller.
	AllocCount    uint32 // tracked allocations
	FreeCount     uint32 // tracked frees
	ArenaUsed     uint32 // bytes
	ArenaCapacity uint32 // bytes
}

// Print writes the counters with println, one group per line.  Uses println
// rather than fmt to keep it usable from size-constrained builds, matching the
// C-side dump helpers.
func (s Stats) Print() {
	println("espradio sched: isr", s.WiFiISRCount,
		"reentry", s.SchedReentries,
		"caphits ev/tmr/esptmr", s.EventCapHits, s.TimerCapHits, s.ESPTimerCapHits,
		"yieldsupp", s.YieldsSuppressed,
		"intsoff", s.IntsOffNesting)
	println("espradio passes:", s.SchedPasses, "rate/s", s.SchedPassesPerSec,
		"(nominal 200)")
	println("espradio kicks: sent", s.KicksDelivered,
		"suppressed", s.KicksSuppressed,
		"throttle_us", s.KickThrottleUs)
	println("espradio hwisr:", s.HWISRCount, "rate/s", s.HWISRPerSec,
		"slots", s.WiFiISRSlots, "installed", s.WiFiISRInstalled,
		"handlercalls", s.WiFiISRHandlerCalls)
	println("espradio unmask: interval_us", s.UnmaskIntervalUs,
		"suppressed", s.UnmaskSuppressed)
	println("espradio bringup: startready", s.StartReadyAfterPass,
		"connectready", s.ConnectReadyAfterPass,
		"savedunready", s.RomPtrsSavedUnready,
		"missing", s.RomPtrsMissing)
	println("espradio queue: isrdrop", s.ISRRingDrops,
		"isrsendfail", s.ISRRingSendFail,
		"queuefull", s.QueueSendFull)
	println("espradio rx: cb", s.RxCallbacks, "drop", s.RxDrops,
		"oversize", s.RxOversize)
	println("espradio tx: try", s.TxAttempts,
		"nomem", s.TxFailNoMem,
		"other", s.TxFailOther,
		"notconn", s.TxNotConnected,
		"done", s.TxDoneCB,
		"retry", s.TxRetries,
		"busywait", s.TxBusyWaits)
	println("espradio mem: alloc", s.AllocCount,
		"free", s.FreeCount,
		"used", s.ArenaUsed, "of", s.ArenaCapacity)
}

// ReadStats fills s with a snapshot of the driver's internal counters.
func ReadStats(s *Stats) {
	readStats(s)
}
