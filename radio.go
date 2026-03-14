package espradio

/*
// TODO: vary these by chip
// For OSI callback logs from C (osi.c): add -DESPRADIO_OSI_DEBUG=1 to CFLAGS or set CGO_CFLAGS=-DESPRADIO_OSI_DEBUG=1
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo LDFLAGS: -Lblobs/libs/esp32c3 -lcoexist -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant

#include "include.h"
void espradio_set_blob_log_level(uint32_t level);
esp_err_t espradio_wifi_init(void);
void espradio_wifi_init_completed(void);
void espradio_timer_fire(void *ptimer);
void espradio_event_register_default_cb(void);
void espradio_event_loop_run_once(void);
int espradio_fire_one_pending_timer(void);
int espradio_timer_poll_due(int max_fire);
void espradio_fire_pending_timers(void);
int espradio_esp_timer_poll_due(int max_fire);
void espradio_prepare_memory_for_wifi(void);
void espradio_ensure_osi_ptr(void);
void espradio_coex_adapter_init(void);
void espradio_call_saved_isr(int32_t n);
int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
uint32_t espradio_isr_ring_head(void);
uint32_t espradio_isr_ring_tail(void);
void     espradio_isr_ring_advance_tail(void);
void    *espradio_isr_ring_entry_queue(uint32_t idx);
void    *espradio_isr_ring_entry_item(uint32_t idx);
void espradio_set_task_stack_bottom(unsigned long bottom);
unsigned long espradio_stack_remaining(void);
uint32_t espradio_wifi_boot_state(void);
void espradio_hal_init_clocks_go(void);
void espradio_test_pll(void);
int rtc_get_reset_reason(int cpu_no);
*/
import "C"
import (
	"runtime"
	"runtime/interrupt"
	"sync"
	"time"
	"unsafe"
)

type LogLevel uint8

// Various log levels to use inside the espradio. Higher log levels will produce
// more output over the serial console.
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

const schedTickerMs = 5

var eventLoopKick chan struct{}

func startSchedTicker() {
	eventLoopKick = make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(schedTickerMs * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// println("osi: event_loop ticker tick")
			case <-eventLoopKick:
				// println("osi: event_loop kick recv")
			}

			for C.espradio_isr_ring_tail() != C.espradio_isr_ring_head() {
				idx := C.espradio_isr_ring_tail()
				q := C.espradio_isr_ring_entry_queue(idx)
				item := C.espradio_isr_ring_entry_item(idx)
				C.espradio_queue_send(q, item, 0)
				C.espradio_isr_ring_advance_tail()
			}

			for i := 0; i < 4; i++ {
				C.espradio_event_loop_run_once()
			}
			for i := 0; i < 4; i++ {
				// println("osi: timer_poll_due call", i)
				fired := C.espradio_timer_poll_due(8)
				// println("osi: timer_poll_due ret", i, "fired=", fired)
				if fired == 0 {
					break
				}
			}
			for i := 0; i < 4; i++ {
				if C.espradio_esp_timer_poll_due(8) == 0 {
					break
				}
			}
		}
	}()
}

//export espradio_event_loop_kick_go
func espradio_event_loop_kick_go() {
	if eventLoopKick == nil {
		return
	}
	select {
	case eventLoopKick <- struct{}{}:
	default:
	}
}

// Enable and configure the radio.
func Enable(config Config) error {
	startSchedTicker()
	time.Sleep(schedTickerMs * time.Millisecond)
	initHardware()
	C.espradio_ensure_osi_ptr()
	initWiFiISR()
	C.espradio_event_register_default_cb()
	C.espradio_set_blob_log_level(C.uint32_t(config.Logging))

	mask := interrupt.Disable()
	C.espradio_hal_init_clocks_go()
	interrupt.Restore(mask)

	C.espradio_test_pll()

	errCode := C.espradio_wifi_init()
	if errCode != 0 {
		return makeError(errCode)
	}
	C.espradio_wifi_init_completed()
	C.espradio_test_pll()

	return nil
}

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
		var anchor byte
		top := uintptr(unsafe.Pointer(&anchor))
		bottom := top - taskStackSize
		C.espradio_set_task_stack_bottom(C.ulong(bottom))
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

//export espradio_task_yield_go
func espradio_task_yield_go() {
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

//export espradio_wifi_int_disable
func espradio_wifi_int_disable(wifi_int_mux unsafe.Pointer) uint32 {
	return uint32(interrupt.Disable())
}

//export espradio_wifi_int_restore
func espradio_wifi_int_restore(wifi_int_mux unsafe.Pointer, tmp uint32) {
	interrupt.Restore(interrupt.State(tmp))
}

var wifiISR interrupt.Interrupt

func initWiFiISR() {
	wifiISR = interrupt.New(1, func(interrupt.Interrupt) {
		C.espradio_call_saved_isr(1)
	})
}

func enableWiFiISR() {
	wifiISR.Enable()
}
