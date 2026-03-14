package espradio

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// constants
// ---------------------------------------------------------------------------

const (
	_SOCK_STREAM = 1
	_SOCK_DGRAM  = 2

	tcpRxBufSize = 4096
	tcpWindowSz  = 4096
	maxSocks     = 8
	ephBase      = 49152

	_FIN byte = 0x01
	_SYN byte = 0x02
	_RST byte = 0x04
	_PSH byte = 0x08
	_ACK byte = 0x10

	_TCP_CLOSED      = 0
	_TCP_SYN_SENT    = 1
	_TCP_ESTABLISHED = 2
	_TCP_FIN_WAIT1   = 3
	_TCP_FIN_WAIT2   = 4
	_TCP_CLOSE_WAIT  = 5
	_TCP_LAST_ACK    = 6
	_TCP_TIME_WAIT   = 7

	etIPv4 uint16 = 0x0800
	etARP  uint16 = 0x0806
)

var (
	errBadSock     = errors.New("bad socket")
	errConnRefused = errors.New("connection refused")
	errConnReset   = errors.New("connection reset")
	errTimeout     = errors.New("timeout")
	errDHCPFail    = errors.New("DHCP failed")
	errNoRoute     = errors.New("no route")
	errDNSFail     = errors.New("DNS failed")
	errClosed      = errors.New("closed")
	errNotImpl     = errors.New("not implemented")

	bcastMAC = [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	zeroIP4  = [4]byte{}
	bcastIP4 = [4]byte{255, 255, 255, 255}
)

// ---------------------------------------------------------------------------
// NetStack
// ---------------------------------------------------------------------------

type NetStackConfig struct {
	Debug bool
}

type NetStack struct {
	dev   L2Device
	mac   [6]byte
	debug bool

	ip   [4]byte
	gw   [4]byte
	mask [4]byte
	dns  [4]byte

	arpMu    sync.Mutex
	arpCache map[[4]byte][6]byte
	arpWait  map[[4]byte][]chan [6]byte

	mu       sync.Mutex
	socks    map[int]*sock
	nextFD   int
	nextPort uint16
	ipID     uint16
}

type sock struct {
	typ        int // _SOCK_STREAM or _SOCK_DGRAM
	localPort  uint16
	remoteIP   [4]byte
	remotePort uint16

	// TCP
	state    int
	seq      uint32
	ack      uint32
	rxBuf    []byte
	rxOff    int
	rxLen    int
	rxMu     sync.Mutex
	rxReady  chan struct{}
	connDone chan error

	// UDP
	udpRx chan []byte

	closed bool
}

// NewNetStack creates a TCP/IP stack on top of an L2Device.
// It runs DHCP to obtain an IP address before returning.
func NewNetStack(dev L2Device, cfg ...NetStackConfig) (*NetStack, error) {
	var debug bool
	if len(cfg) > 0 {
		debug = cfg[0].Debug
	}
	ns := &NetStack{
		dev:      dev,
		debug:    debug,
		arpCache: make(map[[4]byte][6]byte),
		arpWait:  make(map[[4]byte][]chan [6]byte),
		socks:    make(map[int]*sock),
		nextFD:   1,
		nextPort: ephBase,
	}
	mac, err := dev.HardwareAddr()
	if err != nil {
		return nil, err
	}
	ns.mac = mac
	ns.dbg("starting rxLoop")
	go ns.rxLoop()

	ns.dbg("starting DHCP")
	if err := ns.dhcp(); err != nil {
		return nil, err
	}
	return ns, nil
}

func (ns *NetStack) dbg(msg string) {
	if ns.debug {
		println("netstack:", msg)
	}
}

// IP returns the IP address obtained via DHCP.
func (ns *NetStack) IP() [4]byte { return ns.ip }

// GW returns the gateway address.
func (ns *NetStack) GW() [4]byte { return ns.gw }

// DNS returns the DNS server address.
func (ns *NetStack) DNS() [4]byte { return ns.dns }

func (ns *NetStack) allocPort() uint16 {
	ns.mu.Lock()
	p := ns.nextPort
	ns.nextPort++
	if ns.nextPort == 0 {
		ns.nextPort = ephBase
	}
	ns.mu.Unlock()
	return p
}

func (ns *NetStack) getSock(fd int) *sock {
	ns.mu.Lock()
	s := ns.socks[fd]
	ns.mu.Unlock()
	return s
}

// ---------------------------------------------------------------------------
// Frame receive loop
// ---------------------------------------------------------------------------

func (ns *NetStack) rxLoop() {
	ns.dbg("rxLoop: started")
	for frame := range ns.dev.RecvCh() {
		if len(frame) < 14 {
			continue
		}
		et := binary.BigEndian.Uint16(frame[12:14])
		if ns.debug {
			println("rxLoop: frame len=", len(frame), "ethertype=", et)
		}
		switch et {
		case etARP:
			ns.handleARP(frame[14:])
		case etIPv4:
			ns.handleIP(frame[14:])
		}
	}
	ns.dbg("rxLoop: channel closed")
}

// ---------------------------------------------------------------------------
// ARP
// ---------------------------------------------------------------------------

func (ns *NetStack) handleARP(d []byte) {
	if len(d) < 28 {
		return
	}
	op := binary.BigEndian.Uint16(d[6:8])
	var sMAC [6]byte
	var sIP, tIP [4]byte
	copy(sMAC[:], d[8:14])
	copy(sIP[:], d[14:18])
	copy(tIP[:], d[24:28])

	ns.arpMu.Lock()
	ns.arpCache[sIP] = sMAC
	waiters := ns.arpWait[sIP]
	delete(ns.arpWait, sIP)
	ns.arpMu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- sMAC:
		default:
		}
	}

	if op == 1 && tIP == ns.ip && ns.ip != zeroIP4 {
		ns.sendARP(2, sIP, sMAC)
	}
}

func (ns *NetStack) sendARP(op uint16, dstIP [4]byte, dstMAC [6]byte) {
	pkt := make([]byte, 42)
	copy(pkt[0:6], dstMAC[:])
	copy(pkt[6:12], ns.mac[:])
	binary.BigEndian.PutUint16(pkt[12:14], etARP)

	a := pkt[14:]
	binary.BigEndian.PutUint16(a[0:2], 1) // hw type
	binary.BigEndian.PutUint16(a[2:4], etIPv4)
	a[4] = 6
	a[5] = 4
	binary.BigEndian.PutUint16(a[6:8], op)
	copy(a[8:14], ns.mac[:])
	copy(a[14:18], ns.ip[:])
	copy(a[18:24], dstMAC[:])
	copy(a[24:28], dstIP[:])
	ns.dev.SendEth(pkt)
}

func (ns *NetStack) arpResolve(ip [4]byte) ([6]byte, error) {
	ns.arpMu.Lock()
	if mac, ok := ns.arpCache[ip]; ok {
		ns.arpMu.Unlock()
		return mac, nil
	}
	ch := make(chan [6]byte, 1)
	ns.arpWait[ip] = append(ns.arpWait[ip], ch)
	ns.arpMu.Unlock()

	for try := 0; try < 3; try++ {
		ns.sendARP(1, ip, bcastMAC)
		select {
		case mac := <-ch:
			return mac, nil
		case <-time.After(time.Second):
		}
	}
	return [6]byte{}, errNoRoute
}

func (ns *NetStack) isLocal(ip [4]byte) bool {
	for i := range ip {
		if (ip[i] & ns.mask[i]) != (ns.ip[i] & ns.mask[i]) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// IP
// ---------------------------------------------------------------------------

func (ns *NetStack) handleIP(d []byte) {
	if len(d) < 20 {
		return
	}
	ihl := int(d[0]&0x0f) * 4
	if len(d) < ihl {
		return
	}
	var srcIP, dstIP [4]byte
	copy(srcIP[:], d[12:16])
	copy(dstIP[:], d[16:20])
	proto := d[9]
	if ns.debug {
		println("handleIP: src=", fmtI(srcIP), "dst=", fmtI(dstIP), "proto=", proto)
	}

	mine := dstIP == ns.ip || dstIP == bcastIP4 || dstIP == zeroIP4
	// During DHCP (ip==0.0.0.0), also accept unicast UDP to any IP
	// (some DHCP servers send Offer to the offered IP, ignoring broadcast flag)
	if !mine && ns.ip == zeroIP4 && proto == 17 {
		mine = true
		ns.dbg("handleIP: accepting during DHCP")
	}
	if !mine {
		ns.dbg("handleIP: dropped")
		return
	}

	payload := d[ihl:]
	switch proto {
	case 6:
		ns.handleTCP(srcIP, payload)
	case 17:
		ns.handleUDP(srcIP, payload)
	}
}

func (ns *NetStack) sendIP(dstIP [4]byte, proto byte, payload []byte) error {
	nextHop := dstIP
	if !ns.isLocal(dstIP) {
		nextHop = ns.gw
	}
	dstMAC, err := ns.arpResolve(nextHop)
	if err != nil {
		return err
	}
	return ns.sendIPWith(dstMAC, ns.ip, dstIP, proto, payload)
}

func (ns *NetStack) sendIPBcast(proto byte, payload []byte) error {
	return ns.sendIPWith(bcastMAC, zeroIP4, bcastIP4, proto, payload)
}

func (ns *NetStack) sendIPWith(dstMAC [6]byte, srcIP, dstIP [4]byte, proto byte, payload []byte) error {
	tl := 20 + len(payload)
	pkt := make([]byte, 14+tl)
	copy(pkt[0:6], dstMAC[:])
	copy(pkt[6:12], ns.mac[:])
	binary.BigEndian.PutUint16(pkt[12:14], etIPv4)

	ip := pkt[14:]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(tl))
	ns.ipID++
	binary.BigEndian.PutUint16(ip[4:6], ns.ipID)
	ip[6] = 0x40 // DF
	ip[8] = 64
	ip[9] = proto
	copy(ip[12:16], srcIP[:])
	copy(ip[16:20], dstIP[:])
	binary.BigEndian.PutUint16(ip[10:12], ipCsum(ip[:20]))
	copy(ip[20:], payload)
	return ns.dev.SendEth(pkt)
}

// ---------------------------------------------------------------------------
// UDP
// ---------------------------------------------------------------------------

func (ns *NetStack) handleUDP(srcIP [4]byte, d []byte) {
	if len(d) < 8 {
		return
	}
	srcPort := binary.BigEndian.Uint16(d[0:2])
	dstPort := binary.BigEndian.Uint16(d[2:4])
	udpLen := binary.BigEndian.Uint16(d[4:6])
	if ns.debug {
		println("handleUDP: src=", srcPort, "dst=", dstPort, "len=", udpLen)
	}
	if int(udpLen) > len(d) {
		return
	}
	payload := d[8:udpLen]
	_ = srcPort

	ns.mu.Lock()
	for _, s := range ns.socks {
		if s.typ == _SOCK_DGRAM && s.localPort == dstPort && !s.closed {
			data := make([]byte, len(payload))
			copy(data, payload)
			select {
			case s.udpRx <- data:
			default:
			}
			ns.mu.Unlock()
			return
		}
	}
	ns.mu.Unlock()
}

func (ns *NetStack) sendUDP(dstIP [4]byte, srcPort, dstPort uint16, data []byte) error {
	udp := make([]byte, 8+len(data))
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(8+len(data)))
	// checksum=0 is valid for IPv4 UDP
	copy(udp[8:], data)
	return ns.sendIP(dstIP, 17, udp)
}

// ---------------------------------------------------------------------------
// TCP
// ---------------------------------------------------------------------------

func (ns *NetStack) handleTCP(srcIP [4]byte, d []byte) {
	if len(d) < 20 {
		return
	}
	srcPort := binary.BigEndian.Uint16(d[0:2])
	dstPort := binary.BigEndian.Uint16(d[2:4])
	seq := binary.BigEndian.Uint32(d[4:8])
	ackN := binary.BigEndian.Uint32(d[8:12])
	dataOff := int(d[12]>>4) * 4
	flags := d[13]

	var payload []byte
	if dataOff < len(d) {
		payload = d[dataOff:]
	}

	ns.mu.Lock()
	var s *sock
	for _, cs := range ns.socks {
		if cs.typ == _SOCK_STREAM && cs.localPort == dstPort &&
			cs.remoteIP == srcIP && cs.remotePort == srcPort {
			s = cs
			break
		}
	}
	ns.mu.Unlock()
	if s == nil {
		ns.sendTCPRst(srcIP, srcPort, dstPort, ackN, seq+uint32(len(payload))+boolU32(flags&_SYN != 0)+boolU32(flags&_FIN != 0))
		return
	}

	switch s.state {
	case _TCP_SYN_SENT:
		if flags&_RST != 0 {
			s.state = _TCP_CLOSED
			trySend(s.connDone, errConnRefused)
			return
		}
		if flags&_SYN != 0 && flags&_ACK != 0 {
			s.ack = seq + 1
			s.seq = ackN
			s.state = _TCP_ESTABLISHED
			ns.sendTCPFlags(s, _ACK, nil)
			trySend(s.connDone, nil)
		}

	case _TCP_ESTABLISHED:
		if flags&_RST != 0 {
			s.state = _TCP_CLOSED
			s.closed = true
			notifyRx(s)
			return
		}
		if flags&_ACK != 0 {
			s.seq = ackN
		}
		if len(payload) > 0 {
			s.rxMu.Lock()
			n := s.rxWrite(payload)
			_ = n
			s.rxMu.Unlock()
			s.ack = seq + uint32(len(payload))
			ns.sendTCPFlags(s, _ACK, nil)
			notifyRx(s)
		}
		if flags&_FIN != 0 {
			s.ack = seq + uint32(len(payload)) + 1
			s.state = _TCP_CLOSE_WAIT
			ns.sendTCPFlags(s, _ACK, nil)
			s.closed = true
			notifyRx(s)
		}

	case _TCP_FIN_WAIT1:
		if flags&_ACK != 0 && flags&_FIN != 0 {
			s.ack = seq + 1
			s.state = _TCP_TIME_WAIT
			ns.sendTCPFlags(s, _ACK, nil)
		} else if flags&_ACK != 0 {
			s.state = _TCP_FIN_WAIT2
		} else if flags&_FIN != 0 {
			s.ack = seq + 1
			ns.sendTCPFlags(s, _ACK, nil)
			s.state = _TCP_TIME_WAIT
		}

	case _TCP_FIN_WAIT2:
		if flags&_FIN != 0 {
			s.ack = seq + 1
			ns.sendTCPFlags(s, _ACK, nil)
			s.state = _TCP_TIME_WAIT
		}

	case _TCP_LAST_ACK:
		if flags&_ACK != 0 {
			s.state = _TCP_CLOSED
		}
	}
}

func (ns *NetStack) sendTCPFlags(s *sock, flags byte, data []byte) {
	hdrLen := 20
	seg := make([]byte, hdrLen+len(data))
	binary.BigEndian.PutUint16(seg[0:2], s.localPort)
	binary.BigEndian.PutUint16(seg[2:4], s.remotePort)
	binary.BigEndian.PutUint32(seg[4:8], s.seq)
	binary.BigEndian.PutUint32(seg[8:12], s.ack)
	seg[12] = byte(hdrLen/4) << 4
	seg[13] = flags
	binary.BigEndian.PutUint16(seg[14:16], tcpWindowSz)
	if len(data) > 0 {
		copy(seg[20:], data)
	}
	binary.BigEndian.PutUint16(seg[16:18], tcpCsum(ns.ip, s.remoteIP, seg))
	ns.sendIP(s.remoteIP, 6, seg)
	if flags&(_SYN|_FIN) != 0 {
		s.seq++
	}
	s.seq += uint32(len(data))
}

func (ns *NetStack) sendTCPRst(dstIP [4]byte, dstPort, srcPort uint16, seq, ack uint32) {
	seg := make([]byte, 20)
	binary.BigEndian.PutUint16(seg[0:2], srcPort)
	binary.BigEndian.PutUint16(seg[2:4], dstPort)
	binary.BigEndian.PutUint32(seg[4:8], seq)
	binary.BigEndian.PutUint32(seg[8:12], ack)
	seg[12] = 0x50
	seg[13] = _RST | _ACK
	binary.BigEndian.PutUint16(seg[16:18], tcpCsum(ns.ip, dstIP, seg))
	ns.sendIP(dstIP, 6, seg)
}

func (s *sock) rxWrite(data []byte) int {
	cap_ := len(s.rxBuf)
	space := cap_ - s.rxLen
	n := len(data)
	if n > space {
		n = space
	}
	if n == 0 {
		return 0
	}
	wp := (s.rxOff + s.rxLen) % cap_
	first := cap_ - wp
	if first > n {
		first = n
	}
	copy(s.rxBuf[wp:wp+first], data[:first])
	if n > first {
		copy(s.rxBuf[:n-first], data[first:n])
	}
	s.rxLen += n
	return n
}

func (s *sock) rxRead(buf []byte) int {
	s.rxMu.Lock()
	defer s.rxMu.Unlock()
	n := len(buf)
	if n > s.rxLen {
		n = s.rxLen
	}
	if n == 0 {
		return 0
	}
	cap_ := len(s.rxBuf)
	first := cap_ - s.rxOff
	if first > n {
		first = n
	}
	copy(buf[:first], s.rxBuf[s.rxOff:s.rxOff+first])
	if n > first {
		copy(buf[first:n], s.rxBuf[:n-first])
	}
	s.rxOff = (s.rxOff + n) % cap_
	s.rxLen -= n
	return n
}

func (s *sock) rxAvail() int {
	s.rxMu.Lock()
	n := s.rxLen
	s.rxMu.Unlock()
	return n
}

// ---------------------------------------------------------------------------
// DNS
// ---------------------------------------------------------------------------

func (ns *NetStack) dnsResolve(name string) ([4]byte, error) {
	port := ns.allocPort()
	ch := make(chan []byte, 2)

	ns.mu.Lock()
	fd := ns.nextFD
	ns.nextFD++
	ns.socks[fd] = &sock{typ: _SOCK_DGRAM, localPort: port, udpRx: ch}
	ns.mu.Unlock()
	defer func() {
		ns.mu.Lock()
		delete(ns.socks, fd)
		ns.mu.Unlock()
	}()

	qID := uint16(time.Now().UnixNano())
	query := buildDNSQuery(qID, name)
	if err := ns.sendUDP(ns.dns, port, 53, query); err != nil {
		return zeroIP4, err
	}

	select {
	case resp := <-ch:
		return parseDNSResponse(resp, qID)
	case <-time.After(5 * time.Second):
		return zeroIP4, errDNSFail
	}
}

func buildDNSQuery(id uint16, name string) []byte {
	// header(12) + question(name + 2 + 2)
	nameEnc := encodeDNSName(name)
	buf := make([]byte, 12+len(nameEnc)+4)
	binary.BigEndian.PutUint16(buf[0:2], id)
	buf[2] = 0x01 // RD
	buf[3] = 0x00
	binary.BigEndian.PutUint16(buf[4:6], 1) // QDCOUNT
	copy(buf[12:], nameEnc)
	off := 12 + len(nameEnc)
	binary.BigEndian.PutUint16(buf[off:off+2], 1)   // QTYPE A
	binary.BigEndian.PutUint16(buf[off+2:off+4], 1) // QCLASS IN
	return buf
}

func encodeDNSName(name string) []byte {
	var out []byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			label := name[start:i]
			out = append(out, byte(len(label)))
			out = append(out, label...)
			start = i + 1
		}
	}
	out = append(out, 0)
	return out
}

func parseDNSResponse(d []byte, wantID uint16) ([4]byte, error) {
	if len(d) < 12 {
		return zeroIP4, errDNSFail
	}
	id := binary.BigEndian.Uint16(d[0:2])
	if id != wantID {
		return zeroIP4, errDNSFail
	}
	anCount := binary.BigEndian.Uint16(d[6:8])
	off := 12
	// skip question
	for off < len(d) && d[off] != 0 {
		if d[off]&0xc0 == 0xc0 {
			off += 2
			goto pastQ
		}
		off += int(d[off]) + 1
	}
	off++ // null terminator
pastQ:
	off += 4 // QTYPE + QCLASS

	for i := uint16(0); i < anCount && off+12 <= len(d); i++ {
		// name (might be pointer)
		if d[off]&0xc0 == 0xc0 {
			off += 2
		} else {
			for off < len(d) && d[off] != 0 {
				off += int(d[off]) + 1
			}
			off++
		}
		if off+10 > len(d) {
			break
		}
		rtype := binary.BigEndian.Uint16(d[off : off+2])
		rdLen := binary.BigEndian.Uint16(d[off+8 : off+10])
		off += 10
		if rtype == 1 && rdLen == 4 && off+4 <= len(d) {
			var ip [4]byte
			copy(ip[:], d[off:off+4])
			return ip, nil
		}
		off += int(rdLen)
	}
	return zeroIP4, errDNSFail
}

// ---------------------------------------------------------------------------
// DHCP
// ---------------------------------------------------------------------------

func (ns *NetStack) dhcp() error {
	ns.dbg("dhcp: creating socket")
	ch := make(chan []byte, 4)
	ns.mu.Lock()
	fd := ns.nextFD
	ns.nextFD++
	ns.socks[fd] = &sock{typ: _SOCK_DGRAM, localPort: 68, udpRx: ch}
	ns.mu.Unlock()
	defer func() {
		ns.mu.Lock()
		delete(ns.socks, fd)
		ns.mu.Unlock()
	}()

	xid := uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	// Wait for rxLoop to start consuming frames
	time.Sleep(100 * time.Millisecond)

	var offeredIP, serverIP, gwIP, maskIP, dnsIP [4]byte
	for attempt := 0; attempt < 4; attempt++ {
		if ns.debug {
			println("dhcp: sending Discover attempt", attempt+1)
		}
		ns.sendDHCPMsg(xid, 1, zeroIP4, nil)

		select {
		case data := <-ch:
			offeredIP, serverIP, gwIP, maskIP, dnsIP = parseDHCPReply(data)
			if offeredIP != zeroIP4 {
				if ns.debug {
					println("dhcp: got Offer IP=", fmtI(offeredIP))
				}
				goto gotOffer
			}
			ns.dbg("dhcp: bad Offer, retrying")
		case <-time.After(3 * time.Second):
			ns.dbg("dhcp: timeout, retrying")
		}
	}
	return errDHCPFail

gotOffer:

	// Request
	ns.sendDHCPMsg(xid, 3, offeredIP, serverIP[:])

	select {
	case data := <-ch:
		ip, _, _, _, _ := parseDHCPReply(data)
		if ip == zeroIP4 {
			return errDHCPFail
		}
	case <-time.After(5 * time.Second):
		return errDHCPFail
	}

	ns.ip = offeredIP
	ns.gw = gwIP
	ns.mask = maskIP
	ns.dns = dnsIP
	if ns.debug {
		println("netstack: IP", fmtI(ns.ip), "GW", fmtI(ns.gw), "DNS", fmtI(ns.dns))
	}
	return nil
}

func (ns *NetStack) sendDHCPMsg(xid uint32, msgType byte, reqIP [4]byte, serverIP []byte) {
	dhcp := make([]byte, 300)
	dhcp[0] = 1 // BOOTREQUEST
	dhcp[1] = 1 // Ethernet
	dhcp[2] = 6
	binary.BigEndian.PutUint32(dhcp[4:8], xid)
	binary.BigEndian.PutUint16(dhcp[10:12], 0x8000) // broadcast flag
	copy(dhcp[28:34], ns.mac[:])
	copy(dhcp[236:240], []byte{99, 130, 83, 99}) // magic cookie

	o := dhcp[240:]
	i := 0
	o[i] = 53
	o[i+1] = 1
	o[i+2] = msgType
	i += 3

	if reqIP != zeroIP4 {
		o[i] = 50
		o[i+1] = 4
		copy(o[i+2:i+6], reqIP[:])
		i += 6
	}
	if serverIP != nil && len(serverIP) == 4 {
		o[i] = 54
		o[i+1] = 4
		copy(o[i+2:i+6], serverIP)
		i += 6
	}
	o[i] = 55
	o[i+1] = 4
	o[i+2] = 1
	o[i+3] = 3
	o[i+4] = 6
	o[i+5] = 51
	i += 6
	o[i] = 255
	// RFC 2131: BOOTP payload must be at least 300 bytes
	// dhcp buffer is already 300 — don't truncate

	udp := make([]byte, 8+len(dhcp))
	binary.BigEndian.PutUint16(udp[0:2], 68)
	binary.BigEndian.PutUint16(udp[2:4], 67)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], dhcp)
	if ns.debug {
		println("sendDHCPMsg: sending", len(udp)+20+14, "bytes, type=", msgType)
	}
	if err := ns.sendIPBcast(17, udp); err != nil {
		if ns.debug {
			println("sendDHCPMsg: TX error:", err)
		}
	}
}

func parseDHCPReply(d []byte) (yourIP, serverIP, gwIP, maskIP, dnsIP [4]byte) {
	if len(d) < 240 {
		return
	}
	if d[0] != 2 {
		return
	}
	copy(yourIP[:], d[16:20])
	if len(d) < 244 {
		return
	}
	opts := d[240:]
	for len(opts) > 2 {
		t := opts[0]
		if t == 255 {
			break
		}
		if t == 0 {
			opts = opts[1:]
			continue
		}
		ol := int(opts[1])
		if len(opts) < 2+ol {
			break
		}
		od := opts[2 : 2+ol]
		switch t {
		case 1:
			if ol == 4 {
				copy(maskIP[:], od)
			}
		case 3:
			if ol >= 4 {
				copy(gwIP[:], od[:4])
			}
		case 6:
			if ol >= 4 {
				copy(dnsIP[:], od[:4])
			}
		case 54:
			if ol == 4 {
				copy(serverIP[:], od)
			}
		}
		opts = opts[2+ol:]
	}
	return
}

// ---------------------------------------------------------------------------
// Public socket API (can be used directly or wired to netdever later)
// ---------------------------------------------------------------------------

// TCPDial connects to a remote TCP host:port. Returns socket fd.
func (ns *NetStack) TCPDial(ip [4]byte, port uint16) (int, error) {
	s := &sock{
		typ:        _SOCK_STREAM,
		localPort:  ns.allocPort(),
		remoteIP:   ip,
		remotePort: port,
		rxBuf:      make([]byte, tcpRxBufSize),
		rxReady:    make(chan struct{}, 1),
		connDone:   make(chan error, 1),
	}
	ns.mu.Lock()
	fd := ns.nextFD
	ns.nextFD++
	ns.socks[fd] = s
	ns.mu.Unlock()

	s.seq = uint32(time.Now().UnixNano() & 0x7FFFFFFF)
	s.state = _TCP_SYN_SENT
	ns.sendTCPFlags(s, _SYN, nil)

	select {
	case err := <-s.connDone:
		return fd, err
	case <-time.After(10 * time.Second):
		s.state = _TCP_CLOSED
		return -1, errTimeout
	}
}

func (ns *NetStack) Send(fd int, buf []byte) (int, error) {
	s := ns.getSock(fd)
	if s == nil {
		return 0, errBadSock
	}
	if s.typ == _SOCK_DGRAM {
		return len(buf), ns.sendUDP(s.remoteIP, s.localPort, s.remotePort, buf)
	}
	if s.state != _TCP_ESTABLISHED {
		return 0, errClosed
	}
	sent := 0
	mss := ns.dev.MTU() - 40
	for sent < len(buf) {
		chunk := buf[sent:]
		if len(chunk) > mss {
			chunk = chunk[:mss]
		}
		ns.sendTCPFlags(s, _PSH|_ACK, chunk)
		sent += len(chunk)
	}
	return sent, nil
}

func (ns *NetStack) Recv(fd int, buf []byte) (int, error) {
	s := ns.getSock(fd)
	if s == nil {
		return 0, errBadSock
	}
	for {
		n := s.rxRead(buf)
		if n > 0 {
			return n, nil
		}
		if s.closed || s.state == _TCP_CLOSED || s.state == _TCP_CLOSE_WAIT || s.state == _TCP_TIME_WAIT {
			return 0, errClosed
		}
		select {
		case <-s.rxReady:
		case <-time.After(15 * time.Second):
			return 0, errTimeout
		}
	}
}

func (ns *NetStack) CloseSock(fd int) error {
	ns.mu.Lock()
	s := ns.socks[fd]
	if s == nil {
		ns.mu.Unlock()
		return errBadSock
	}
	delete(ns.socks, fd)
	ns.mu.Unlock()

	if s.typ == _SOCK_STREAM && s.state == _TCP_ESTABLISHED {
		s.state = _TCP_FIN_WAIT1
		ns.sendTCPFlags(s, _FIN|_ACK, nil)
		time.Sleep(100 * time.Millisecond)
	}
	s.closed = true
	return nil
}

// Resolve resolves a hostname to an IPv4 address via DNS.
func (ns *NetStack) Resolve(name string) ([4]byte, error) {
	return ns.dnsResolve(name)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func ipCsum(hdr []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(hdr); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}
	if len(hdr)%2 != 0 {
		sum += uint32(hdr[len(hdr)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}

func tcpCsum(srcIP, dstIP [4]byte, seg []byte) uint16 {
	var sum uint32
	sum += uint32(srcIP[0])<<8 | uint32(srcIP[1])
	sum += uint32(srcIP[2])<<8 | uint32(srcIP[3])
	sum += uint32(dstIP[0])<<8 | uint32(dstIP[1])
	sum += uint32(dstIP[2])<<8 | uint32(dstIP[3])
	sum += 6 // protocol TCP
	sum += uint32(len(seg))
	for i := 0; i+1 < len(seg); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(seg[i : i+2]))
	}
	if len(seg)%2 != 0 {
		sum += uint32(seg[len(seg)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}

func trySend(ch chan error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func notifyRx(s *sock) {
	select {
	case s.rxReady <- struct{}{}:
	default:
	}
}

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func fmtI(ip [4]byte) string {
	d := func(b byte) string {
		if b < 10 {
			return string(rune('0' + b))
		}
		if b < 100 {
			return string(rune('0'+b/10)) + string(rune('0'+b%10))
		}
		return string(rune('0'+b/100)) + string(rune('0'+(b/10)%10)) + string(rune('0'+b%10))
	}
	return d(ip[0]) + "." + d(ip[1]) + "." + d(ip[2]) + "." + d(ip[3])
}
