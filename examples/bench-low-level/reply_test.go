package main

import (
	"bytes"
	"testing"
)

var (
	devMAC  = [6]byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	devIP   = [4]byte{192, 168, 1, 99}
	peerMAC = [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	peerIP  = [4]byte{192, 168, 1, 10}
)

// onesSum is the standard internet checksum over b.
func onesSum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = (s & 0xffff) + (s >> 16)
	}
	return ^uint16(s)
}

func TestParseIP4(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want [4]byte
		ok   bool
	}{
		{"192.168.1.99", [4]byte{192, 168, 1, 99}, true},
		{"0.0.0.0", [4]byte{}, true},
		{"255.255.255.255", [4]byte{255, 255, 255, 255}, true},
		{"10.0.0.1", [4]byte{10, 0, 0, 1}, true},
		{"256.0.0.1", [4]byte{}, false},
		{"1.2.3", [4]byte{}, false},
		{"1.2.3.4.5", [4]byte{}, false},
		{"1.2.3.", [4]byte{}, false},
		{".1.2.3", [4]byte{}, false},
		{"1.2.3.4a", [4]byte{}, false},
		{"", [4]byte{}, false},
		{"1.2.3.0004", [4]byte{}, false},
	} {
		got, ok := parseIP4(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseIP4(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func arpRequest(targetIP [4]byte) []byte {
	f := make([]byte, 60)
	copy(f[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(f[6:12], peerMAC[:])
	f[12], f[13] = 0x08, 0x06
	a := f[14:]
	a[0], a[1] = 0, 1 // htype ethernet
	a[2], a[3] = 8, 0 // ptype ipv4
	a[4], a[5] = 6, 4
	a[6], a[7] = 0, 1 // request
	copy(a[8:14], peerMAC[:])
	copy(a[14:18], peerIP[:])
	copy(a[24:28], targetIP[:])
	return f
}

func TestARPReply(t *testing.T) {
	initReply(devMAC, devIP)

	f := arpRequest(devIP)
	n := replyTo(f, len(f))
	if n != 60 {
		t.Fatalf("length = %d, want 60 (42-byte ARP padded to the minimum frame)", n)
	}
	if !bytes.Equal(f[0:6], peerMAC[:]) {
		t.Errorf("eth dst = % x, want the requester", f[0:6])
	}
	if !bytes.Equal(f[6:12], devMAC[:]) {
		t.Errorf("eth src = % x, want us", f[6:12])
	}
	a := f[14:]
	if a[6] != 0 || a[7] != 2 {
		t.Errorf("oper = %d %d, want 0 2 (reply)", a[6], a[7])
	}
	if !bytes.Equal(a[8:14], devMAC[:]) || !bytes.Equal(a[14:18], devIP[:]) {
		t.Errorf("sender = % x / % x, want us", a[8:14], a[14:18])
	}
	if !bytes.Equal(a[18:24], peerMAC[:]) || !bytes.Equal(a[24:28], peerIP[:]) {
		t.Errorf("target = % x / % x, want the requester", a[18:24], a[24:28])
	}

	// A request for somebody else must be left alone.
	f = arpRequest([4]byte{192, 168, 1, 7})
	if n := replyTo(f, len(f)); n != 0 {
		t.Errorf("ARP for another host answered with %d bytes", n)
	}

	// A gratuitous reply (oper 2) is not a request.
	f = arpRequest(devIP)
	f[14+7] = 2
	if n := replyTo(f, len(f)); n != 0 {
		t.Errorf("ARP reply answered with %d bytes", n)
	}
}

// echoRequest builds a well-formed ICMP echo request with payloadLen bytes of
// payload, padded out to the minimum frame like a real short ping.
func echoRequest(dstIP [4]byte, payloadLen int) []byte {
	ipLen := 20 + 8 + payloadLen
	frameLen := 14 + ipLen
	if frameLen < 60 {
		frameLen = 60
	}
	f := make([]byte, frameLen)
	copy(f[0:6], devMAC[:])
	copy(f[6:12], peerMAC[:])
	f[12], f[13] = 0x08, 0x00

	ip := f[14 : 14+ipLen]
	ip[0] = 0x45
	ip[2], ip[3] = byte(ipLen>>8), byte(ipLen)
	ip[4], ip[5] = 0x12, 0x34 // id
	ip[8] = 64                // ttl
	ip[9] = 1                 // ICMP
	copy(ip[12:16], peerIP[:])
	copy(ip[16:20], dstIP[:])
	sum := onesSum(ip[:20])
	ip[10], ip[11] = byte(sum>>8), byte(sum)

	icmp := ip[20:]
	icmp[0], icmp[1] = 8, 0    // echo request
	icmp[4], icmp[5] = 0xab, 1 // id
	icmp[6], icmp[7] = 0, 7    // seq
	for i := 0; i < payloadLen; i++ {
		icmp[8+i] = byte(i)
	}
	sum = onesSum(icmp)
	icmp[2], icmp[3] = byte(sum>>8), byte(sum)
	return f
}

func TestICMPEchoReply(t *testing.T) {
	initReply(devMAC, devIP)

	// 0 exercises the padded path; 56 is what ping sends by default.
	for _, payload := range []int{0, 1, 18, 56, 1000} {
		f := echoRequest(devIP, payload)
		orig := append([]byte(nil), f...)
		n := replyTo(f, len(f))

		wantIPLen := 20 + 8 + payload
		want := 14 + wantIPLen
		if want < 60 {
			want = 60
		}
		if n != want {
			t.Fatalf("payload %d: length = %d, want %d", payload, n, want)
		}
		if !bytes.Equal(f[0:6], peerMAC[:]) || !bytes.Equal(f[6:12], devMAC[:]) {
			t.Errorf("payload %d: MACs not swapped: % x", payload, f[0:12])
		}
		ip := f[14:]
		if !bytes.Equal(ip[12:16], devIP[:]) || !bytes.Equal(ip[16:20], peerIP[:]) {
			t.Errorf("payload %d: IPs not swapped: % x", payload, ip[12:20])
		}
		if s := onesSum(ip[:20]); s != 0 {
			t.Errorf("payload %d: IP header checksum invalid (residual %#04x)", payload, s)
		}
		icmp := ip[20 : 8+payload+20]
		if icmp[0] != 0 || icmp[1] != 0 {
			t.Errorf("payload %d: type/code = %d/%d, want 0/0", payload, icmp[0], icmp[1])
		}
		if s := onesSum(icmp); s != 0 {
			t.Errorf("payload %d: ICMP checksum invalid (residual %#04x)", payload, s)
		}
		// The echoed identifier, sequence and payload must come back untouched.
		if !bytes.Equal(icmp[4:], orig[14+20+4:14+20+8+payload]) {
			t.Errorf("payload %d: echoed body altered", payload)
		}
	}
}

func TestICMPIgnored(t *testing.T) {
	initReply(devMAC, devIP)

	mutate := func(name string, fn func([]byte)) {
		f := echoRequest(devIP, 56)
		fn(f)
		if n := replyTo(f, len(f)); n != 0 {
			t.Errorf("%s: answered with %d bytes, want 0", name, n)
		}
	}
	mutate("addressed elsewhere", func(f []byte) { f[14+19] = 7 })
	mutate("not ICMP", func(f []byte) { f[14+9] = 17 })
	mutate("echo reply not request", func(f []byte) { f[14+20] = 0 })
	mutate("destination unreachable", func(f []byte) { f[14+20] = 3 })
	mutate("more-fragments set", func(f []byte) { f[14+6] |= 0x20 })
	mutate("non-zero fragment offset", func(f []byte) { f[14+7] = 8 })
	mutate("IPv6", func(f []byte) { f[14+0] = 0x60 })
	mutate("IHL below minimum", func(f []byte) { f[14+0] = 0x43 })
	mutate("total length beyond frame", func(f []byte) { f[14+2], f[14+3] = 0xff, 0xff })

	// Frames too short to hold a header at all.
	for _, n := range []int{0, 1, 13, 14, 20, 33} {
		f := echoRequest(devIP, 56)
		if got := replyTo(f, n); got != 0 {
			t.Errorf("n=%d: answered with %d bytes, want 0", n, got)
		}
	}

	// Unknown ethertypes.
	f := echoRequest(devIP, 56)
	f[12], f[13] = 0x86, 0xdd // IPv6
	if got := replyTo(f, len(f)); got != 0 {
		t.Errorf("IPv6 ethertype answered with %d bytes", got)
	}
}

// TestIHLWithOptions covers a header longer than 20 bytes, where the ICMP
// message does not start at a fixed offset.
func TestIHLWithOptions(t *testing.T) {
	initReply(devMAC, devIP)

	const opts = 4
	payload := 16
	ipLen := 20 + opts + 8 + payload
	f := make([]byte, 14+ipLen)
	copy(f[0:6], devMAC[:])
	copy(f[6:12], peerMAC[:])
	f[12], f[13] = 0x08, 0x00
	ip := f[14:]
	ip[0] = 0x40 | byte((20+opts)/4)
	ip[2], ip[3] = byte(ipLen>>8), byte(ipLen)
	ip[8], ip[9] = 64, 1
	copy(ip[12:16], peerIP[:])
	copy(ip[16:20], devIP[:])
	sum := onesSum(ip[:20+opts])
	ip[10], ip[11] = byte(sum>>8), byte(sum)
	icmp := ip[20+opts:]
	icmp[0] = 8
	for i := 0; i < payload; i++ {
		icmp[8+i] = byte(i)
	}
	sum = onesSum(icmp)
	icmp[2], icmp[3] = byte(sum>>8), byte(sum)

	n := replyTo(f, len(f))
	if n != 14+ipLen {
		t.Fatalf("length = %d, want %d", n, 14+ipLen)
	}
	if onesSum(ip[:20+opts]) != 0 {
		t.Error("IP header checksum invalid")
	}
	if icmp[0] != 0 {
		t.Errorf("type = %d, want 0", icmp[0])
	}
	if onesSum(icmp) != 0 {
		t.Error("ICMP checksum invalid")
	}
}

// TestChecksumWrap exercises the one's-complement fold: a checksum near the top
// of the range must carry rather than overflow to zero.
func TestChecksumWrap(t *testing.T) {
	initReply(devMAC, devIP)
	// Search payloads until the request checksum is high enough that adding
	// 0x0800 carries, then verify the reply still validates.
	found := false
	for seed := 0; seed <= 0xffff && !found; seed++ {
		f := echoRequest(devIP, 8)
		icmp := f[14+20:]
		icmp[8], icmp[9] = byte(seed>>8), byte(seed)
		icmp[2], icmp[3] = 0, 0 // the field must be zero while it is computed
		sum := onesSum(icmp[:16])
		icmp[2], icmp[3] = byte(sum>>8), byte(sum)
		if uint32(sum)+0x0800 <= 0xffff {
			continue
		}
		found = true
		if n := replyTo(f, len(f)); n == 0 {
			t.Fatal("carry case not answered")
		}
		if got := onesSum(f[14+20 : 14+20+16]); got != 0 {
			t.Errorf("carry case: ICMP checksum invalid (residual %#04x)", got)
		}
	}
	if !found {
		t.Fatal("no carrying checksum found in the search space")
	}
}
