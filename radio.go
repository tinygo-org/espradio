package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0

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

type Config struct {
	Logging LogLevel
}

type AccessPoint struct {
	SSID string
	RSSI int
}

type STAConfig struct {
	SSID     string
	Password string
}

type ConnectResult struct {
	Connected bool
	SSID      string
	Channel   uint8
	Reason    uint8
}

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
		}
	}()
}

func schedOnce() {
	// Poll the WiFi blob ISR to catch events where the hardware level
	// stayed asserted but no new edge was generated.  This compensates
	// for the edge-triggered interrupt mode losing events when the
	// peripheral line never transitions low between assertions.
	// Interrupts are disabled during the poll to prevent reentrant
	// calls to the blob ISR from a real edge interrupt.
	mask := interrupt.Disable()
	C.espradio_call_wifi_isr()
	interrupt.Restore(mask)

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
}

func kickSched() {
	select {
	case isrKick <- struct{}{}:
	default:
	}
}

// Enable and configure the radio.
func Enable(config Config) error {
	startSchedTicker()
	time.Sleep(schedTickerMs * time.Millisecond)
	initHardware()
	C.espradio_ensure_osi_ptr()

	wifiISR = interrupt.New(1, func(interrupt.Interrupt) {
		C.espradio_call_wifi_isr()
		kickSched()
	})
	wifiISR.Enable()

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
	C.espradio_netif_init_netstack_cb()

	return nil
}

// ─── Start / Scan ────────────────────────────────────────────────────────────

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

	if code := C.espradio_esp_wifi_start(); code != C.ESP_OK {
		return makeError(code)
	}

	return nil
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

func ticksToMilliseconds(ticks uint32) uint32 {
	return ticks / (ticksPerSecond / 1000)
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

const taskStackSize = 8192

//export espradio_task_create_pinned_to_core
func espradio_task_create_pinned_to_core(task_func unsafe.Pointer, name *C.char, stack_depth uint32, param unsafe.Pointer, prio uint32, task_handle *unsafe.Pointer, core_id uint32) int32 {
	ch := make(chan struct{}, 1)
	go func() {
		*task_handle = tinygo_task_current()
		close(ch)
		espradio_run_task(task_func, param)
	}()
	<-ch
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

var (
	timerGenMu sync.Mutex
	timerGen   map[uintptr]uint32
)

func timerArmGeneration(timer unsafe.Pointer) uint32 {
	key := uintptr(timer)
	timerGenMu.Lock()
	defer timerGenMu.Unlock()
	if timerGen == nil {
		timerGen = make(map[uintptr]uint32)
	}
	g := timerGen[key] + 1
	timerGen[key] = g
	return g
}

func timerGenerationAlive(timer unsafe.Pointer, gen uint32) bool {
	key := uintptr(timer)
	timerGenMu.Lock()
	defer timerGenMu.Unlock()
	if timerGen == nil {
		return false
	}
	return timerGen[key] == gen
}

//export espradio_timer_cancel_go
func espradio_timer_cancel_go(timer unsafe.Pointer) {
	key := uintptr(timer)
	timerGenMu.Lock()
	if timerGen == nil {
		timerGen = make(map[uintptr]uint32)
	}
	timerGen[key] = timerGen[key] + 1
	timerGenMu.Unlock()
}

//export espradio_timer_arm_go
func espradio_timer_arm_go(timer unsafe.Pointer, tmout_ticks uint32, repeat int32) {
	ms := ticksToMilliseconds(tmout_ticks)
	if ms == 0 {
		ms = 1
	}
	gen := timerArmGeneration(timer)
	go func(gen uint32) {
		d := time.Duration(ms) * time.Millisecond
		if repeat != 0 {
			for {
				time.Sleep(d)
				if !timerGenerationAlive(timer, gen) {
					return
				}
				C.espradio_timer_fire(timer)
			}
		}
		time.Sleep(d)
		if !timerGenerationAlive(timer, gen) {
			return
		}
		C.espradio_timer_fire(timer)
	}(gen)
}

//export espradio_timer_arm_go_us
func espradio_timer_arm_go_us(timer unsafe.Pointer, us uint32, repeat int32) {
	if us == 0 {
		us = 1
	}
	gen := timerArmGeneration(timer)
	go func(gen uint32) {
		d := time.Duration(us) * time.Microsecond
		if repeat != 0 {
			for {
				time.Sleep(d)
				if !timerGenerationAlive(timer, gen) {
					return
				}
				C.espradio_timer_fire(timer)
			}
		}
		time.Sleep(d)
		if !timerGenerationAlive(timer, gen) {
			return
		}
		C.espradio_timer_fire(timer)
	}(gen)
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

const debugOSI = false

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

	matches := func(bits uint32) bool {
		if waitForAllBits != 0 {
			return bits&want == want
		}
		return bits&want != 0
	}

	forever := blockTimeTick == C.OSI_FUNCS_TIME_BLOCKING
	start := time.Now()
	var timeout time.Duration
	if !forever {
		timeout = time.Duration(blockTimeTick) * time.Millisecond
	}

	var snapshot uint32
	for {
		eg.mu.Lock()
		snapshot = eg.bits
		ok := matches(snapshot)
		if ok {
			if clearOnExit != 0 {
				eg.bits &^= want
			}
			eg.mu.Unlock()
			return snapshot
		}
		eg.mu.Unlock()
		if blockTimeTick == 0 || (!forever && time.Since(start) >= timeout) {
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
)

func wifiThreadSemOwner(semphr unsafe.Pointer) unsafe.Pointer {
	wifiThreadSemMu.Lock()
	defer wifiThreadSemMu.Unlock()
	for th, sem := range wifiThreadSemByTH {
		if unsafe.Pointer(sem) == semphr {
			return th
		}
	}
	return nil
}

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
	owner := wifiThreadSemOwner(semphr)
	_ = owner
	if block_time_tick == 0 {
		if semTryTake(sem) {
			return 1
		}
		return 0
	}

	forever := block_time_tick == C.OSI_FUNCS_TIME_BLOCKING
	start := time.Now()
	var timeout time.Duration
	if !forever {
		timeout = time.Duration(block_time_tick) * time.Millisecond
	}

	for {
		if semTryTake(sem) {
			return 1
		}
		if !forever && time.Since(start) >= timeout {
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
		sem = &semaphore{}
		wifiThreadSemByTH[task] = sem
	}
	return unsafe.Pointer(sem)
}

// ─── Queues ────────────────────────────────────────────────────────────────────

type queue struct {
	mu      sync.Mutex
	storage [][8]byte
	read    int
	write   int
	count   int
}

func newQueue(capacity int) *queue {
	return &queue{
		storage: make([][8]byte, capacity),
	}
}

func (q *queue) enqueue(cmd [8]byte) int32 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == len(q.storage) {
		return 0
	}
	q.storage[q.write] = cmd
	q.write++
	if q.write == len(q.storage) {
		q.write = 0
	}
	q.count++
	return 1
}

func (q *queue) dequeue(out *[8]byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		return false
	}
	*out = q.storage[q.read]
	q.read++
	if q.read == len(q.storage) {
		q.read = 0
	}
	q.count--
	return true
}

func (q *queue) length() uint32 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return uint32(q.count)
}

var (
	wifiQueueObj    *queue
	wifiQueuePtr    unsafe.Pointer
	wifiQueueHandle unsafe.Pointer
	queueMapMu      sync.Mutex
	queueByAlias    = map[unsafe.Pointer]*queue{}
	queueByHandle   = map[unsafe.Pointer]*queue{}
)

func registerQueue(handle unsafe.Pointer, alias unsafe.Pointer, q *queue) {
	queueMapMu.Lock()
	queueByHandle[handle] = q
	if alias != nil {
		queueByAlias[alias] = q
	}
	queueMapMu.Unlock()
}

func unregisterQueue(handle unsafe.Pointer, alias unsafe.Pointer) {
	queueMapMu.Lock()
	delete(queueByHandle, handle)
	if alias != nil {
		delete(queueByAlias, alias)
	}
	queueMapMu.Unlock()
}

func queueFromPtr(ptr unsafe.Pointer) *queue {
	queueMapMu.Lock()
	q := queueByAlias[ptr]
	if q == nil {
		q = queueByHandle[ptr]
	}
	queueMapMu.Unlock()
	if q != nil {
		return q
	}
	if ptr != nil {
		handle := *(*unsafe.Pointer)(ptr)
		queueMapMu.Lock()
		q = queueByHandle[handle]
		queueMapMu.Unlock()
		if q != nil {
			return q
		}
	}
	return nil
}

//export espradio_generic_queue_create
func espradio_generic_queue_create(queue_len, item_size int) unsafe.Pointer {
	if item_size != 8 {
		item_size = 8
	}
	if queue_len < 1 {
		queue_len = 1
	}
	q := newQueue(queue_len)
	handle := unsafe.Pointer(q)
	ptr := unsafe.Pointer(&handle)
	registerQueue(handle, ptr, q)
	return ptr
}

//export espradio_generic_queue_delete
func espradio_generic_queue_delete(ptr unsafe.Pointer) {
	if ptr != nil {
		unregisterQueue(ptr, nil)
	}
}

//export espradio_wifi_create_queue
func espradio_wifi_create_queue(queue_len, item_size int) unsafe.Pointer {
	if item_size != 8 {
		panic("espradio: unexpected queue item_size")
	}
	if queue_len < 1 {
		queue_len = 1
	}
	wifiQueueObj = newQueue(queue_len)
	wifiQueueHandle = unsafe.Pointer(wifiQueueObj)
	wifiQueuePtr = unsafe.Pointer(&wifiQueueHandle)
	registerQueue(wifiQueueHandle, wifiQueuePtr, wifiQueueObj)
	return wifiQueuePtr
}

//export espradio_wifi_delete_queue
func espradio_wifi_delete_queue(ptr unsafe.Pointer) {
	unregisterQueue(wifiQueueHandle, wifiQueuePtr)
	if ptr != nil && ptr != wifiQueuePtr {
		unregisterQueue(ptr, nil)
	}
	wifiQueueObj = nil
	wifiQueueHandle = nil
	wifiQueuePtr = nil
}

//export espradio_queue_recv
func espradio_queue_recv(ptr unsafe.Pointer, item unsafe.Pointer, block_time_tick uint32) int32 {
	q := queueFromPtr(ptr)
	if q == nil {
		return 0
	}

	forever := block_time_tick == C.OSI_FUNCS_TIME_BLOCKING
	start := time.Now()
	var timeout time.Duration
	if !forever {
		timeout = time.Duration(block_time_tick) * time.Millisecond
	}

	var cmd [8]byte
	for {
		if q.dequeue(&cmd) {
			*(*[8]byte)(item) = cmd
			return 1
		}
		if !forever && time.Since(start) >= timeout {
			return 0
		}
		safeGosched()
	}
}

//export espradio_queue_send
func espradio_queue_send(ptr unsafe.Pointer, item unsafe.Pointer, block_time_tick uint32) int32 {
	q := queueFromPtr(ptr)
	if q == nil {
		return 0
	}
	_ = block_time_tick
	cmd := *(*[8]byte)(item)
	return q.enqueue(cmd)
}

//export espradio_queue_len
func espradio_queue_len(ptr unsafe.Pointer) uint32 {
	q := queueFromPtr(ptr)
	if q == nil {
		return 0
	}
	return q.length()
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
