// bench-low-level measures espradio with nothing between the instrument and the
// driver: no lneto, no net, no fmt.  It brings the radio up, associates with an
// AP, and then sits in a poll/reply loop over the raw NetDev, reporting what the
// Go heap, the blob arena, and the RX/TX path are doing.
//
// It exists because the question "does a driver change move the Go heap" cannot
// be answered through a TCP stack.  Two things make that question awkward on this
// target, and this program is built around both:
//
//   - Almost all driver memory is invisible to the Go GC.  espradio hands the blob
//     one large []byte (see arena_pool_default.go) and arena.c sub-allocates inside
//     it, so a driver that leaks steadily still shows a flat runtime.MemStats.
//     ArenaStats is therefore sampled alongside MemStats; it is the only view of
//     that memory, and it exists on every revision worth comparing against.
//
//   - Forcing a collection before reading the heap destroys the measurement.  This
//     program never calls runtime.GC.  HeapAlloc is left to trace its real sawtooth,
//     which is what makes the collection count below a count rather than an estimate.
//
// The load generator is ping.  The program answers ARP and ICMP echo in place on
// the received frame (see reply.go), which costs about forty lines and no
// dependencies, and in exchange ping supplies host-side RTT and packet loss on any
// machine without root.
//
//	tinygo flash -target xiao-esp32c3 -monitor \
//	  -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword -X main.ip=192.168.1.99" \
//	  ./examples/bench-low-level
package main

import (
	"time"

	"tinygo.org/x/espradio"
)

// Set with -ldflags "-X main.ssid=... -X main.password=... -X main.ip=...".
//
// The address is static because DHCP would require the stack this program exists
// to stay out of.  Pick one outside the AP's DHCP pool.
var (
	ssid     string
	password string
	ip       = "192.168.1.99"
)

// idleSleep is how long the loop waits after an EthPoll that found nothing.  It
// also sets the resolution of the idle-period figure: the loop asks for this and
// reports what it actually got, so scheduler overhead shows up as overshoot.
const idleSleep = time.Millisecond

// announceEvery is the gratuitous-ARP period.  One 60-byte frame at this rate is
// negligible against the load, and it keeps the AP's forwarding table warm.
const announceEvery = 2 * time.Second

var nextAnnounce time.Time

// frameBuf is the one RX/TX buffer.  Package-level so it lands in bss: declared
// inside main it escapes to the heap, and a one-off 1518-byte allocation landing
// in the first sample after the baseline is exactly the kind of noise this
// program is supposed to be free of.
var frameBuf [espradio.MaxFrameSize]byte

func main() {
	time.Sleep(time.Second)
	println("bench-low-level: espradio raw netdev benchmark")

	println("enabling radio...")
	err := espradio.Enable(espradio.Config{
		Logging: espradio.LogLevelError,
	})
	failIfErr("enabling radio", err)

	println("starting radio...")
	err = espradio.Start()
	failIfErr("starting radio", err)

	// Associate before StartNetDev.  Without this the RX ring never fills and
	// every SendEthFrame is refused before it reaches the blob.
	println("connecting to", ssid, "...")
	err = espradio.Connect(espradio.STAConfig{SSID: ssid, Password: password})
	failIfErr("connecting to AP", err)

	println("starting netdev...")
	netdev, err := espradio.StartNetDev()
	failIfErr("starting netdev", err)

	mac, err := netdev.HardwareAddr6()
	failIfErr("reading MAC", err)
	addr, ok := parseIP4(ip)
	if !ok {
		failIfErr("parsing -X main.ip", errBadIP)
	}
	initReply(mac, addr)
	printIdentity(mac, addr)

	// Latch the baseline after bring-up and before any traffic, so drift is
	// measured against a radio that is up and idle rather than one still associating.
	statsBaseline()
	println("ready: ping", ip)

	buf := frameBuf[:]
	var lastTop time.Time
	idle := false
	for {
		top := time.Now()
		if idle && !lastTop.IsZero() {
			// The period of a loop that did nothing but sleep.  Asking for
			// idleSleep and getting several times it is scheduler overhead, which
			// is the thing a driver change is most likely to move.
			idlePeriod.add(top.Sub(lastTop))
		}
		lastTop = top

		n, err := netdev.EthPoll(buf[:])
		if err != nil {
			rxErr++
		}
		if n == 0 {
			idle = true
			// A host pinging us must ARP first, and an AP that has never seen a
			// frame from this IP may not deliver that request.  Buffer is free here.
			if !top.Before(nextAnnounce) {
				nextAnnounce = top.Add(announceEvery)
				if m := announce(buf); m > 0 {
					if err := netdev.SendEthFrame(buf[:m]); err != nil {
						txErr++
					} else {
						announces++
					}
				}
			}
			sample(time.Now())
			time.Sleep(idleSleep)
			continue
		}
		idle = false
		rxDone := time.Now()
		noteRx(rxDone, n)

		m := replyTo(buf[:], n)
		if m == 0 {
			noReply++
			sample(rxDone)
			continue
		}
		err = netdev.SendEthFrame(buf[:m])
		// Measured from the frame being in hand to the driver having taken it, so
		// it is the TX path and nothing else -- no sleep, no poll, no host.
		noteService(time.Since(rxDone))
		if err != nil {
			txErr++
		} else {
			ntx++
			txBytes += int64(m)
		}
		sample(time.Now())
	}
}

// parseIP4 parses dotted-quad IPv4 without net/netip, which would pull in more
// than this program is willing to link.
func parseIP4(s string) (out [4]byte, ok bool) {
	field, val, digits := 0, 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			val = val*10 + int(c-'0')
			digits++
			if val > 255 || digits > 3 {
				return out, false
			}
		case c == '.':
			if digits == 0 || field >= 3 {
				return out, false
			}
			out[field] = byte(val)
			field, val, digits = field+1, 0, 0
		default:
			return out, false
		}
	}
	if field != 3 || digits == 0 {
		return out, false
	}
	out[3] = byte(val)
	return out, true
}

type benchError string

func (e benchError) Error() string { return string(e) }

const errBadIP = benchError("want dotted-quad IPv4")

func failIfErr(msg string, err error) {
	if err == nil {
		return
	}
	for {
		println("failure:", msg+":", err.Error())
		time.Sleep(time.Second)
	}
}
