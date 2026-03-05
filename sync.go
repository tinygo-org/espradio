package espradio

// Various functions related to locks, mutexes, semaphores, and queues.
//
// WiFi task queue: driver messages in the queue are 8 bytes [cmd, p1..p7].
// cmd is the internal API command type (not documented in public IDF headers).
// Observed during init: 6 (right after wifi task start), 15 (right after semaphore creation).
// Implementation lives in binary blobs; we only log cmd and do not block waiting for a reply.

/*
#include "include.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

func wifiCmdString(cmd byte) string {
	switch cmd {
	case 6:
		return "6 (wifi_task init step / set_log?)"
	case 15:
		return "15 (wifi_task init step)"
	default:
		return fmt.Sprintf("%d", cmd)
	}
}

// Use a single fake spinlock. This is also how the Rust port does it.
var fakeSpinLock uint8

//export espradio_spin_lock_create
func espradio_spin_lock_create() unsafe.Pointer {
	return unsafe.Pointer(&fakeSpinLock)
}

//export espradio_spin_lock_delete
func espradio_spin_lock_delete(lock unsafe.Pointer) {
}

// Use a small pool of mutexes. The blobs don't need that many, and we can
// reuse freed ones.
var mutexes [8]sync.Mutex
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
	mut := (*sync.Mutex)(cmut)
	for i := range mutexes {
		if mut == &mutexes[i] {
			atomic.StoreUint32(&mutexInUse[i], 0)
			return
		}
	}
}

//export espradio_mutex_lock
func espradio_mutex_lock(cmut unsafe.Pointer) int32 {
	// This is xSemaphoreTake with an infinite timeout in ESP-IDF. Therefore,
	// just lock the mutex and return true.
	// TODO: recursive locking. See:
	// https://www.freertos.org/RTOS-Recursive-Mutexes.html
	// For that we need to track the current goroutine - or maybe just whether
	// we're inside a special goroutine like the timer goroutine.
	mut := (*sync.Mutex)(cmut)
	mut.Lock()
	return 1
}

//export espradio_mutex_unlock
func espradio_mutex_unlock(cmut unsafe.Pointer) int32 {
	// Note: this is xSemaphoreGive in the ESP-IDF, which doesn't panic when
	// unlocking fails but rather returns false.
	mut := (*sync.Mutex)(cmut)
	mut.Unlock()
	return 1
}

type semaphore chan struct{}

var semaphores [2]semaphore
var semaphoreIndex uint32

var wifiSemaphore semaphore

//export espradio_semphr_create
func espradio_semphr_create(max, init uint32) unsafe.Pointer {
	i := atomic.AddUint32(&semaphoreIndex, 1) - 1
	if i >= 2 {
		panic("espradio: too many semaphores")
	}
	ch := make(semaphore, max)
	for j := uint32(0); j < init; j++ {
		ch <- struct{}{}
	}
	semaphores[i] = ch
	ptr := unsafe.Pointer(&semaphores[i])
	println("espradio_semphr_create", max, init, "->", ptr)
	return ptr
}

//export espradio_semphr_take
func espradio_semphr_take(semphr unsafe.Pointer, block_time_tick uint32) int32 {
	sem := (*semaphore)(semphr)
	println("espradio_semphr_take", block_time_tick, "sem", semphr)
	if block_time_tick == 0 {
		select {
		case <-*sem:
			return 1
		default:
			return 0
		}
	}
	if block_time_tick == C.OSI_FUNCS_TIME_BLOCKING {
		<-*sem
		return 1
	}
	d := time.Duration(ticksToMilliseconds(block_time_tick)) * time.Millisecond
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-*sem:
		return 1
	case <-timer.C:
		return 0
	}
}

//export espradio_semphr_give
func espradio_semphr_give(semphr unsafe.Pointer) int32 {
	sem := (*semaphore)(semphr)
	*sem <- struct{}{}
	println("espradio_semphr_give", semphr)
	return 1
}

//export espradio_semphr_delete
func espradio_semphr_delete(semphr unsafe.Pointer) {
	sem := (*semaphore)(semphr)
	close(*sem)
}

//export espradio_wifi_thread_semphr_get
func espradio_wifi_thread_semphr_get() unsafe.Pointer {
	if wifiSemaphore == nil {
		wifiSemaphore = make(semaphore, 1)
	}
	return unsafe.Pointer(&wifiSemaphore)
}

type queue chan [8]byte

var (
	wifiQueue    queue
	wifiQueuePtr unsafe.Pointer
)

func init() {
	wifiQueue = make(queue, 1)
}

//export espradio_wifi_create_queue
func espradio_wifi_create_queue(queue_len, item_size int) unsafe.Pointer {
	if item_size != 8 {
		panic("espradio: unexpected queue item_size")
	}
	wifiQueuePtr = unsafe.Pointer(&wifiQueue)
	println("espradio_wifi_create_queue", queue_len, item_size, "&wifiQueue", wifiQueuePtr)
	return wifiQueuePtr
}

//export espradio_wifi_delete_queue
func espradio_wifi_delete_queue(ptr unsafe.Pointer) {
	println("espradio_wifi_delete_queue")
}

func queueFromPtr(ptr unsafe.Pointer) *queue {
	if ptr == wifiQueuePtr {
		return (*queue)(ptr)
	}
	realPtr := *(*unsafe.Pointer)(ptr)
	if realPtr != nil {
		return (*queue)(realPtr)
	}
	return (*queue)(wifiQueuePtr)
}

//export espradio_queue_recv
func espradio_queue_recv(ptr unsafe.Pointer, item unsafe.Pointer, block_time_tick uint32) int32 {
	q := queueFromPtr(ptr)
	if block_time_tick != C.OSI_FUNCS_TIME_BLOCKING {
		panic("espradio: todo: queue_recv with timeout")
	}
	runtime.Gosched()
	cmd := <-*q
	println("espradio_queue_recv got cmd", wifiCmdString(cmd[0]),
		"params:", cmd[1], cmd[2], cmd[3], cmd[4], cmd[5], cmd[6], cmd[7])
	*(*[8]byte)(item) = cmd
	println("espradio_queue_recv return")
	return 1
}

//export espradio_queue_send
func espradio_queue_send(ptr unsafe.Pointer, item unsafe.Pointer, block_time_tick uint32) int32 {
	q := queueFromPtr(ptr)
	cmd := *(*[8]byte)(item)
	println("espradio_queue_send", block_time_tick, "cmd", wifiCmdString(cmd[0]))
	*q <- cmd
	return 1
}

//export espradio_queue_len
func espradio_queue_len(ptr unsafe.Pointer) uint32 {
	q := queueFromPtr(ptr)
	return uint32(len(*q))
}

type eventGroup struct {
	mu   sync.Mutex
	cond *sync.Cond
	bits uint32
}

func newEventGroup() *eventGroup {
	eg := &eventGroup{}
	eg.cond = sync.NewCond(&eg.mu)
	return eg
}

//export espradio_event_group_create
func espradio_event_group_create() unsafe.Pointer {
	eg := newEventGroup()
	println("espradio_event_group_create", eg)
	return unsafe.Pointer(eg)
}

//export espradio_event_group_delete
func espradio_event_group_delete(ptr unsafe.Pointer) {
	println("espradio_event_group_delete", ptr)
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits = 0
	eg.mu.Unlock()
}

//export espradio_event_group_set_bits
func espradio_event_group_set_bits(ptr unsafe.Pointer, bits uint32) uint32 {
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits |= bits
	cur := eg.bits
	eg.mu.Unlock()
	eg.cond.Broadcast()
	println("espradio_event_group_set_bits", ptr, "bits", bits, "->", cur)
	return cur
}

//export espradio_event_group_clear_bits
func espradio_event_group_clear_bits(ptr unsafe.Pointer, bits uint32) uint32 {
	eg := (*eventGroup)(ptr)
	eg.mu.Lock()
	eg.bits &^= bits
	cur := eg.bits
	eg.mu.Unlock()
	println("espradio_event_group_clear_bits", ptr, "bits", bits, "->", cur)
	return cur
}

//export espradio_event_group_wait_bits
func espradio_event_group_wait_bits(ptr unsafe.Pointer, bitsToWaitFor uint32, clearOnExit int32, waitForAllBits int32, blockTimeTick uint32) uint32 {
	eg := (*eventGroup)(ptr)
	want := bitsToWaitFor

	predicate := func() bool {
		if waitForAllBits != 0 {
			return eg.bits&want == want
		}
		return eg.bits&want != 0
	}

	eg.mu.Lock()
	defer eg.mu.Unlock()

	println("espradio_event_group_wait_bits enter", ptr, "want", want, "bits", eg.bits, "block", blockTimeTick)

	switch {
	case blockTimeTick == 0:
		// Just check the current state.
	case blockTimeTick == C.OSI_FUNCS_TIME_BLOCKING:
		for !predicate() {
			eg.cond.Wait()
		}
	default:
		timeout := time.Duration(ticksToMilliseconds(blockTimeTick)) * time.Millisecond
		deadline := time.Now().Add(timeout)
		for !predicate() {
			now := time.Now()
			if !now.Before(deadline) {
				break
			}
			// cond.Wait has no timeout, so we approximate with sleep loops.
			eg.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			eg.mu.Lock()
		}
	}

	cur := eg.bits
	if predicate() && clearOnExit != 0 {
		eg.bits &^= want
		println("espradio_event_group_wait_bits clearOnExit", ptr, "clear", want, "->", eg.bits)
		cur = eg.bits
	}

	println("espradio_event_group_wait_bits exit", ptr, "want", want, "got", cur)
	return cur
}
