//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

import (
	"bytes"
	"io"
	"sync"
)

// PeerStream adapts a packet-oriented Peer into a stream-like io.ReadWriter.
//
// Read buffers received ESP-NOW packets internally and presents them as a
// continuous byte stream. Packet boundaries are not preserved: one Read may
// return data from part of a packet, a whole packet, or multiple packets.
//
// Write splits large writes into multiple ESP-NOW packets as needed. Each
// packet is sent in order using the wrapped peer.
//
// This adapter is intentionally lossy with respect to packet framing. Use Peer
// directly when application-level message boundaries matter.
type PeerStream struct {
	peer *Peer

	readMu  sync.Mutex
	writeMu sync.Mutex
	readBuf bytes.Buffer
}

var _ io.ReadWriter = (*PeerStream)(nil)

// NewPeerStream returns a stream-like adapter around a packet-oriented Peer.
func NewPeerStream(peer *Peer) *PeerStream {
	return &PeerStream{peer: peer}
}

// Stream returns a stream-like adapter for the peer.
//
// The returned adapter buffers incoming packets and fragments large writes into
// multiple ESP-NOW frames. Packet boundaries are not preserved.
func (p *Peer) Stream() *PeerStream {
	return NewPeerStream(p)
}

// Read reads from the adapter's internal buffer, refilling it from received
// ESP-NOW packets as needed.
//
// Unlike bytes.Buffer.Read, this method does not surface io.EOF merely because
// the current internal buffer is empty. Instead it blocks waiting for the next
// packet from the wrapped peer. Errors returned by the wrapped peer, including
// deadline and close errors, are returned directly.
func (s *PeerStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	s.readMu.Lock()
	defer s.readMu.Unlock()

	for s.readBuf.Len() == 0 {
		pkt, _, err := s.peer.ReadPacket()
		if err != nil {
			return 0, err
		}
		if len(pkt) == 0 {
			continue
		}
		_, _ = s.readBuf.Write(pkt)
	}

	n, err := s.readBuf.Read(p)
	if err == io.EOF {
		return n, nil
	}
	return n, err
}

// Write writes a byte stream to the wrapped peer.
//
// Large writes are split into multiple ESP-NOW packets of at most
// ESPNowMaxDataLength bytes each and sent sequentially.
func (s *PeerStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	written := 0
	for written < len(p) {
		end := written + ESPNowMaxDataLength
		if end > len(p) {
			end = len(p)
		}
		n, err := s.peer.Send(p[written:end])
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
