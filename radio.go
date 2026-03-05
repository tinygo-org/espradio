package espradio

/*
// TODO: vary these by chip
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/esp32c3
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo LDFLAGS: -Lblobs/libs/esp32c3 -lcore -lmesh -lnet80211 -lespnow -lregulatory -lphy -lpp -lwpa_supplicant

#include "include.h"
esp_err_t espradio_wifi_init(void);
void espradio_wifi_init_completed(void);
void espradio_timer_fire(void *ptimer);
void espradio_event_register_default_cb(void);
*/
import "C"
import (
	"runtime"
	"runtime/interrupt"
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

func startSchedTicker() {
	go func() {
		for {
			time.Sleep(schedTickerMs * time.Millisecond)
			runtime.Gosched()
		}
	}()
}

// Enable and configure the radio.
func Enable(config Config) error {
	startSchedTicker()
	time.Sleep(schedTickerMs * time.Millisecond)
	initHardware()
	C.espradio_event_register_default_cb()

	// TODO: run timers in separate goroutine

	// Уровень логов WiFi из блоба (INFO/DEBUG/VERBOSE). Работает только если блоб собран с CONFIG_LOG_MAXIMUM_LEVEL >= уровня.
	errCode := C.esp_wifi_internal_set_log_level(C.wifi_log_level_t(config.Logging))
	if errCode != 0 {
		return makeError(errCode)
	}

	// TODO: BLE needs the interrupts RWBT, RWBLE, BT_BB

	mask := interrupt.Disable()
	// TODO: setup 200Hz tick rate timer
	// TODO: init_clocks
	interrupt.Restore(mask)

	// Initialize the wireless stack.
	// Contract: blobs/include/esp_private/wifi.h — esp_err_t esp_wifi_init_internal(const wifi_init_config_t*);
	//           esp_err_t is int (esp_err.h). Return: ESP_OK (0) or error (e.g. ESP_ERR_NO_MEM 0x101). Symbol in libnet80211.a.
	// In practice the blob sometimes returns a DRAM address (e.g. 0x3FC83136); we treat that as success (isPointerLike).
	errCode = C.espradio_wifi_init()
	if errCode != 0 {
		if isPointerLike(errCode) {
			// see comment above
		} else {
			return makeError(errCode)
		}
	}

	// Воркер обрабатывает cmd 6, cmd 15 асинхронно. Вызываем wifi_init_completed() после паузы,
	// чтобы драйвер выставил флаг "inited" (иначе esp_wifi_set_mode даёт NOT_INIT).
	time.Sleep(400 * time.Millisecond)
	C.espradio_wifi_init_completed()
	time.Sleep(100 * time.Millisecond)
	if config.Logging >= LogLevelDebug {
		_ = C.esp_wifi_internal_set_log_mod(0, 0, true) // WIFI_LOG_MODULE_ALL, WIFI_LOG_SUBMODULE_ALL, enable
	}
	const initWaitMs = 2000
	deadline := time.Now().Add(initWaitMs * time.Millisecond)
	for time.Now().Before(deadline) {
		if code := C.esp_wifi_set_mode(C.WIFI_MODE_STA); code == C.ESP_OK {
			C.esp_wifi_set_mode(C.WIFI_MODE_NULL)
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return makeError(C.esp_err_t(0x3001)) // ESP_ERR_WIFI_NOT_INIT
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

//export espradio_task_create_pinned_to_core
func espradio_task_create_pinned_to_core(task_func unsafe.Pointer, name *C.char, stack_depth uint32, param unsafe.Pointer, prio uint32, task_handle *unsafe.Pointer, core_id uint32) int32 {
	println("espradio: driver task create pinned to core", task_func, name, stack_depth, param, prio, task_handle, core_id)
	ch := make(chan struct{}, 1)
	go func() {
		println("espradio: driver task goroutine started")
		*task_handle = tinygo_task_current()
		close(ch)
		espradio_run_task(task_func, unsafe.Pointer(task_handle))
	}()
	<-ch
	return 1
}

//export espradio_task_delete
func espradio_task_delete(task_handle unsafe.Pointer) {
	println("espradio TODO: delete task", task_handle)
}

//export tinygo_task_current
func tinygo_task_current() unsafe.Pointer

//export espradio_task_get_current_task
func espradio_task_get_current_task() unsafe.Pointer {
	return tinygo_task_current()
}

//export espradio_timer_arm_go
func espradio_timer_arm_go(timer unsafe.Pointer, tmout_ticks uint32, repeat int32) {
	ms := ticksToMilliseconds(tmout_ticks)
	if ms == 0 {
		ms = 1
	}
	go func() {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		C.espradio_timer_fire(timer)
	}()
}

//export espradio_timer_arm_go_us
func espradio_timer_arm_go_us(timer unsafe.Pointer, us uint32, repeat int32) {
	if us == 0 {
		us = 1
	}
	go func() {
		time.Sleep(time.Duration(us) * time.Microsecond)
		C.espradio_timer_fire(timer)
	}()
}

//export espradio_task_delay
func espradio_task_delay(ticks uint32) {
	const ticksPerMillisecond = ticksPerSecond / 1000
	// Round milliseconds up.
	ms := (ticks + ticksPerMillisecond - 1) / ticksPerMillisecond
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

//export espradio_task_ms_to_tick
func espradio_task_ms_to_tick(ms uint32) int32 {
	return int32(millisecondsToTicks(ms))
}

//export espradio_wifi_int_disable
func espradio_wifi_int_disable(wifi_int_mux unsafe.Pointer) uint32 {
	// This is portENTER_CRITICAL (or portENTER_CRITICAL_ISR).
	return uint32(interrupt.Disable())
}

//export espradio_wifi_int_restore
func espradio_wifi_int_restore(wifi_int_mux unsafe.Pointer, tmp uint32) {
	// This is portEXIT_CRITICAL (or portEXIT_CRITICAL_ISR).
	interrupt.Restore(interrupt.State(tmp))
}
