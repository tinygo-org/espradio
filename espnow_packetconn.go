//go:build esp32c3 || esp32c3_qemu_target || esp32s3 || esp32

package espradio

/*
#cgo CFLAGS: -Iblobs/include
#cgo CFLAGS: -Iblobs/include/local
#cgo CFLAGS: -Iblobs/headers
#cgo CFLAGS: -DCONFIG_SOC_WIFI_NAN_SUPPORT=0
#cgo CFLAGS: -DESPRADIO_PHY_PATCH_ROMFUNCS=0
#cgo CFLAGS: -fno-short-enums

#include "espradio.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"
)

const espNowPeerQueueDepth = 8

var (
	// ErrESPNowClosed reports use of a closed ESP-NOW manager or peer.
	ErrESPNowClosed = net.ErrClosed
	// ErrESPNowPacketTooLarge reports an attempt to send a payload larger than
	// the maximum supported ESP-NOW payload size.
	ErrESPNowPacketTooLarge = errors.New("espradio: esp-now payload too large")
	// ErrESPNowPeerActive reports that a second managed ESP-NOW instance was
	// created while another one is still active.
	ErrESPNowPeerActive = errors.New("espradio: esp-now manager already active")
	// ErrESPNowPeerMismatch reports that WriteTo was asked to send to an address
	// other than the peer's configured remote address.
	ErrESPNowPeerMismatch = errors.New("espradio: packet destination does not match peer")
	// ErrESPNowAddrType reports that a net.Addr value is not an ESPNowAddr.
	ErrESPNowAddrType = errors.New("espradio: expected ESP-NOW address")
)

// ESPNowAddr is a 6-byte ESP-NOW MAC address and implements net.Addr.
//
// The Network method returns "espnow". The String method formats the address
// as lower-case hexadecimal separated by colons.
type ESPNowAddr [ESPNowAddressLength]byte

func (a ESPNowAddr) Network() string { return "espnow" }

func (a ESPNowAddr) String() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", a[0], a[1], a[2], a[3], a[4], a[5])
}

func (a ESPNowAddr) IsBroadcast() bool {
	return a == ESPNowBroadcastAddr
}

// ESPNowBroadcastAddr is the broadcast destination FF:FF:FF:FF:FF:FF.
var ESPNowBroadcastAddr = ESPNowAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

// ESPNowConfig configures a managed ESP-NOW instance created by NewESPNow.
//
// If PrimaryMasterKey is non-nil, it is installed before any peers are added.
type ESPNowConfig struct {
	PrimaryMasterKey *[ESPNowKeyLength]byte
}

// PeerConfig describes one remote ESP-NOW peer.
//
// Address is required. If If is zero, station mode is used by default.
// Channel may be zero to use the current Wi-Fi channel. For encrypted peers,
// set Encrypt and provide a 16-byte Key.
type PeerConfig struct {
	Address ESPNowAddr
	Key     *[ESPNowKeyLength]byte
	Channel uint8
	If      WiFiInterface
	Encrypt bool
}

// ESPNow is a higher-level managed ESP-NOW wrapper.
//
// It owns the underlying global ESP-NOW initialization, maintains the SDK peer
// table for peers added through AddPeer, and routes incoming packets into Peer
// instances that implement net.PacketConn.
//
// Only one managed ESPNow instance may be active at a time.
type ESPNow struct {
	mu            sync.RWMutex
	closed        bool
	peers         map[ESPNowAddr]*Peer
	broadcastPeer *Peer
}

// Peer represents one remote ESP-NOW destination and implements net.PacketConn.
//
// The abstraction is packet-oriented, not stream-oriented: each WriteTo sends
// one ESP-NOW frame, and each ReadFrom returns at most one received frame.
// Incoming packets are routed by source address, so a peer created for address
// A receives packets whose source MAC is A.
type Peer struct {
	now *ESPNow

	mu            sync.RWMutex
	addr          ESPNowAddr
	iface         WiFiInterface
	localAddr     ESPNowAddr
	closed        bool
	readDeadline  time.Time
	writeDeadline time.Time
	rx            chan espNowInboundPacket
	done          chan struct{}
}

type espNowInboundPacket struct {
	src  ESPNowAddr
	dst  ESPNowAddr
	data []byte
}

var _ net.PacketConn = (*Peer)(nil)

// NewESPNow initializes ESP-NOW and returns a managed wrapper.
//
// The caller must have already enabled and started Wi-Fi before creating the
// managed instance. Close must be called when finished to release the global
// ESP-NOW state.
//
// Only one managed ESPNow instance may exist at a time; attempting to create a
// second one returns ErrESPNowPeerActive.
func NewESPNow(cfg ESPNowConfig) (*ESPNow, error) {
	if err := ESPNowInit(); err != nil {
		return nil, err
	}
	if cfg.PrimaryMasterKey != nil {
		if err := ESPNowSetPrimaryMasterKey(*cfg.PrimaryMasterKey); err != nil {
			_ = ESPNowDeinit()
			return nil, err
		}
	}

	now := &ESPNow{
		peers: make(map[ESPNowAddr]*Peer),
	}

	espNowMu.Lock()
	defer espNowMu.Unlock()
	if activeManagedESPNow != nil {
		_ = ESPNowDeinit()
		return nil, ErrESPNowPeerActive
	}
	activeManagedESPNow = now

	return now, nil
}

// Close deinitializes the managed ESP-NOW instance and closes all peers created
// through it.
//
// Close is idempotent. After Close, all peer operations return ErrESPNowClosed.
func (n *ESPNow) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	peers := make([]*Peer, 0, len(n.peers))
	for _, peer := range n.peers {
		peers = append(peers, peer)
	}
	n.peers = nil
	n.broadcastPeer = nil
	n.mu.Unlock()

	for _, peer := range peers {
		peer.closeLocal()
	}

	espNowMu.Lock()
	if activeManagedESPNow == n {
		activeManagedESPNow = nil
	}
	espNowMu.Unlock()

	return ESPNowDeinit()
}

// AddPeer adds a remote peer to the SDK peer table and returns a Peer that
// implements net.PacketConn.
//
// If the peer already exists in this managed instance, the existing Peer is
// returned. Broadcast may be added either by calling Broadcast or by passing
// ESPNowBroadcastAddr as the peer address.
func (n *ESPNow) AddPeer(cfg PeerConfig) (*Peer, error) {
	if cfg.Address.IsBroadcast() {
		return n.broadcast()
	}
	if cfg.If == 0 {
		cfg.If = WiFiInterfaceSTA
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, ErrESPNowClosed
	}
	if peer := n.peers[cfg.Address]; peer != nil {
		return peer, nil
	}

	raw := ESPNowPeer{
		Address: [ESPNowAddressLength]byte(cfg.Address),
		Channel: cfg.Channel,
		If:      cfg.If,
		Encrypt: cfg.Encrypt,
	}
	if cfg.Key != nil {
		raw.Key = *cfg.Key
	}
	if err := ESPNowAddPeer(raw); err != nil {
		return nil, err
	}

	peer, err := n.newPeer(cfg.Address, cfg.If)
	if err != nil {
		_ = ESPNowDeletePeer(raw.Address)
		return nil, err
	}
	n.peers[cfg.Address] = peer
	return peer, nil
}

// Peer looks up a previously added peer by MAC address.
func (n *ESPNow) Peer(addr ESPNowAddr) (*Peer, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	peer, ok := n.peers[addr]
	return peer, ok
}

// Broadcast returns a Peer bound to the broadcast address
// FF:FF:FF:FF:FF:FF.
//
// Writes sent through the returned peer are broadcast. Reads from it receive
// incoming packets whose destination address is broadcast, regardless of which
// source peer sent them.
func (n *ESPNow) Broadcast() (*Peer, error) {
	return n.broadcast()
}

func (n *ESPNow) broadcast() (*Peer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, ErrESPNowClosed
	}
	if n.broadcastPeer != nil {
		return n.broadcastPeer, nil
	}

	raw := ESPNowPeer{
		Address: [ESPNowAddressLength]byte(ESPNowBroadcastAddr),
		If:      WiFiInterfaceSTA,
	}
	if !ESPNowPeerExists(raw.Address) {
		if err := ESPNowAddPeer(raw); err != nil {
			return nil, err
		}
	}

	peer, err := n.newPeer(ESPNowBroadcastAddr, WiFiInterfaceSTA)
	if err != nil {
		return nil, err
	}
	n.peers[ESPNowBroadcastAddr] = peer
	n.broadcastPeer = peer
	return peer, nil
}

func (n *ESPNow) newPeer(addr ESPNowAddr, iface WiFiInterface) (*Peer, error) {
	local, err := currentESPNowMAC(iface)
	if err != nil {
		return nil, err
	}
	return &Peer{
		now:       n,
		addr:      addr,
		iface:     iface,
		localAddr: local,
		rx:        make(chan espNowInboundPacket, espNowPeerQueueDepth),
		done:      make(chan struct{}),
	}, nil
}

func (n *ESPNow) handleReceive(event ESPNowReceive) {
	src := ESPNowAddr(event.SourceAddress)
	dst := ESPNowAddr(event.DestinationAddress)

	n.mu.RLock()
	if n.closed {
		n.mu.RUnlock()
		return
	}
	peer := n.peers[src]
	broadcast := n.broadcastPeer
	n.mu.RUnlock()

	if peer != nil {
		peer.enqueue(espNowInboundPacket{
			src:  src,
			dst:  dst,
			data: append([]byte(nil), event.Data...),
		})
	}
	if dst.IsBroadcast() && broadcast != nil && broadcast != peer {
		broadcast.enqueue(espNowInboundPacket{
			src:  src,
			dst:  dst,
			data: append([]byte(nil), event.Data...),
		})
	}
}

func (n *ESPNow) handleSend(ESPNowSendReport) {
}

// Addr returns the peer's configured remote address.
func (p *Peer) Addr() ESPNowAddr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.addr
}

// LocalESPNowAddr returns the local MAC address used by this peer's interface.
func (p *Peer) LocalESPNowAddr() ESPNowAddr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.localAddr
}

// Send sends one payload to the peer's configured remote address.
//
// It is a convenience wrapper around WriteTo(payload, nil).
func (p *Peer) Send(payload []byte) (int, error) {
	return p.WriteTo(payload, p.addr)
}

// ReadPacket allocates a maximum-size ESP-NOW buffer, reads one packet, and
// returns the payload and sender address.
func (p *Peer) ReadPacket() ([]byte, net.Addr, error) {
	buf := make([]byte, ESPNowMaxDataLength)
	n, addr, err := p.ReadFrom(buf)
	if err != nil {
		return nil, addr, err
	}
	return buf[:n], addr, nil
}

// ReadFrom waits for one received packet for this peer.
//
// The returned address is the sender's ESP-NOW address. Deadlines are honored
// using SetReadDeadline or SetDeadline. If the peer is closed while ReadFrom is
// blocked, it returns ErrESPNowClosed.
//
// If buf is smaller than the received packet, the payload is truncated and
// ReadFrom returns io.ErrShortBuffer.
func (p *Peer) ReadFrom(buf []byte) (int, net.Addr, error) {
	if err := p.checkClosed(); err != nil {
		return 0, nil, err
	}

	deadline := p.getReadDeadline()
	var timer <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, nil, os.ErrDeadlineExceeded
		}
		timer = time.After(d)
	}

	select {
	case pkt, ok := <-p.rx:
		if ok {
			if len(pkt.data) > len(buf) {
				copy(buf, pkt.data[:len(buf)])
				return len(buf), pkt.src, io.ErrShortBuffer
			}
			copy(buf, pkt.data)
			return len(pkt.data), pkt.src, nil
		}
		return 0, nil, ErrESPNowClosed
	case <-p.done:
		return 0, nil, ErrESPNowClosed
	case <-timer:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

// WriteTo sends one ESP-NOW payload.
//
// The destination address must be nil or equal to the peer's configured remote
// address. Each successful call sends exactly one ESP-NOW frame. Payloads
// larger than ESPNowMaxDataLength return ErrESPNowPacketTooLarge.
//
// Write deadlines are checked before the frame is queued into the SDK.
func (p *Peer) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if err := p.checkClosed(); err != nil {
		return 0, err
	}
	if len(payload) > ESPNowMaxDataLength {
		return 0, ErrESPNowPacketTooLarge
	}
	if deadline := p.getWriteDeadline(); !deadline.IsZero() && time.Now().After(deadline) {
		return 0, os.ErrDeadlineExceeded
	}

	dest, err := p.resolveWriteAddr(addr)
	if err != nil {
		return 0, err
	}

	raw := [ESPNowAddressLength]byte(dest)
	if err := ESPNowSend(&raw, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

// Close closes the peer and removes it from the SDK peer table.
//
// Close is idempotent. Closing a peer does not close the owning ESPNow
// instance, but any blocked reads on the peer are unblocked with
// ErrESPNowClosed.
func (p *Peer) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.now.mu.Lock()
	if p.now.peers != nil {
		delete(p.now.peers, p.addr)
		if p.now.broadcastPeer == p {
			p.now.broadcastPeer = nil
		}
	}
	p.now.mu.Unlock()

	p.closeLocal()
	return ESPNowDeletePeer([ESPNowAddressLength]byte(p.addr))
}

// LocalAddr returns the local address used by this peer and satisfies
// net.PacketConn.
func (p *Peer) LocalAddr() net.Addr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.localAddr
}

// SetDeadline sets both the read and write deadlines for the peer.
func (p *Peer) SetDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readDeadline = t
	p.writeDeadline = t
	return nil
}

// SetReadDeadline sets the deadline for future ReadFrom calls.
func (p *Peer) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readDeadline = t
	return nil
}

// SetWriteDeadline sets the deadline checked by future WriteTo calls.
func (p *Peer) SetWriteDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeDeadline = t
	return nil
}

func (p *Peer) enqueue(pkt espNowInboundPacket) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return
	}
	ch := p.rx
	p.mu.RUnlock()

	select {
	case ch <- pkt:
	default:
	}
}

func (p *Peer) closeLocal() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	done := p.done
	p.mu.Unlock()
	close(done)
}

func (p *Peer) checkClosed() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrESPNowClosed
	}
	return nil
}

func (p *Peer) getReadDeadline() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.readDeadline
}

func (p *Peer) getWriteDeadline() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.writeDeadline
}

func (p *Peer) resolveWriteAddr(addr net.Addr) (ESPNowAddr, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if addr == nil {
		return p.addr, nil
	}
	dest, err := parseESPNowAddr(addr)
	if err != nil {
		return ESPNowAddr{}, err
	}
	if dest != p.addr {
		return ESPNowAddr{}, ErrESPNowPeerMismatch
	}
	return dest, nil
}

func parseESPNowAddr(addr net.Addr) (ESPNowAddr, error) {
	switch v := addr.(type) {
	case ESPNowAddr:
		return v, nil
	case *ESPNowAddr:
		if v == nil {
			return ESPNowAddr{}, ErrESPNowAddrType
		}
		return *v, nil
	default:
		return ESPNowAddr{}, ErrESPNowAddrType
	}
}

func currentESPNowMAC(iface WiFiInterface) (ESPNowAddr, error) {
	var mac ESPNowAddr
	code := C.esp_wifi_get_mac(C.wifi_interface_t(iface), (*C.uint8_t)(unsafe.Pointer(&mac[0])))
	if code != C.ESP_OK {
		return ESPNowAddr{}, makeError(code)
	}
	return mac, nil
}
