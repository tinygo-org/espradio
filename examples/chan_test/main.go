// chan_test verifies channel behavior and goroutine switching on the device.
// If all cases pass, queues/semaphores can safely be implemented using channels in sync.go.
package main

import (
	"runtime"
	"time"
)

type msg [8]byte

const schedIntervalMs = 5

func startSchedTicker() {
	go func() {
		for {
			time.Sleep(schedIntervalMs * time.Millisecond)
			runtime.Gosched()
		}
	}()
}

func main() {
	time.Sleep(500 * time.Millisecond)
	println("chan_test: start")
	startSchedTicker()
	time.Sleep(schedIntervalMs * time.Millisecond)

	// Case 1: buffer size 1, worker is already in recv before send (like current sync: array + yield)
	runCase1()
	// Case 2: worker sleeps before first recv (simulates C-init delay in the driver)
	runCase2()
	// Case 3: unbuffered queue (cap 0) — main blocks on send until worker does recv
	runCase3()

	println("chan_test: all OK — channels and goroutine switch work")
	for {
		time.Sleep(5 * time.Second)
	}
}

func runCase1() {
	queue := make(chan msg, 1)
	done := make(chan struct{}, 1)
	go func() {
		println("chan_test case1: worker waiting for msg")
		m := <-queue
		println("chan_test case1: worker got msg[0]=", m[0])
		done <- struct{}{}
	}()
	runtime.Gosched()
	time.Sleep(20 * time.Millisecond)
	queue <- msg{6}
	<-done
	println("chan_test case1: OK")
}

func runCase2() {
	queue := make(chan msg, 1)
	done := make(chan struct{}, 1)
	go func() {
		time.Sleep(80 * time.Millisecond) // like the delay before first queue_recv in the blob
		println("chan_test case2: worker after sleep, waiting for msg")
		m := <-queue
		println("chan_test case2: worker got msg[0]=", m[0])
		done <- struct{}{}
	}()
	queue <- msg{6}
	println("chan_test case2: main sent, waiting done...")
	<-done
	println("chan_test case2: OK")
}

func runCase3() {
	queue := make(chan msg, 0) // unbuffered: send blocks until recv
	done := make(chan struct{}, 1)
	go func() {
		println("chan_test case3: worker waiting for msg (unbuffered)")
		m := <-queue
		println("chan_test case3: worker got msg[0]=", m[0])
		done <- struct{}{}
	}()
	runtime.Gosched()
	time.Sleep(20 * time.Millisecond)
	queue <- msg{15}
	<-done
	println("chan_test case3: OK")
}
