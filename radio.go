//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums

#include "espradio.h"
*/
import "C"
import (
	"bytes"
	"errors"
	"runtime"
	"runtime/interrupt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// ─── Types ───────────────────────────────────────────────────────────────────

type LogLevel uint8

const (
	LogLevelNone    = C.WIFI_LOG_NONE
	LogLevelError   = C.WIFI_LOG_ERROR
	LogLevelWarning = C.WIFI_LOG_WARNING
	LogLevelInfo    = C.WIFI_LOG_INFO
	LogLevelDebug   = C.WIFI_LOG_DEBUG
	LogLevelVerbose = C.WIFI_LOG_VERBOSE
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

// ─── Enable ──────────────────────────────────────────────────────────────────

const schedTickerMs = 5

var isrKick chan struct{}

func startSchedTicker() {
	isrKick = make(chan struct{}, 1)
	schedPassesStart = time.Now()
	go func() {
		ticker := time.NewTicker(schedTickerMs * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-isrKick:
			}
			schedOnce()
			runtime.Gosched()
		}
	}()
}

var wifiInitDone uint32

// ─── Scheduler policy (runtime-switchable, for A/B on hardware) ──────────────

// SchedPolicy selects behaviour inside schedOnce that is worth measuring on
// real hardware rather than reasoning about.  Modelled on the BLE side's
// espradio_bt_set_sched_tick_mask, which is what made the scheduler-contention
// theory in the BLE work falsifiable.
type SchedPolicy uint32

const (
	// SchedPollWiFiISR polls the blob's WiFi ISR from the scheduler tick.
	//
	// On ESP32-S3 and ESP32 this is required: their hardware handler only
	// masks and kicks, so the tick is the only thing that runs the blob ISR.
	// On ESP32-C3 the real interrupt handler already runs it, so the poll
	// makes it run twice per event.  BLE refuses to poll its ISR at all
	// because doing so without a hardware event corrupts the link-layer state
	// (see the note in schedOnce), and whether the WiFi blob tolerates it is a
	// property of that blob, not a general rule -- so it is switchable and
	// meant to be measured, not removed on argument.
	SchedPollWiFiISR SchedPolicy = 1 << iota
)

// schedPolicy defaults to polling the WiFi ISR with fixed-count drains, which is
// the behaviour that predates this switch.
var schedPolicy = uint32(SchedPollWiFiISR)

// SetSchedPolicy replaces the scheduler policy mask.  Intended for bisecting
// behaviour on hardware; the default is the historical behaviour.
func SetSchedPolicy(p SchedPolicy) { atomic.StoreUint32(&schedPolicy, uint32(p)) }

// GetSchedPolicy returns the current scheduler policy mask.
func GetSchedPolicy() SchedPolicy { return SchedPolicy(atomic.LoadUint32(&schedPolicy)) }

func schedPolicyHas(p SchedPolicy) bool {
	return atomic.LoadUint32(&schedPolicy)&uint32(p) != 0
}

// ─── Scheduler counters ──────────────────────────────────────────────────────

// Drain caps.  Measured: the cap-hit counters below stay at zero, so four passes
// per source is always enough and no work is being left behind.  A
// drain-until-idle mode was tried and removed -- there was nothing for it to
// drain, and a longer pass costs interrupt-masked time.  The counters stay as
// regression detectors.
const (
	drainCap          = 4
	timerFirePerRound = 8
)

// Bounds for the two bring-up pumps.  These are ceilings on a wait for a real
// predicate, not settle times: the pumps exit as soon as the blob has set the ROM
// pointers.  They stay bounded because at least one pointer is measurably never
// set at this stage on the C3 and S3, so an unbounded wait would hang there.
const (
	postStartMaxPasses     = 40
	connectSettleMaxPasses = 20
)

// readyNeverSentinel distinguishes "the pump ran and the predicate never became
// true" from "the pump never ran at all", which a plain zero conflates -- neither
// scan nor apwebserver calls Connect, so its counter reads zero for the second
// reason while postWiFiStart's reads zero for the first.
const readyNeverSentinel = 0xffff

var (
	// schedReentries counts schedOnce calls that found another pass already in
	// progress and returned without doing anything.
	//
	// Measured on hardware: this stays at zero.  The ticker goroutine cannot
	// re-enter itself, because a yield from blob code inside a pass resumes that
	// goroutine mid-pass rather than at the top of its loop.  The only overlap
	// available is the main goroutine's pumpSched during bring-up racing the
	// ticker, and that window is evidently never hit.
	//
	// The guard is kept as a safety net rather than as a fix for an observed
	// problem: if the two ever did overlap, the inner pass's
	// espradio_wifi_unmask() would re-enable the WiFi interrupt while the outer
	// pass was still iterating the blob's ISR handlers.  Cost is two atomics per
	// pass.
	schedReentries  uint32
	schedInProgress uint32

	// Drain loops that used their whole budget, i.e. left work behind.
	schedEventCapHits    uint32
	schedTimerCapHits    uint32
	schedESPTimerCapHits uint32

	// ISR-ring items discarded because the destination blob queue was full.
	// The drain advances the ring tail regardless of the send result, so
	// without this the loss is invisible.
	schedISRRingSendFail uint32

	// safeGosched calls that returned without yielding because interrupts were
	// off.  Every spin loop that calls safeGosched turns into a busy spin when
	// this happens, so a non-zero value here explains a hang.
	yieldSuppressed uint32

	// Which scheduler pass each bring-up pump needed before the blob had set the
	// ROM pointers, or readyNeverSentinel if it never did.
	startReadyAfterPass   uint32
	connectReadyAfterPass uint32

	// schedPasses counts completed schedOnce passes.  The tick is nominally
	// 5 ms / 200 Hz, but every espradio_task_yield_go calls kickSched, so the
	// ticker is woken far more often than that -- the ISR counter's growth implies
	// tens of thousands of passes per second.  This measures it directly instead
	// of inferring it: DebugStats divides by elapsed time.
	//
	// The same feedback loop was found and throttled on the BLE side, where waking
	// the controller task fed back into kickSched and collapsed the 5 ms tick to
	// ~30 us.  Nothing throttles the WiFi side.
	schedPasses uint32
)

// schedPassesStart is when pass counting began, for the rate calculation.
var schedPassesStart time.Time

// schedOnce runs one scheduler pass.  It reports false if another pass was
// already in flight and this call did nothing, so a caller pumping in a loop can
// yield to the holder instead of spinning through no-ops.
func schedOnce() bool {
	// Bail out rather than nesting a second pass.
	//
	// The alternative -- counting nesting and unmasking only at zero -- would
	// still run the blob's ISR handlers and queue drains re-entrantly, which is
	// what corrupts blob state.  Skipping is safe: the outer pass is already
	// doing this work.
	if !atomic.CompareAndSwapUint32(&schedInProgress, 0, 1) {
		atomic.AddUint32(&schedReentries, 1)
		return false
	}

	// Snapshot INTENABLE before any blob code runs so that wifi_unmask can
	// restore TinyGo-owned bits (e.g. GPIO at bit 10 on ESP32-S3) that the
	// blob may clear via ROM calls (ets_isr_mask) bypassing the OS adapter.
	C.espradio_snapshot_intenable()

	// Mask WiFi CPU interrupt before the ISR softcall.  On Xtensa (ESP32-S3)
	// the WiFi interrupt is level-triggered at level 1.  If the MAC asserts
	// its interrupt while we're already iterating the ISR handlers below,
	// the hardware ISR preempts us and re-entrantly calls the blob's ISR
	// handler, corrupting its state and crashing.  Masking first prevents
	// this; espradio_wifi_unmask() at the end re-enables the interrupt.
	C.espradio_ints_off(C.uint32_t(1 << wifiCPUInterrupt))

	// Restore ROM pointers BEFORE any blob code runs.  The blob reads
	// pTxRx, pp_wdev_funcs etc. during ISR/queue/timer processing below.
	C.espradio_restore_rom_ptrs()

	// Poll WiFi ISR: work around missing hardware interrupt on ESP32-S3.
	// Only poll after init is complete (blob ISR not registered until then).
	if atomic.LoadUint32(&wifiInitDone) != 0 && schedPolicyHas(SchedPollWiFiISR) {
		C.espradio_call_wifi_isr()
	}

	// NOTE: Do NOT poll BLE ISR here. Calling the blob's RWBLE ISR without
	// a real hardware event corrupts the link-layer state machine (resets
	// pwr_state, disrupts scan timing). BLE uses level-triggered interrupts
	// that re-fire automatically when the peripheral asserts.

	// Call r_rwip_schedule to drive the BLE link-layer scheduler.
	// This programs COMPVAL for the next scan window timer event.
	// Unlike calling the ISR, this is safe from non-ISR context.
	C.espradio_bt_sched_tick()

	for C.espradio_isr_ring_tail() != C.espradio_isr_ring_head() {
		idx := C.espradio_isr_ring_tail()
		q := C.espradio_isr_ring_entry_queue(idx)
		itemPtr := C.espradio_isr_ring_entry_item(idx)
		// The tail advances either way, as it always has: a full destination
		// queue means the item is gone.  Count it so the loss is visible.
		if C.espradio_queue_send(q, itemPtr, 0) == 0 {
			atomic.AddUint32(&schedISRRingSendFail, 1)
		}
		C.espradio_isr_ring_advance_tail()
	}

	// Drain the three work sources.  Each loop exits early when its source
	// reports nothing left; using the whole budget means work was left behind,
	// which is what the cap-hit counters record.
	//
	// espradio_event_loop_run_once returns 1 when it dispatched an event.
	// Without that return value this loop could not tell "drained" from "out of
	// passes", and it previously ran the full count unconditionally.
	n := 0
	for ; n < drainCap; n++ {
		if C.espradio_event_loop_run_once() == 0 {
			break
		}
	}
	if n == drainCap {
		atomic.AddUint32(&schedEventCapHits, 1)
	}

	n = 0
	for ; n < drainCap; n++ {
		if C.espradio_timer_poll_due(timerFirePerRound) == 0 {
			break
		}
	}
	if n == drainCap {
		atomic.AddUint32(&schedTimerCapHits, 1)
	}

	n = 0
	for ; n < drainCap; n++ {
		if C.espradio_esp_timer_poll_due(timerFirePerRound) == 0 {
			break
		}
	}
	if n == drainCap {
		atomic.AddUint32(&schedESPTimerCapHits, 1)
	}

	// Restore critical ROM pointer variables that WiFi DMA may have
	// corrupted (pTxRx, our_tx_eb, our_wait_eb, lmacConfMib_ptr).
	C.espradio_restore_rom_ptrs()

	C.espradio_wifi_unmask()

	atomic.AddUint32(&schedPasses, 1)
	atomic.StoreUint32(&schedInProgress, 0)
	return true
}

// pumpSched runs one scheduler pass and yields.  If the pass was skipped because
// the ticker goroutine held it, the yield is what lets that pass finish -- a bare
// loop over schedOnce would otherwise burn its whole budget on no-ops.
func pumpSched() {
	if !schedOnce() {
		runtime.Gosched()
	}
}

// espradio_pump_sched_once lets C run one scheduler pass.  Used by the TX retry
// path in netif.c, which has to let the blob run in order to get a TX buffer back.
//
//export espradio_pump_sched_once
func espradio_pump_sched_once() {
	pumpSched()
}

// kickSched wakes the scheduler goroutine immediately.
//
// Use this only for a real hardware event, which must be serviced now.  Every
// other caller should use kickSchedThrottled: the ticker is woken by the kick
// channel, so an unthrottled kick on a path the blob takes thousands of times a
// second replaces the 5 ms tick entirely.
func kickSched() {
	select {
	case isrKick <- struct{}{}:
	default:
	}
}

// Kick throttling.
//
// The scheduler ticker selects on a 5 ms timer and the kick channel, so a kick is
// what actually determines how often it runs.  espradio_task_yield_go kicks on
// every blob yield, and the blob yields on every queue-empty, semaphore-unavailable
// and lock-contended path -- measured, that drove 27,599 scheduler passes per
// second against a nominal 200.  Each of those masks the WiFi interrupt, restores
// the ROM pointers twice, runs the blob ISR and three drain loops.
//
// The BLE side hit the same feedback loop from the other direction -- waking the
// controller task made its goroutine runnable, which kicked, which collapsed the
// tick to ~30 us -- and fixed it with a 1 kHz cap in bt_wake_task_throttled.  This
// is the WiFi equivalent, and it keeps the same split: a real hardware event kicks
// unthrottled, because it must be serviced now; the cooperative-yield path is
// rate-limited, because it is a hint that work may be available, not a deadline.
//
// The throttle bounds added latency to kickThrottleUs, which is well inside the
// 5 ms the tick alone would impose.
//
// Scope, measured: this fixes the ESP32-C3, which went from 27,599 passes/second
// to ~1,200.  It barely moves the ESP32-S3 (45,154) or the ESP32 (27,183), because
// on those targets the passes are not yield-driven at all -- their handler masks
// the interrupt and kicks, schedOnce runs the blob ISR and unmasks, and if the
// source is still asserting it re-fires at once.  Their pass count tracks the ISR
// count almost exactly.  That is a second, separate loop, and it is not addressed
// here: the kick that drives it is a real hardware event, so throttling it would
// mean dropping interrupts rather than coalescing hints.  The C3 escapes it
// because its handler runs the blob ISR inline and so clears the source.
const defaultKickThrottleUs = 1000

var (
	// kickThrottleUs is the minimum interval between yield-driven kicks.  Zero
	// disables throttling, restoring the pre-Stage-3 behaviour for A/B.
	kickThrottleUs = uint32(defaultKickThrottleUs)

	// lastKickUs is only touched from the throttled path, which never runs in
	// hardware interrupt context (that path kicks unthrottled), so a plain 64-bit
	// read-modify-write here cannot be interleaved by an ISR on this core.  A lost
	// update would only cost one extra or one missed kick anyway.
	lastKickUs uint64

	kicksSuppressed uint32
	kicksDelivered  uint32

	// hwWiFiISRCount counts entries to the Go WiFi interrupt handler, i.e. real
	// hardware interrupts.
	//
	// This is deliberately separate from espradio_get_wifi_isr_count(), which
	// counts espradio_call_wifi_isr() and therefore tracks scheduler passes on
	// ESP32-S3 and ESP32 -- those targets invoke the blob ISR from the tick, not
	// from the trap handler.  Conflating the two is what made the Xtensa pass rate
	// unattributable: the handler's own kickSched was uncounted, so 45k passes per
	// second on an idle radio had no visible cause.
	hwWiFiISRCount uint32
)

// countHWWiFiISR is called from each target's WiFi interrupt handler.
func countHWWiFiISR() { atomic.AddUint32(&hwWiFiISRCount, 1) }

// SetKickThrottleUs sets the minimum interval in microseconds between
// yield-driven scheduler wakes.  Zero disables the throttle.
//
// Intended for bisecting on hardware: 0 is the historical behaviour, 1000 the
// default, and larger values trade latency for fewer scheduler passes.
func SetKickThrottleUs(us uint32) { atomic.StoreUint32(&kickThrottleUs, us) }

// KickThrottleUs returns the current yield-driven kick interval.
func KickThrottleUs() uint32 { return atomic.LoadUint32(&kickThrottleUs) }

// Note on bring-up and the throttle: disabling the throttle for the duration of
// the bring-up pumps was tried, on the theory that rate-limiting the blob's wakes
// was what left our_tx_eb and our_wait_eb unlatched within the pump's bound.  It
// was refuted -- savedunready and the missing-pointer mask came back bit-identical
// with the throttle off during the pump -- so that code is not here.  Whatever
// determines when those two pointers appear, it is not the kick rate.

// kickSchedThrottled wakes the scheduler unless it was woken too recently.
//
// In hardware interrupt context the throttle is bypassed: that is a real event,
// and it is also the one context where the timestamp must not be written.
func kickSchedThrottled() {
	throttle := atomic.LoadUint32(&kickThrottleUs)
	if throttle == 0 || C.espradio_in_hw_isr() {
		atomic.AddUint32(&kicksDelivered, 1)
		kickSched()
		return
	}
	now := timeUsNow()
	if now-lastKickUs < uint64(throttle) {
		atomic.AddUint32(&kicksSuppressed, 1)
		return
	}
	lastKickUs = now
	atomic.AddUint32(&kicksDelivered, 1)
	kickSched()
}

// arenaPool keeps the arena backing memory reachable from Go so the GC
// won't collect it.  The WiFi blob stores pointers into this pool in ROM
// BSS (outside the GC's scan range), so individual malloc'd objects would
// be collected.  One large pool kept alive by this global is safe.
var arenaPool []byte

// ArenaStats returns the current arena usage and capacity in bytes.
func ArenaStats() (used, capacity uint32) {
	var u, c C.uint32_t
	C.espradio_arena_stats(&u, &c)
	return uint32(u), uint32(c)
}

// wifiEnabled guards Enable against a second call.  Re-running it would
// re-initialise the arena under the blob's live pointers, start a second ticker
// goroutine and re-register the ISR.
var wifiEnabled uint32

// ErrAlreadyEnabled is returned by Enable when the radio is already enabled.
var ErrAlreadyEnabled = errors.New("espradio: radio already enabled")

// Enable and configure the radio for WiFi.
//
// Enable is not idempotent and returns ErrAlreadyEnabled on a second call.
func Enable(config Config) error {
	if !atomic.CompareAndSwapUint32(&wifiEnabled, 0, 1) {
		return ErrAlreadyEnabled
	}

	// Before anything can register a handler: on ESP32 these tables sit in a
	// custom section the runtime does not zero.
	C.espradio_isr_tables_init()

	// Allocate the arena pool from the Go heap and hand it to C -- but only if
	// nobody has already done so.  espradio_arena_init re-lays the whole pool as
	// one free block, so calling it when BLEInit got there first would reset the
	// heap underneath the live BT controller.  BLEInit already guards this the
	// same way.
	if arenaPool == nil {
		poolSize := arenaPoolSize
		if config.ArenaPoolSize > 0 {
			poolSize = config.ArenaPoolSize
		}
		arenaPool = makeArenaPool(poolSize)
		C.espradio_arena_init((*C.uint8_t)(unsafe.Pointer(&arenaPool[0])), C.size_t(len(arenaPool)))
	}

	if isrKick == nil {
		startSchedTicker()
	}
	time.Sleep(schedTickerMs * time.Millisecond)
	initHardware()
	C.espradio_ensure_osi_ptr()

	wifiISR = interrupt.New(wifiCPUInterrupt, wifiISRHandler)
	wifiISR.Enable()
	C.espradio_wifi_int_raise_priority()

	C.espradio_prewire_wifi_interrupts()

	C.espradio_event_register_default_cb()
	C.espradio_set_blob_log_level(C.uint32_t(config.Logging))

	mask := interrupt.Disable()
	C.espradio_hal_init_clocks_go()
	interrupt.Restore(mask)

	errCode := C.espradio_wifi_init()
	if errCode != 0 {
		return makeError(errCode)
	}
	C.espradio_wifi_init_completed()
	C.espradio_wifi_int_to_level()
	atomic.StoreUint32(&wifiInitDone, 1)
	schedOnce()
	C.espradio_netif_init_netstack_cb()

	// set default transmit level of 20 dBm (100 mW) for ESP32.
	C.esp_wifi_set_max_tx_power(C.int8_t(20))

	return nil
}

// Start starts the Wi-Fi driver and connects to the AP if in station mode.
// Blocks until the driver is ready.  Start is separate from Enable to allow
// configuration (e.g. country code) before starting, and to allow scanning without
// starting the driver.  Start calls schedOnce in a loop to let the blob process
// its internal startup sequence (posting events, etc.) before Start returns.
func Start() error {
	var mode C.wifi_mode_t
	if code := C.esp_wifi_get_mode(&mode); code != C.ESP_OK {
		return makeError(code)
	}
	if mode != C.WIFI_MODE_STA {
		if code := C.esp_wifi_set_mode(C.WIFI_MODE_STA); code != C.ESP_OK {
			return makeError(code)
		}
	}

	C.espradio_set_country_eu_manual()

	if code := C.espradio_esp_wifi_start(); code != C.ESP_OK {
		return makeError(code)
	}

	// Disable modem-sleep power management.  The blob's default
	// WIFI_PS_MIN_MODEM fires PM timer callbacks (pm_dream, pm_go_to_wake,
	// etc.) that call ppCheckTxConnTrafficIdle.  Under cooperative scheduling
	// the TX frame queues may be in an inconsistent state when those PM
	// callbacks run, causing NULL-pointer crashes in ppCheckIsConnTraffic.
	//
	// Pump the scheduler so ppTask processes the START command (pp_attach,
	// ppInitTxq, etc.) and the critical ROM pointer variables (pTxRx,
	// our_tx_eb, …) get initialised, then snapshot them.
	postWiFiStart()

	return nil
}

// postWiFiStart disables modem-sleep and pumps the scheduler so ppTask
// processes the START command and ROM pointer variables get initialised.
func postWiFiStart() {
	C.esp_wifi_set_ps(C.WIFI_PS_NONE)

	// Pump until the blob has set the ROM pointers, rather than for a fixed
	// count and hoping.  Bounded, because on the C3 and S3 at least one pointer
	// is measurably never set at this stage -- so this must not become a wait
	// that cannot finish.
	//
	// espradio_save_rom_ptrs latches each pointer independently the first time it
	// is non-NULL, so calling it every pass is both safe and the point: whatever
	// is ready gets captured now, and anything late gets captured by the next
	// call rather than being pinned to NULL.
	for i := 0; i < postStartMaxPasses; i++ {
		pumpSched()
		runtime.Gosched()
		C.espradio_save_rom_ptrs()
		if C.espradio_rom_ptrs_ready() != 0 {
			startReadyAfterPass = uint32(i + 1)
			break
		}
	}
	if startReadyAfterPass == 0 {
		startReadyAfterPass = readyNeverSentinel
	}
	C.espradio_save_rom_ptrs()
}

// DebugISRCount returns the number of WiFi ISR invocations (for debugging).
func DebugISRCount() uint32 {
	return uint32(C.espradio_get_wifi_isr_count())
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
	// RxIngressErrors counts frames the upper stack declined.  Expected to be
	// non-zero in normal operation: unhandled protocols land here.
	RxIngressErrors uint32

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
		"oversize", s.RxOversize, "ingresserr", s.RxIngressErrors)
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

// DebugStats returns a snapshot of the driver's internal counters.
func DebugStats() Stats {
	var s Stats

	s.WiFiISRCount = uint32(C.espradio_get_wifi_isr_count())
	s.SchedReentries = atomic.LoadUint32(&schedReentries)
	s.EventCapHits = atomic.LoadUint32(&schedEventCapHits)
	s.TimerCapHits = atomic.LoadUint32(&schedTimerCapHits)
	s.ESPTimerCapHits = atomic.LoadUint32(&schedESPTimerCapHits)
	s.YieldsSuppressed = atomic.LoadUint32(&yieldSuppressed)
	s.IntsOffNesting = atomic.LoadUint32(&wifiIntsOff)
	s.SchedPasses = atomic.LoadUint32(&schedPasses)
	if !schedPassesStart.IsZero() {
		if elapsed := time.Since(schedPassesStart).Seconds(); elapsed >= 1 {
			s.SchedPassesPerSec = uint32(float64(s.SchedPasses) / elapsed)
		}
	}
	s.KicksDelivered = atomic.LoadUint32(&kicksDelivered)
	s.KicksSuppressed = atomic.LoadUint32(&kicksSuppressed)
	s.KickThrottleUs = atomic.LoadUint32(&kickThrottleUs)
	s.HWISRCount = atomic.LoadUint32(&hwWiFiISRCount)
	if !schedPassesStart.IsZero() {
		if elapsed := time.Since(schedPassesStart).Seconds(); elapsed >= 1 {
			s.HWISRPerSec = uint32(float64(s.HWISRCount) / elapsed)
		}
	}
	s.WiFiISRSlots = uint32(C.espradio_wifi_isr_slots())
	s.WiFiISRHandlerCalls = uint32(C.espradio_wifi_isr_handler_calls())
	s.WiFiISRInstalled = uint32(C.espradio_wifi_isr_installed())
	s.UnmaskIntervalUs = uint32(C.espradio_unmask_interval_us())
	s.UnmaskSuppressed = uint32(C.espradio_unmask_suppressed())
	s.StartReadyAfterPass = startReadyAfterPass
	s.ConnectReadyAfterPass = connectReadyAfterPass
	s.RomPtrsSavedUnready = uint32(C.espradio_rom_ptrs_saved_unready())
	s.RomPtrsMissing = uint32(C.espradio_rom_ptrs_missing())

	s.ISRRingDrops = uint32(C.espradio_isr_ring_drops())
	s.ISRRingSendFail = atomic.LoadUint32(&schedISRRingSendFail)
	s.QueueSendFull = uint32(C.espradio_queue_send_full_count())

	s.RxCallbacks = uint32(C.espradio_netif_rx_cb_count())
	s.RxDrops = uint32(C.espradio_netif_rx_cb_drop())
	s.RxOversize = uint32(C.espradio_netif_rx_oversize())
	s.RxIngressErrors = atomic.LoadUint32(&rxIngressErrors)

	var attempts, failNoMem, failOther, notConnected, txDone, retries, busyWaits C.uint32_t
	C.espradio_netif_tx_stats(&attempts, &failNoMem, &failOther, &notConnected,
		&txDone, &retries, &busyWaits)
	s.TxAttempts = uint32(attempts)
	s.TxFailNoMem = uint32(failNoMem)
	s.TxFailOther = uint32(failOther)
	s.TxNotConnected = uint32(notConnected)
	s.TxDoneCB = uint32(txDone)
	s.TxRetries = uint32(retries)
	s.TxBusyWaits = uint32(busyWaits)

	var allocs, frees C.uint
	C.espradio_alloc_stats(&allocs, &frees)
	s.AllocCount = uint32(allocs)
	s.FreeCount = uint32(frees)
	s.ArenaUsed, s.ArenaCapacity = ArenaStats()

	return s
}

// Scan tuning.  All three were unnamed literals; none has a recorded derivation.
// The per-channel dwell times are the blob's own units (milliseconds) and bound
// how long a scan takes: 13 channels at up to scanActiveMaxMs each.
const (
	scanSettleTime  = 250 * time.Millisecond
	scanActiveMaxMs = 300
	scanPassiveMs   = 500
)

// Scan performs a single Wi-Fi scan pass and returns the list of discovered access points.
func Scan() ([]AccessPoint, error) {
	C.espradio_ensure_osi_ptr()
	C.esp_wifi_set_ps(C.WIFI_PS_NONE)
	C.espradio_set_country_eu_manual()

	// Settle after the three config calls above before starting a scan.
	//
	// Deliberately still a delay, not a pump-until-predicate like the bring-up
	// waits.  Those were open-loop because the blob needed CPU that a fixed count
	// of passes did not reliably provide; that reasoning does not apply here.  The
	// ticker goroutine runs schedOnce continuously in the background -- measured at
	// tens of thousands of passes per second -- so the blob has ample CPU during
	// this sleep and is not waiting on us to drive it.  Whatever this interval is
	// for is wall-clock settling (PHY/RF after the country and power-save changes),
	// which a pump cannot shorten.
	//
	// Its true purpose and correct size are still unverified: it arrived without a
	// comment. Reducing or removing it needs an A/B against scan on all three
	// targets, watching for empty or short AP lists.
	time.Sleep(scanSettleTime)

	var scanCfg C.wifi_scan_config_t
	scanCfg.ssid = nil
	scanCfg.bssid = nil
	scanCfg.channel = 0
	scanCfg.show_hidden = false
	scanCfg.scan_type = C.WIFI_SCAN_TYPE_ACTIVE
	scanCfg.scan_time.active.min = 0
	scanCfg.scan_time.active.max = scanActiveMaxMs
	scanCfg.scan_time.passive = scanPassiveMs
	if code := C.esp_wifi_scan_start(&scanCfg, true); code != C.ESP_OK {
		return nil, makeError(code)
	}

	var num C.uint16_t
	if code := C.esp_wifi_scan_get_ap_num(&num); code != C.ESP_OK {
		return nil, makeError(code)
	}
	if num == 0 {
		return nil, nil
	}

	recs := make([]C.wifi_ap_record_t, int(num))
	if code := C.esp_wifi_scan_get_ap_records(
		&num,
		(*C.wifi_ap_record_t)(unsafe.Pointer(&recs[0])),
	); code != C.ESP_OK {
		return nil, makeError(code)
	}

	aps := make([]AccessPoint, int(num))
	for i := 0; i < int(num); i++ {
		raw := C.GoBytes(unsafe.Pointer(&recs[i].ssid[0]), C.int(len(recs[i].ssid)))
		if idx := bytes.IndexByte(raw, 0); idx >= 0 {
			raw = raw[:idx]
		}
		aps[i] = AccessPoint{
			SSID: string(raw),
			RSSI: int(recs[i].rssi),
		}
	}

	return aps, nil
}

// ─── Connect ─────────────────────────────────────────────────────────────────

var (
	connectMu     sync.Mutex
	connectResult chan ConnectResult
)

// Connect configures STA credentials and initiates association.
// Blocks until CONNECTED, DISCONNECTED or timeout.
func Connect(cfg STAConfig) error {
	connectMu.Lock()
	connectResult = make(chan ConnectResult, 1)
	connectMu.Unlock()

	code := C.espradio_sta_set_config(
		C.CString(cfg.SSID), C.int(len(cfg.SSID)),
		C.CString(cfg.Password), C.int(len(cfg.Password)),
	)
	if code != C.ESP_OK {
		return makeError(code)
	}

	if code := C.esp_wifi_connect_internal(); code != C.ESP_OK {
		return makeError(code)
	}

	select {
	case res := <-connectResult:
		if res.Connected {
			// The blob fires WIFI_EVENT_STA_CONNECTED before its internal
			// TX path (ppCheckIsConnTraffic) is fully initialized.  Pump the
			// scheduler to let the blob finish setup before callers try to TX.
			for i := 0; i < connectSettleMaxPasses; i++ {
				pumpSched()
				C.espradio_save_rom_ptrs()
				if C.espradio_rom_ptrs_ready() != 0 {
					connectReadyAfterPass = uint32(i + 1)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if connectReadyAfterPass == 0 {
				connectReadyAfterPass = readyNeverSentinel
			}
			return nil
		}
		return makeError(C.esp_err_t(res.Reason))
	case <-time.After(15 * time.Second):
		return makeError(C.ESP_ERR_TIMEOUT)
	}
}

//export espradio_on_wifi_event
func espradio_on_wifi_event(eventID int32, data unsafe.Pointer) {
	switch eventID {
	case C.WIFI_EVENT_STA_CONNECTED:
		C.espradio_netif_set_connected(1)
		ev := (*C.wifi_event_sta_connected_t)(data)
		ssidLen := int(ev.ssid_len)
		if ssidLen > 32 {
			ssidLen = 32
		}
		ssid := C.GoBytes(unsafe.Pointer(&ev.ssid[0]), C.int(ssidLen))
		connectMu.Lock()
		ch := connectResult
		connectMu.Unlock()
		if ch != nil {
			select {
			case ch <- ConnectResult{Connected: true, SSID: string(ssid), Channel: uint8(ev.channel)}:
			default:
			}
		}

	case C.WIFI_EVENT_STA_DISCONNECTED:
		C.espradio_netif_set_connected(0)
		ev := (*C.wifi_event_sta_disconnected_t)(data)
		connectMu.Lock()
		ch := connectResult
		connectMu.Unlock()
		if ch != nil {
			select {
			case ch <- ConnectResult{Connected: false, Reason: uint8(ev.reason)}:
			default:
			}
		}

	case C.WIFI_EVENT_STA_START:
	}
}

// ─── Soft-AP ─────────────────────────────────────────────────────────────────

// StartAP starts the radio in soft-AP mode with the given configuration.
func StartAP(cfg APConfig) error {
	if code := C.esp_wifi_set_mode(C.WIFI_MODE_AP); code != C.ESP_OK {
		return makeError(code)
	}

	ssid := cfg.SSID
	if len(ssid) == 0 {
		ssid = "espradio-ap"
	}
	code := C.espradio_ap_set_config(
		C.CString(ssid), C.int(len(ssid)),
		C.CString(cfg.Password), C.int(len(cfg.Password)),
		C.uint8_t(cfg.Channel), C.int(boolToInt(cfg.AuthOpen)),
	)
	if code != C.ESP_OK {
		return makeError(code)
	}

	if code := C.espradio_esp_wifi_start(); code != C.ESP_OK {
		return makeError(code)
	}

	// Same post-start sequence as Start().
	postWiFiStart()

	return nil
}

// ─── RF diagnostics ─────────────────────────────────────────────────────────

func SniffCountOnChannel(channel uint8, duration time.Duration) (uint32, error) {
	if duration <= 0 {
		duration = 1500 * time.Millisecond
	}
	if code := C.espradio_sniff_begin(C.uint8_t(channel)); code != C.ESP_OK {
		return 0, makeError(code)
	}
	time.Sleep(duration)
	packets := uint32(C.espradio_sniff_count())
	if code := C.espradio_sniff_end(); code != C.ESP_OK {
		return packets, makeError(code)
	}
	return packets, nil
}

// ─── Tasks / timers / ISR ────────────────────────────────────────────────────

func millisecondsToTicks(ms uint32) uint32 {
	return ms * (ticksPerSecond / 1000)
}

//export espradio_panic
func espradio_panic(msg *C.char) {
	panic("espradio: " + C.GoString(msg))
}

//export espradio_log_timestamp
func espradio_log_timestamp() uint32 {
	return uint32(time.Now().UnixMilli())
}

//export espradio_run_task
func espradio_run_task(task_func, param unsafe.Pointer)

//export espradio_task_create_pinned_to_core
func espradio_task_create_pinned_to_core(task_func unsafe.Pointer, name *C.char, stack_depth uint32, param unsafe.Pointer, prio uint32, task_handle *unsafe.Pointer, core_id uint32) int32 {
	var ready uint32
	go func() {
		*task_handle = tinygo_task_current()
		atomic.StoreUint32(&ready, 1)
		espradio_run_task(task_func, param)
	}()
	for atomic.LoadUint32(&ready) == 0 {
		runtime.Gosched()
	}
	return 1
}

//export espradio_task_delete
func espradio_task_delete(task_handle unsafe.Pointer) {
	// The goroutine itself cannot be killed from here; it exits when
	// espradio_run_task returns.  What can be reclaimed is the per-task
	// semaphore slot and its map entry, which otherwise leak on every task
	// teardown -- the slot pool is small and shared with the BT controller, and
	// the map grew without bound.
	releaseThreadSem(task_handle)
}

//export tinygo_task_current
func tinygo_task_current() unsafe.Pointer

//export espradio_task_get_current_task
func espradio_task_get_current_task() unsafe.Pointer {
	return tinygo_task_current()
}

// safeGosched yields unless interrupts are currently disabled.  It reports
// whether it actually yielded.
//
// A false return means no other goroutine can have run, so a caller spinning on
// this cannot make progress: whatever it is waiting for is held by something that
// will never be scheduled.  Callers must treat that as failure rather than
// looping, which is what turned these waits into hangs.
//
// The blob is not supposed to reach a blocking wait from inside a critical
// section at all -- in ESP-IDF these regions are portENTER_CRITICAL and never
// yield -- so a non-zero yieldSuppressed count is evidence of a real defect, not
// a state to tolerate.
//
// Measured on hardware: yieldSuppressed stays at zero, so the false return and
// the failure paths that depend on it are unreached.  They are kept because they
// convert a documented-impossible state from a hang into an observable failure,
// not because the state was seen.
func safeGosched() bool {
	if wifiIntsOff > 0 {
		atomic.AddUint32(&yieldSuppressed, 1)
		return false
	}
	runtime.Gosched()
	return true
}

// mutexLockTimeoutUs bounds espradio_mutex_lock, which previously had no timeout
// of any kind.  Generous: it should never be reached in normal operation, and
// hitting it means genuine cross-goroutine contention that is not resolving.
const mutexLockTimeoutUs = 250_000

//export espradio_task_yield_go
func espradio_task_yield_go() {
	// Don't fire timers inline here — the blob calls task_yield from
	// deep call stacks and the extra depth risks overflowing the
	// goroutine stack.  Timer polling is handled by schedOnce() in
	// the ticker goroutine on its own stack.
	//
	// Throttled: the blob yields on every contended lock, empty queue and
	// unavailable semaphore, so kicking on each one is what drove the scheduler to
	// 138x its nominal rate.  A yield says work may be available, not that it is
	// due now, and the Gosched below still hands over to the ticker if it is
	// already runnable.
	kickSchedThrottled()
	runtime.Gosched()
}

//export espradio_time_us_now
func espradio_time_us_now() uint64 {
	return uint64(time.Now().UnixMicro())
}

// timeUsNow returns monotonic microseconds without allocating a time.Time.
func timeUsNow() uint64 {
	return uint64(time.Now().UnixMicro())
}

//export espradio_task_delay
func espradio_task_delay(ticks uint32) {
	const ticksPerMillisecond = ticksPerSecond / 1000
	ms := (ticks + ticksPerMillisecond - 1) / ticksPerMillisecond
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

//export espradio_task_ms_to_tick
func espradio_task_ms_to_tick(ms uint32) int32 {
	return int32(millisecondsToTicks(ms))
}

// The blob uses these as a real critical section, not an advisory hint -- in
// ESP-IDF they map to portENTER_CRITICAL/portEXIT_CRITICAL.  The BLE side learned
// this the hard way: with no-ops here the controller's ke message queues, which
// its ISR mutates concurrently, got corrupted and event delivery silently
// stopped.
//
// Nesting is tracked inside the primitive, following bt_interrupt_disable: the
// previous interrupt state is captured only at depth 0, and only the outermost
// restore re-enables.  That makes the depth trustworthy -- DebugStats reports it,
// and a non-zero value while the radio is idle means a critical section leaked --
// and makes the pair robust against the blob handing back a stale saved state.
var (
	wifiIntsOff   uint32 // nesting depth
	wifiIntsSaved uint32 // interrupt state captured at depth 0
)

//export espradio_wifi_int_disable
func espradio_wifi_int_disable(wifi_int_mux unsafe.Pointer) uint32 {
	s := uint32(interrupt.Disable())
	// Interrupts are off from here, so this read-modify-write cannot be
	// interleaved by the ISR path on this core.  The previous plain increment
	// ran before that was guaranteed.
	if wifiIntsOff == 0 {
		wifiIntsSaved = s
	}
	wifiIntsOff++
	return s
}

//export espradio_wifi_int_restore
func espradio_wifi_int_restore(wifi_int_mux unsafe.Pointer, tmp uint32) {
	// Ignore an unbalanced restore rather than re-enabling: doing so would drop
	// an outer critical section's protection while it is still inside.
	if wifiIntsOff == 0 {
		return
	}
	wifiIntsOff--
	if wifiIntsOff == 0 {
		// Restore the state captured at depth 0, not tmp.  An inner disable
		// captured "already disabled", so honouring per-call state depends on
		// the blob unwinding in strict LIFO order.
		interrupt.Restore(interrupt.State(wifiIntsSaved))
	}
	_ = tmp
}

var wifiISR interrupt.Interrupt

// ─── OSI sync primitives ───────────────────────────────────────────────────────

var fakeSpinLock uint8

//export espradio_spin_lock_create
func espradio_spin_lock_create() unsafe.Pointer {
	return unsafe.Pointer(&fakeSpinLock)
}

//export espradio_spin_lock_delete
func espradio_spin_lock_delete(lock unsafe.Pointer) {
}

type recursiveMutex struct {
	state sync.Mutex
	owner unsafe.Pointer
	count uint32
}

var mutexes [8]recursiveMutex
var mutexInUse [8]uint32

//export espradio_recursive_mutex_create
func espradio_recursive_mutex_create() unsafe.Pointer {
	for i := range mutexes {
		if atomic.CompareAndSwapUint32(&mutexInUse[i], 0, 1) {
			return unsafe.Pointer(&mutexes[i])
		}
	}
	panic("espradio: too many mutexes")
}

//export espradio_mutex_delete
func espradio_mutex_delete(cmut unsafe.Pointer) {
	mut := (*recursiveMutex)(cmut)
	mut.state.Lock()
	mut.owner = nil
	mut.count = 0
	mut.state.Unlock()
	for i := range mutexes {
		if mut == &mutexes[i] {
			atomic.StoreUint32(&mutexInUse[i], 0)
			return
		}
	}
}

//export espradio_mutex_lock
func espradio_mutex_lock(cmut unsafe.Pointer) int32 {
	mut := (*recursiveMutex)(cmut)
	me := tinygo_task_current()
	startUs := timeUsNow()
	for {
		mut.state.Lock()
		if mut.count == 0 || mut.owner == me {
			mut.owner = me
			mut.count++
			mut.state.Unlock()
			return 1
		}
		mut.state.Unlock()
		// Held by another goroutine.  If we cannot yield, that goroutine can
		// never run and no amount of spinning will free the mutex.
		if !safeGosched() {
			return 0
		}
		if timeUsNow()-startUs >= mutexLockTimeoutUs {
			return 0
		}
	}
}

//export espradio_mutex_unlock
func espradio_mutex_unlock(cmut unsafe.Pointer) int32 {
	mut := (*recursiveMutex)(cmut)
	me := tinygo_task_current()
	mut.state.Lock()
	if mut.count > 0 && mut.owner == me {
		mut.count--
		if mut.count == 0 {
			mut.owner = nil
		}
		mut.state.Unlock()
		return 1
	}
	mut.state.Unlock()
	return 0
}

// ─── Event groups ─────────────────────────────────────────────────────────────

type eventGroup struct {
	mu   sync.Mutex
	bits uint32
}

var eventGroups [8]eventGroup
var eventGroupInUse [8]uint32

//export espradio_event_group_create
func espradio_event_group_create() unsafe.Pointer {
	for i := range eventGroups {
		if atomic.CompareAndSwapUint32(&eventGroupInUse[i], 0, 1) {
			eventGroups[i].bits = 0
			return unsafe.Pointer(&eventGroups[i])
		}
	}
	panic("espradio: too many event groups")
}

//export espradio_event_group_delete
func espradio_event_group_delete(ptr unsafe.Pointer) {
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits = 0
	eg.mu.Unlock()
	for i := range eventGroups {
		if eg == &eventGroups[i] {
			atomic.StoreUint32(&eventGroupInUse[i], 0)
			return
		}
	}
}

//export espradio_event_group_set_bits
func espradio_event_group_set_bits(ptr unsafe.Pointer, bits uint32) uint32 {
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits |= bits
	cur := eg.bits
	eg.mu.Unlock()
	return cur
}

//export espradio_event_group_clear_bits
func espradio_event_group_clear_bits(ptr unsafe.Pointer, bits uint32) uint32 {
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits &^= bits
	cur := eg.bits
	eg.mu.Unlock()
	return cur
}

//export espradio_event_group_wait_bits
func espradio_event_group_wait_bits(ptr unsafe.Pointer, bitsToWaitFor uint32, clearOnExit int32, waitForAllBits int32, blockTimeTick uint32) uint32 {
	eg := (*eventGroup)(ptr)
	want := bitsToWaitFor

	forever := blockTimeTick == C.OSI_FUNCS_TIME_BLOCKING
	startUs := timeUsNow()
	var timeoutUs uint64
	if !forever {
		timeoutUs = uint64(blockTimeTick) * 1000
	}

	var snapshot uint32
	for {
		eg.mu.Lock()
		snapshot = eg.bits
		var ok bool
		if waitForAllBits != 0 {
			ok = snapshot&want == want
		} else {
			ok = snapshot&want != 0
		}
		if ok {
			if clearOnExit != 0 {
				eg.bits &^= want
			}
			eg.mu.Unlock()
			return snapshot
		}
		eg.mu.Unlock()
		if blockTimeTick == 0 || (!forever && (timeUsNow()-startUs) >= timeoutUs) {
			return snapshot
		}
		// Only another goroutine can set these bits, so a wait we cannot yield
		// out of will never be satisfied -- including the "forever" case, which
		// would otherwise hang here permanently.
		if !safeGosched() {
			return snapshot
		}
	}
}

// ─── Semaphores ────────────────────────────────────────────────────────────────

type semaphore struct {
	count uint32
}

// Semaphore slots are drawn from by both WiFi (via the OSI table) and the BT
// controller (bt_ble.c calls these directly), so the pool is shared.  Slots are
// reclaimed on delete via a CAS in-use bitmap, the same way mutexes and event
// groups already do it -- the previous monotonic index never freed a slot, so the
// fifth semaphore ever created panicked regardless of how many were live.
var (
	semaphores        [8]semaphore
	semaphoreInUse    [8]uint32
	wifiThreadSemMu   sync.Mutex
	wifiThreadSemByTH = map[unsafe.Pointer]*semaphore{}
	wifiThreadSemNil  semaphore
	threadSems        [8]semaphore
	threadSemInUse    [8]uint32
)

func semTryTake(sem *semaphore) bool {
	for {
		cur := atomic.LoadUint32(&sem.count)
		if cur == 0 {
			return false
		}
		if atomic.CompareAndSwapUint32(&sem.count, cur, cur-1) {
			return true
		}
	}
}

//export espradio_semphr_create
func espradio_semphr_create(max, init uint32) unsafe.Pointer {
	for i := range semaphores {
		if atomic.CompareAndSwapUint32(&semaphoreInUse[i], 0, 1) {
			atomic.StoreUint32(&semaphores[i].count, init)
			return unsafe.Pointer(&semaphores[i])
		}
	}
	panic("espradio: too many semaphores")
}

//export espradio_semphr_take
func espradio_semphr_take(semphr unsafe.Pointer, block_time_tick uint32) int32 {
	sem := (*semaphore)(semphr)
	if block_time_tick == 0 {
		// A zero-timeout take must be genuinely non-blocking: no yield.
		//
		// The blob uses these as mutual-exclusion guards, not as scheduling
		// points. rw_schedule() in particular does a non-blocking take of
		// g_waking_sleeping_sem to guard its critical section. Yielding on the
		// failure path let the other context (the 5ms tick and the BT
		// controller task both reach rw_schedule) re-enter the scheduler, which
		// ran it twice and delivered the same ACL packet to the host twice --
		// breaking GATT, because the duplicate ATT response is consumed as the
		// reply to the next request.
		if semTryTake(sem) {
			return 1
		}
		// Yield on the failure path. The BT controller task needs CPU to service
		// connection events on time; without this the link drops with
		// "Connection Failed to be Established" (0x3e) right after the first ATT
		// request.
		//
		// The ke-event re-entrancy this yield used to allow was, at the time,
		// blocked by a guard on modules_funcs[0x284]. That guard is gone: it was
		// removed once instrumenting it showed its deferral counter stay at 0 for
		// entire runs while GATT still failed, so the two contexts never actually
		// overlapped and the real defect was elsewhere (a lost HCI write, fixed
		// by waiting for the controller to take each packet). For the record,
		// that slot holds r_rwip_schedule, not r_ke_event_schedule as the guard's
		// comment claimed. What keeps the successful path from re-entering the
		// scheduler is the absence of a yield above, nothing else.
		safeGosched()
		return 0
	}

	forever := block_time_tick == C.OSI_FUNCS_TIME_BLOCKING
	startUs := timeUsNow()
	var timeoutUs uint64
	if !forever {
		timeoutUs = uint64(block_time_tick) * 1000
	}

	for {
		if semTryTake(sem) {
			return 1
		}
		if !forever && (timeUsNow()-startUs) >= timeoutUs {
			return 0
		}
		// Only a give from another context can raise the count, so a wait we
		// cannot yield out of will never be satisfied.  This is the one that
		// matters most: the BT controller task takes its semaphore with
		// OSI_FUNCS_TIME_BLOCKING, so without this the forever case parks here.
		if !safeGosched() {
			return 0
		}
	}
}

//export espradio_semphr_give
func espradio_semphr_give(semphr unsafe.Pointer) int32 {
	sem := (*semaphore)(semphr)
	atomic.AddUint32(&sem.count, 1)
	return 1
}

//export espradio_semphr_delete
func espradio_semphr_delete(semphr unsafe.Pointer) {
	sem := (*semaphore)(semphr)
	atomic.StoreUint32(&sem.count, 0)
	// Release the slot.  Only slots from the shared pool are reclaimable; the
	// per-thread semaphores below are owned by their task, and wifiThreadSemNil
	// is a singleton, so neither is in this array.
	for i := range semaphores {
		if sem == &semaphores[i] {
			atomic.StoreUint32(&semaphoreInUse[i], 0)
			return
		}
	}
}

//export espradio_wifi_thread_semphr_get
func espradio_wifi_thread_semphr_get() unsafe.Pointer {
	task := tinygo_task_current()
	wifiThreadSemMu.Lock()
	defer wifiThreadSemMu.Unlock()
	if task == nil {
		return unsafe.Pointer(&wifiThreadSemNil)
	}
	sem := wifiThreadSemByTH[task]
	if sem == nil {
		sem = allocThreadSem()
		if sem == nil {
			panic("espradio: too many thread semaphores")
		}
		wifiThreadSemByTH[task] = sem
	}
	return unsafe.Pointer(sem)
}

// allocThreadSem claims a per-task semaphore slot, or returns nil if all are
// taken.  Slots are reclaimed when the owning task's goroutine is deleted; the
// previous monotonic index leaked one per task teardown.
func allocThreadSem() *semaphore {
	for i := range threadSems {
		if atomic.CompareAndSwapUint32(&threadSemInUse[i], 0, 1) {
			atomic.StoreUint32(&threadSems[i].count, 0)
			return &threadSems[i]
		}
	}
	return nil
}

// releaseThreadSem drops the per-task semaphore for task, if it has one.
func releaseThreadSem(task unsafe.Pointer) {
	if task == nil {
		return
	}
	wifiThreadSemMu.Lock()
	sem := wifiThreadSemByTH[task]
	delete(wifiThreadSemByTH, task)
	wifiThreadSemMu.Unlock()
	if sem == nil {
		return
	}
	for i := range threadSems {
		if sem == &threadSems[i] {
			atomic.StoreUint32(&threadSemInUse[i], 0)
			return
		}
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
