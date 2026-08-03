//go:build l2echo

package main

// The protocol-free alternative to reply.go, selected with -tags l2echo: bounce
// every frame back to its sender with the MACs swapped.  It measures the same TX
// path with no parsing in front of it, at the cost of needing a raw-socket sender
// on the host instead of ping, and of having no host-side RTT or loss figure.
//
// Be careful what it is pointed at.  It answers broadcast too, so two boards
// running this on one AP will echo each other until the channel is full.

var (
	ourMAC    [6]byte
	announces int64 // unused here; the loop counts what announce sends
)

func initReply(mac [6]byte, _ [4]byte) { ourMAC = mac }

// announce has nothing to advertise without an IP, so the loop sends nothing.
func announce([]byte) int { return 0 }

func appendReplyStats(dst []byte) []byte { return dst }

func replyTo(buf []byte, n int) int {
	if n < 14 {
		return 0
	}
	copy(buf[0:6], buf[6:12])
	copy(buf[6:12], ourMAC[:])
	for n < 60 && n < len(buf) {
		buf[n] = 0
		n++
	}
	return n
}
