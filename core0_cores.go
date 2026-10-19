//go:build (esp32c3 || esp32c3_qemu_target || esp32s3 || esp32) && scheduler.cores

package espradio

import (
	"machine"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// The blob runs on core 0 only, like the Wi-Fi and BT tasks in ESP-IDF. Its
// interrupts are routed to core 0 and its critical sections mask only the
// calling core.

var (
	core0Mu    sync.Mutex
	core0Tasks = map[unsafe.Pointer]struct{}{}
)

// pinCore0 assigns the calling goroutine to core 0.
func pinCore0() {
	machine.AssignCore(0)
	t := tinygo_task_current()
	core0Mu.Lock()
	core0Tasks[t] = struct{}{}
	core0Mu.Unlock()
}

// unpinCore0 must be called by a pinned goroutine before it exits.
func unpinCore0() {
	t := tinygo_task_current()
	core0Mu.Lock()
	delete(core0Tasks, t)
	core0Mu.Unlock()
}

// onCore0Direct reports whether the caller was pinned with pinCore0.
func onCore0Direct() bool {
	t := tinygo_task_current()
	core0Mu.Lock()
	_, ok := core0Tasks[t]
	core0Mu.Unlock()
	return ok
}

// onCore0 runs fn on core 0 and waits for it to return.
func onCore0(fn func()) {
	if onCore0Direct() {
		fn()
		return
	}
	done := make(chan struct{})
	go func() {
		pinCore0()
		fn()
		unpinCore0()
		releaseThreadSem(tinygo_task_current())
		close(done)
	}()
	<-done
}

// A channel send from an interrupt can take the scheduler lock, which the
// interrupted core may already hold. So the interrupt only sets a flag.
var schedKicked atomic.Uint32

func initKick() {}

func kickSched() {
	schedKicked.Store(1)
}

// waitSchedKick waits for a kick or for one scheduler tick.
func waitSchedKick() {
	for i := 0; i < schedTickerMs; i++ {
		if schedKicked.Swap(0) != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
