//go:build !l2echo

package main

// A minimal ARP and ICMPv4 echo responder, written in place on the received frame.
//
// The point of doing this by hand rather than importing lneto's icmp package is
// that the benchmark's dependency list is part of what it measures: every package
// linked in is more static RAM, which is heap the GC does not get, and more code
// between EthPoll and SendEthFrame than the driver itself.  Answering two protocols
// badly is enough to make ping work, and ping is the load generator.
//
// Nothing here allocates, and nothing here copies the frame -- the reply is the
// request with a few fields rewritten, which is also why the service latency the
// caller measures is almost entirely the driver's.

const (
	ethHdrLen     = 14
	minEthFrame   = 60 // SendEthFrame's documented minimum, CRC excluded.
	etherTypeIPv4 = 0x0800
	etherTypeARP  = 0x0806
)

var (
	ourMAC [6]byte
	ourIP  [4]byte
)

func initReply(mac [6]byte, ip [4]byte) {
	ourMAC, ourIP = mac, ip
}

// What arrived, split finely enough that a log with no replies in it says which
// of the possible reasons it is: nothing addressed to us, ARP that never reached
// us, or a frame we mishandled.  Without this, "norep" alone cannot tell a quiet
// network from a broken responder.
var (
	sawARP      int64 // ARP of any kind
	sawARPForUs int64 // ARP request for ourIP
	sawIPv4     int64
	sawICMP     int64 // ICMP addressed to ourIP
	sawEcho     int64 // ICMP echo request addressed to ourIP
	sawOther    int64 // some other ethertype
	announces   int64 // gratuitous ARPs sent
)

// replyTo rewrites buf[:n] into a reply and returns its length, or 0 if the frame
// is not one this program answers.  Frames we ignore are the normal case: the AP
// forwards broadcast ARP for other hosts, IPv6 router advertisements and mDNS all
// day, and none of that is an error.
func replyTo(buf []byte, n int) int {
	if n < ethHdrLen {
		return 0
	}
	switch uint16(buf[12])<<8 | uint16(buf[13]) {
	case etherTypeARP:
		sawARP++
		return arpReply(buf, n)
	case etherTypeIPv4:
		sawIPv4++
		return icmpEchoReply(buf, n)
	}
	sawOther++
	return 0
}

// announce builds a gratuitous ARP -- a request for our own address, which is how
// a host advertises its binding unprompted -- and returns its length.
func announce(buf []byte) int {
	const arpLen = 28
	if len(buf) < minEthFrame {
		return 0
	}
	clear(buf[:minEthFrame])
	for i := 0; i < 6; i++ {
		buf[i] = 0xff
	}
	copy(buf[6:12], ourMAC[:])
	buf[12], buf[13] = etherTypeARP>>8, etherTypeARP&0xff
	a := buf[ethHdrLen : ethHdrLen+arpLen]
	a[1] = 1    // htype ethernet
	a[2] = 0x08 // ptype IPv4
	a[4], a[5] = 6, 4
	a[7] = 1 // oper request
	copy(a[8:14], ourMAC[:])
	copy(a[14:18], ourIP[:])
	// Target hardware left zero, target protocol is our own address.
	copy(a[24:28], ourIP[:])
	return minEthFrame
}

// appendReplyStats renders the classification counters into the report.
func appendReplyStats(dst []byte) []byte {
	dst = append(dst, "  saw "...)
	dst = kv(dst, "arp", sawARP)
	dst = kv(dst, "arp4us", sawARPForUs)
	dst = kv(dst, "ipv4", sawIPv4)
	dst = kv(dst, "icmp4us", sawICMP)
	dst = kv(dst, "echo4us", sawEcho)
	dst = kv(dst, "other", sawOther)
	dst = kv(dst, "announced", announces)
	return dst
}

// arpReply answers a request for ourIP.  ARP payload layout from offset ethHdrLen:
// htype[2] ptype[2] hlen plen oper[2] sha[6] spa[4] tha[6] tpa[4].
func arpReply(buf []byte, n int) int {
	const arpLen = 28
	if n < ethHdrLen+arpLen {
		return 0
	}
	a := buf[ethHdrLen:]
	ethernetIPv4ARP := a[0] == 0 && a[1] == 1 && a[2] == 0x08 && a[3] == 0 && a[4] == 6 && a[5] == 4
	if !ethernetIPv4ARP || a[6] != 0 || a[7] != 1 { // oper 1 = request
		return 0
	}
	if !ip4Equal(a[24:28], ourIP[:]) {
		return 0
	}
	sawARPForUs++

	copy(buf[0:6], buf[6:12]) // reply goes back to whoever asked
	copy(buf[6:12], ourMAC[:])

	a[7] = 2                  // oper 2 = reply
	copy(a[18:24], a[8:14])   // target hardware = old sender hardware
	copy(a[24:28], a[14:18])  // target protocol = old sender protocol
	copy(a[8:14], ourMAC[:])  // sender hardware = us
	copy(a[14:18], ourIP[:])  // sender protocol = us
	return pad(buf, ethHdrLen+arpLen)
}

// icmpEchoReply answers an echo request addressed to ourIP.
func icmpEchoReply(buf []byte, n int) int {
	if n < ethHdrLen+20 {
		return 0
	}
	ip := buf[ethHdrLen:]
	if ip[0]>>4 != 4 {
		return 0
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || n < ethHdrLen+ihl+8 {
		return 0
	}
	if ip[9] != 1 { // protocol 1 = ICMP
		return 0
	}
	// No reassembly here, so anything fragmented is left alone rather than
	// answered wrongly: MF set, or a non-zero fragment offset.
	if ip[6]&0x3f != 0 || ip[7] != 0 {
		return 0
	}
	if !ip4Equal(ip[16:20], ourIP[:]) {
		return 0
	}
	sawICMP++
	// The reply is as long as the datagram says, not as long as the frame: a
	// short echo arrives padded out to the 60-byte minimum and echoing the
	// padding back would confuse the sender's payload check.
	total := int(ip[2])<<8 | int(ip[3])
	if total < ihl+8 || ethHdrLen+total > n {
		return 0
	}
	icmp := ip[ihl:]
	if icmp[0] != 8 || icmp[1] != 0 { // type 8 code 0 = echo request
		return 0
	}
	sawEcho++

	copy(buf[0:6], buf[6:12])
	copy(buf[6:12], ourMAC[:])

	// The IP header checksum is a one's-complement sum of its words, so swapping
	// two of them leaves it correct as it stands.
	var tmp [4]byte
	copy(tmp[:], ip[12:16])
	copy(ip[12:16], ip[16:20])
	copy(ip[16:20], tmp[:])

	// Echo request becomes echo reply.  Only the type byte changes, and it is the
	// high half of the first word, so the checksum moves by exactly +0x0800 in
	// one's-complement arithmetic -- no need to walk the payload.
	icmp[0] = 0
	c := uint32(icmp[2])<<8 | uint32(icmp[3])
	c += 0x0800
	c = (c & 0xffff) + (c >> 16)
	icmp[2], icmp[3] = byte(c>>8), byte(c)

	return pad(buf, ethHdrLen+total)
}

// pad zero-fills up to the minimum frame length and returns the length to send.
func pad(buf []byte, n int) int {
	for n < minEthFrame && n < len(buf) {
		buf[n] = 0
		n++
	}
	return n
}

func ip4Equal(a, b []byte) bool {
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3]
}
