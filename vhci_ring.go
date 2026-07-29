//go:build esp32c3 || esp32c3_qemu_target

package espradio

/*
#include <stdint.h>
int  espradio_vhci_ring_push(const uint8_t *data, int len);
int  espradio_vhci_ring_capacity(void);
void espradio_vhci_ring_reset(void);
int  espradio_vhci_buffered(void);
int  espradio_vhci_read_byte(void);
int  espradio_vhci_read(uint8_t *buf, int max_len);
*/
import "C"

import "unsafe"

// The VHCI RX ring is the controller -> host byte stream behind VHCITransport.
// These wrappers exist so it can be exercised without the BTDM blob; on the
// device the same functions are driven by the controller's recv callback.

// vhciRingReset empties the ring.
func vhciRingReset() { C.espradio_vhci_ring_reset() }

// vhciRingCapacity is the greatest number of bytes the ring can hold.
func vhciRingCapacity() int { return int(C.espradio_vhci_ring_capacity()) }

// vhciRingPush appends b, returning how many bytes were stored. A short return
// means the ring filled and the remainder was dropped.
func vhciRingPush(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return int(C.espradio_vhci_ring_push((*C.uint8_t)(unsafe.Pointer(&b[0])), C.int(len(b))))
}

// vhciBuffered reports how many bytes are available to read.
func vhciBuffered() int { return int(C.espradio_vhci_buffered()) }

// vhciReadByte pops one byte, or returns -1 when empty.
func vhciReadByte() int { return int(C.espradio_vhci_read_byte()) }

// vhciRead drains up to len(buf) bytes, returning the count.
func vhciRead(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	return int(C.espradio_vhci_read((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
}
