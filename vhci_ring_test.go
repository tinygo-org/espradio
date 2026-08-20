//go:build esp32c3 || esp32c3_qemu_target || esp32s3

package espradio

import "testing"

// The VHCI RX ring carries the raw HCI byte stream from the controller to the
// host. Nothing above it re-frames, so byte order, wraparound and the
// full-buffer path all have to be exact.

func drain(t *testing.T) []byte {
	t.Helper()
	var out []byte
	buf := make([]byte, 64)
	for {
		n := vhciRead(buf)
		if n == 0 {
			return out
		}
		out = append(out, buf[:n]...)
	}
}

func TestVHCIRingEmpty(t *testing.T) {
	vhciRingReset()

	if got := vhciBuffered(); got != 0 {
		t.Errorf("Buffered() on empty ring = %d, want 0", got)
	}
	if got := vhciReadByte(); got != -1 {
		t.Errorf("ReadByte() on empty ring = %d, want -1", got)
	}
	buf := make([]byte, 8)
	if got := vhciRead(buf); got != 0 {
		t.Errorf("Read() on empty ring = %d, want 0", got)
	}
}

func TestVHCIRingRoundTrip(t *testing.T) {
	vhciRingReset()

	// A representative HCI Command Complete event.
	pkt := []byte{0x04, 0x0E, 0x04, 0x01, 0x03, 0x0C, 0x00}
	if n := vhciRingPush(pkt); n != len(pkt) {
		t.Fatalf("push stored %d bytes, want %d", n, len(pkt))
	}
	if got := vhciBuffered(); got != len(pkt) {
		t.Errorf("Buffered() = %d, want %d", got, len(pkt))
	}

	got := drain(t)
	if string(got) != string(pkt) {
		t.Errorf("read back %v, want %v", got, pkt)
	}
	if got := vhciBuffered(); got != 0 {
		t.Errorf("Buffered() after drain = %d, want 0", got)
	}
}

func TestVHCIRingReadByteOrder(t *testing.T) {
	vhciRingReset()

	pkt := []byte{0x04, 0x3E, 0x2B, 0x02}
	vhciRingPush(pkt)

	for i, want := range pkt {
		got := vhciReadByte()
		if got != int(want) {
			t.Fatalf("byte %d = %d, want %d", i, got, want)
		}
	}
	if got := vhciReadByte(); got != -1 {
		t.Errorf("ReadByte() past end = %d, want -1", got)
	}
}

func TestVHCIRingPartialRead(t *testing.T) {
	vhciRingReset()

	vhciRingPush([]byte{1, 2, 3, 4, 5, 6, 7, 8})

	buf := make([]byte, 3)
	if n := vhciRead(buf); n != 3 {
		t.Fatalf("Read(3) = %d, want 3", n)
	}
	if string(buf) != string([]byte{1, 2, 3}) {
		t.Errorf("first chunk = %v, want [1 2 3]", buf)
	}
	if got := vhciBuffered(); got != 5 {
		t.Errorf("Buffered() after partial read = %d, want 5", got)
	}
	if rest := drain(t); string(rest) != string([]byte{4, 5, 6, 7, 8}) {
		t.Errorf("remainder = %v, want [4 5 6 7 8]", rest)
	}
}

// Wraparound is the case most likely to be silently wrong: head and tail are
// advanced modulo the buffer size independently, so a payload straddling the
// end of the backing array must still come back in order, and Buffered() must
// stay correct while head < tail.
func TestVHCIRingWraparound(t *testing.T) {
	vhciRingReset()
	capacity := vhciRingCapacity()

	// Park head/tail near the end of the backing array.
	filler := make([]byte, capacity-4)
	vhciRingPush(filler)
	if n := len(drain(t)); n != capacity-4 {
		t.Fatalf("drained %d filler bytes, want %d", n, capacity-4)
	}

	// This payload straddles the wrap point.
	payload := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22}
	if n := vhciRingPush(payload); n != len(payload) {
		t.Fatalf("push across wrap stored %d, want %d", n, len(payload))
	}
	if got := vhciBuffered(); got != len(payload) {
		t.Errorf("Buffered() across wrap = %d, want %d", got, len(payload))
	}
	if got := drain(t); string(got) != string(payload) {
		t.Errorf("read across wrap = %v, want %v", got, payload)
	}
}

func TestVHCIRingReadByteAcrossWrap(t *testing.T) {
	vhciRingReset()
	capacity := vhciRingCapacity()

	vhciRingPush(make([]byte, capacity-2))
	drain(t)

	payload := []byte{0x10, 0x20, 0x30, 0x40}
	vhciRingPush(payload)
	for i, want := range payload {
		if got := vhciReadByte(); got != int(want) {
			t.Fatalf("byte %d across wrap = %d, want %d", i, got, want)
		}
	}
}

// head == tail encodes "empty", so one slot is unusable and the ring holds
// capacity() bytes, not the full backing array.
func TestVHCIRingFillToCapacity(t *testing.T) {
	vhciRingReset()
	capacity := vhciRingCapacity()

	data := make([]byte, capacity)
	for i := range data {
		data[i] = byte(i)
	}
	if n := vhciRingPush(data); n != capacity {
		t.Fatalf("push of exactly capacity stored %d, want %d", n, capacity)
	}
	if got := vhciBuffered(); got != capacity {
		t.Errorf("Buffered() at capacity = %d, want %d", got, capacity)
	}
	if n := vhciRingPush([]byte{0x99}); n != 0 {
		t.Errorf("push into full ring stored %d, want 0", n)
	}

	got := drain(t)
	if len(got) != capacity {
		t.Fatalf("drained %d bytes, want %d", len(got), capacity)
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("byte %d = %d, want %d", i, got[i], byte(i))
		}
	}
}

// Documents a real limitation rather than asserting desired behaviour: the ring
// is byte-oriented and has no packet framing, so an overflowing push commits a
// prefix of the HCI packet and silently drops the rest. A host parser reading
// the stream will then consume a truncated event and desync. If VHCI overflow
// ever shows up in practice, the fix is to drop the whole packet here rather
// than half of it.
func TestVHCIRingOverflowTruncatesPacket(t *testing.T) {
	vhciRingReset()
	capacity := vhciRingCapacity()

	vhciRingPush(make([]byte, capacity-3))

	pkt := []byte{0x04, 0x0E, 0x04, 0x01, 0x03, 0x0C, 0x00}
	stored := vhciRingPush(pkt)
	if stored != 3 {
		t.Fatalf("overflowing push stored %d bytes, want 3", stored)
	}
	if got := vhciBuffered(); got != capacity {
		t.Errorf("Buffered() = %d, want %d (ring should be full)", got, capacity)
	}

	got := drain(t)
	tail := got[len(got)-3:]
	if string(tail) != string(pkt[:3]) {
		t.Errorf("truncated packet prefix = %v, want %v", tail, pkt[:3])
	}
}
