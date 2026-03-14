package espradio

/*
#include "include.h"
void espradio_call_saved_isr(int32_t n);
int32_t espradio_queue_send(void *queue, void *item, uint32_t block_time_tick);
uint32_t espradio_isr_ring_head(void);
uint32_t espradio_isr_ring_tail(void);
void     espradio_isr_ring_advance_tail(void);
void    *espradio_isr_ring_entry_queue(uint32_t idx);
void    *espradio_isr_ring_entry_item(uint32_t idx);
*/
import "C"

import "runtime/interrupt"

// WiFi MAC/BB → CPU interrupt 1.
// Blob registers ISR via set_isr (osi.c), hardware enable via ints_on.
// TinyGo dispatches via callHandlers — requires compile-time registration.
func initWiFiISR() {
	interrupt.New(1, func(interrupt.Interrupt) {
		C.espradio_call_saved_isr(1)
	})
}

// drainISRQueue forwards items buffered by ISR ring (C) to the Go-side queue_send.
// ISR writes to a C ring buffer to avoid calling Go from interrupt context.
func drainISRQueue() {
	for C.espradio_isr_ring_tail() != C.espradio_isr_ring_head() {
		println("osi: drainISRQueue")
		idx := C.espradio_isr_ring_tail()
		q := C.espradio_isr_ring_entry_queue(idx)
		item := C.espradio_isr_ring_entry_item(idx)
		C.espradio_queue_send(q, item, 0)
		C.espradio_isr_ring_advance_tail()
	}
}
