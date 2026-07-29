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

func schedOnce() {
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
	if atomic.LoadUint32(&wifiInitDone) != 0 {
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
		C.espradio_queue_send(q, itemPtr, 0)
		C.espradio_isr_ring_advance_tail()
	}

	for i := 0; i < 4; i++ {
		C.espradio_event_loop_run_once()
	}
	for i := 0; i < 4; i++ {
		if C.espradio_timer_poll_due(8) == 0 {
			break
		}
	}
	for i := 0; i < 4; i++ {
		if C.espradio_esp_timer_poll_due(8) == 0 {
			break
		}
	}

	// Restore critical ROM pointer variables that WiFi DMA may have
	// corrupted (pTxRx, our_tx_eb, our_wait_eb, lmacConfMib_ptr).
	C.espradio_restore_rom_ptrs()

	C.espradio_wifi_unmask()
}

func kickSched() {
	select {
	case isrKick <- struct{}{}:
	default:
	}
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

// Enable and configure the radio for WiFi.
func Enable(config Config) error {
	// Allocate arena pool from Go heap and hand it to C.
	poolSize := arenaPoolSize
	if config.ArenaPoolSize > 0 {
		poolSize = config.ArenaPoolSize
	}
	arenaPool = makeArenaPool(poolSize)
	C.espradio_arena_init((*C.uint8_t)(unsafe.Pointer(&arenaPool[0])), C.size_t(len(arenaPool)))

	startSchedTicker()
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
	for i := 0; i < 40; i++ {
		schedOnce()
		runtime.Gosched()
	}
	C.espradio_save_rom_ptrs()
}

// DebugISRCount returns the number of WiFi ISR invocations (for debugging).
func DebugISRCount() uint32 {
	return uint32(C.espradio_get_wifi_isr_count())
}

// Scan performs a single Wi-Fi scan pass and returns the list of discovered access points.
func Scan() ([]AccessPoint, error) {
	C.espradio_ensure_osi_ptr()
	C.esp_wifi_set_ps(C.WIFI_PS_NONE)
	C.espradio_set_country_eu_manual()

	time.Sleep(250 * time.Millisecond)
	var scanCfg C.wifi_scan_config_t
	scanCfg.ssid = nil
	scanCfg.bssid = nil
	scanCfg.channel = 0
	scanCfg.show_hidden = false
	scanCfg.scan_type = C.WIFI_SCAN_TYPE_ACTIVE
	scanCfg.scan_time.active.min = 0
	scanCfg.scan_time.active.max = 300
	scanCfg.scan_time.passive = 500
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
			for i := 0; i < 20; i++ {
				schedOnce()
				time.Sleep(10 * time.Millisecond)
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
}

//export tinygo_task_current
func tinygo_task_current() unsafe.Pointer

//export espradio_task_get_current_task
func espradio_task_get_current_task() unsafe.Pointer {
	return tinygo_task_current()
}

func safeGosched() {
	if wifiIntsOff > 0 {
		return
	}
	runtime.Gosched()
}

//export espradio_task_yield_go
func espradio_task_yield_go() {
	// Don't fire timers inline here — the blob calls task_yield from
	// deep call stacks and the extra depth risks overflowing the
	// goroutine stack.  Timer polling is handled by schedOnce() in
	// the ticker goroutine on its own stack.
	kickSched()
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

var wifiIntsOff uint32

//export espradio_wifi_int_disable
func espradio_wifi_int_disable(wifi_int_mux unsafe.Pointer) uint32 {
	s := uint32(interrupt.Disable())
	wifiIntsOff++
	return s
}

//export espradio_wifi_int_restore
func espradio_wifi_int_restore(wifi_int_mux unsafe.Pointer, tmp uint32) {
	if wifiIntsOff > 0 {
		wifiIntsOff--
	}
	interrupt.Restore(interrupt.State(tmp))
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
	for {
		mut.state.Lock()
		if mut.count == 0 || mut.owner == me {
			mut.owner = me
			mut.count++
			mut.state.Unlock()
			return 1
		}
		mut.state.Unlock()
		safeGosched()
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
		safeGosched()
	}
}

// ─── Semaphores ────────────────────────────────────────────────────────────────

type semaphore struct {
	count uint32
}

var (
	semaphores        [4]semaphore
	semaphoreIndex    uint32
	wifiThreadSemMu   sync.Mutex
	wifiThreadSemByTH = map[unsafe.Pointer]*semaphore{}
	wifiThreadSemNil  semaphore
	threadSems        [8]semaphore
	threadSemIndex    uint32
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
	i := atomic.AddUint32(&semaphoreIndex, 1) - 1
	if i >= uint32(len(semaphores)) {
		panic("espradio: too many semaphores")
	}
	semaphores[i] = semaphore{count: init}
	return unsafe.Pointer(&semaphores[i])
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
		// request. The ke-event re-entrancy this used to allow is now blocked by
		// the guard on modules_funcs[0x284] instead.
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
		safeGosched()
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
		i := atomic.AddUint32(&threadSemIndex, 1) - 1
		if i >= uint32(len(threadSems)) {
			panic("espradio: too many thread semaphores")
		}
		sem = &threadSems[i]
		wifiThreadSemByTH[task] = sem
	}
	return unsafe.Pointer(sem)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
