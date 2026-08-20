//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package espradio

/*
#include <stdint.h>
*/
import "C"

import "runtime/volatile"

// The VHCI RX ring is the controller -> host byte stream behind VHCITransport.
//
// It is the one part of the BLE path with no dependency on the BTDM blob or on
// hardware: the controller pushes bytes in from its recv callback
// (vhci_host_recv_cb in bt_ble.c) and Go drains them. That is what makes it
// reachable from the esp32c3-qemu unit-test target.
//
// head == tail means empty, so one slot is never usable and the ring holds
// vhciRingSize-1 bytes.
//
// The indices are accessed through runtime/volatile, not sync/atomic. That is
// deliberate and it matters: the ESP32-C3 is RV32IMC with no atomic extension
// (its target feature string has no "+a"), so sync/atomic lowers to
// __atomic_load_4 / __atomic_store_4 libcalls and TinyGo implements those by
// masking interrupts around the access. In the per-byte loops below -- and in the
// push path, which runs in the controller's recv callback -- that starved the BLE
// controller's real-time interrupts badly enough to stall notification delivery
// on a central. volatile is what the C original used: a plain load/store the
// compiler may not cache in a register, with no call and no interrupt masking.
// Single-writer-per-index is what makes that sufficient.
const vhciRingSize = 2048

var (
	vhciRXBuf  [vhciRingSize]byte
	vhciRXHead uint32 // written by the recv callback (interrupt context)
	vhciRXTail uint32 // written by Go
)

// espradio_vhci_ring_push is the producer side, called from the controller's
// recv callback in bt_ble.c.
//
// On the C3 that callback runs in real interrupt context, so this path must not
// allocate or yield. cArrayToBytes is a bare unsafe.Slice over the caller's
// buffer and does neither.
//
//export espradio_vhci_ring_push
func espradio_vhci_ring_push(data *C.uint8_t, length C.int) C.int {
	if data == nil || length <= 0 {
		return 0
	}
	return C.int(vhciRingPush(cArrayToBytes(data, int(length))))
}

// vhciRingPush appends b, returning how many bytes were stored. A short return
// means the ring filled and the remainder was dropped.
//
// The caller is an HCI packet boundary, so a short return means a truncated
// packet was committed to the byte stream, and a host parser reading it will
// desync -- see TestVHCIRingOverflowTruncatesPacket, which documents that as a
// known limitation of a ring with no packet framing.
func vhciRingPush(b []byte) int {
	head := volatile.LoadUint32(&vhciRXHead)
	n := 0
	for ; n < len(b); n++ {
		next := (head + 1) % vhciRingSize
		// Re-read the tail every iteration rather than once up front: the
		// consumer publishes progress per byte, so space it frees mid-push has to
		// be usable here. Reading it once let a push that began against a
		// near-full ring give up while the consumer was actively draining, which
		// on an HCI stream means a truncated packet rather than a slow one.
		if next == volatile.LoadUint32(&vhciRXTail) {
			break // full
		}
		vhciRXBuf[head] = b[n]
		// Publish the byte before the index so the consumer can never observe a
		// slot it is cleared to read before the byte has landed in it.
		volatile.StoreUint32(&vhciRXHead, next)
		head = next
	}
	return n
}

// vhciBuffered reports how many bytes are available to read.
func vhciBuffered() int {
	head := volatile.LoadUint32(&vhciRXHead)
	tail := volatile.LoadUint32(&vhciRXTail)
	if head >= tail {
		return int(head - tail)
	}
	return int(vhciRingSize - tail + head)
}

// vhciReadByte pops one byte, or returns -1 when empty.
func vhciReadByte() int {
	tail := volatile.LoadUint32(&vhciRXTail)
	if volatile.LoadUint32(&vhciRXHead) == tail {
		return -1
	}
	b := vhciRXBuf[tail]
	volatile.StoreUint32(&vhciRXTail, (tail+1)%vhciRingSize)
	return int(b)
}

// vhciRead drains up to len(buf) bytes, returning the count.
func vhciRead(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	tail := volatile.LoadUint32(&vhciRXTail)
	n := 0
	for n < len(buf) && volatile.LoadUint32(&vhciRXHead) != tail {
		buf[n] = vhciRXBuf[tail]
		n++
		tail = (tail + 1) % vhciRingSize
		// Publish progress per byte so a producer in interrupt context sees the
		// freed space immediately instead of only when the whole drain finishes.
		volatile.StoreUint32(&vhciRXTail, tail)
	}
	return n
}

// vhciRingCapacity is the greatest number of bytes the ring can hold.
func vhciRingCapacity() int { return vhciRingSize - 1 }

// vhciRingReset empties the ring.
func vhciRingReset() {
	volatile.StoreUint32(&vhciRXHead, 0)
	volatile.StoreUint32(&vhciRXTail, 0)
}
