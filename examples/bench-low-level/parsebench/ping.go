package main

import (
	"bufio"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The ping log is parsed the same way and for the same reason: by scanning
// tokens, not by matching whole lines.  iputils moves fields around between
// versions (mdev vs stddev, pipe present or absent, the -D timestamp prefix, the
// -O "no answer yet" lines), and a reply line is already a bag of k=v tokens that
// carries no meaning in its order.
//
//	[1754238301.442913] 64 bytes from 192.168.105.148: icmp_seq=1 ttl=64 time=6.16 ms
//	[1754238301.448022] no answer yet for icmp_seq=2
//	180418 packets transmitted, 180145 received, 0.151315% packet loss, time 1049993ms
//	rtt min/avg/max/mdev = 2.285/8.734/332.788/11.528 ms, pipe 31
//
// Timestamps are what let a host-side RTT spike be joined to a device-side
// service-latency spike.  Without -D there is nothing to join on and the tool
// falls back to one distribution for the whole run.

type pingSample struct {
	at  time.Time // zero when the log was captured without -D
	seq int
	rtt float64 // milliseconds
}

type pingLog struct {
	path    string
	target  string
	samples []pingSample

	// -O prints "no answer yet for icmp_seq=N" when the next packet goes out
	// before N has been answered, which at any interval shorter than the RTT is
	// most of them -- seq 1 gets one and is answered 21 ms later.  So these are
	// not losses until reconciled against the replies: pending holds them as they
	// are read, losses holds the ones that never did get an answer, and delayed
	// counts the rest, which are exactly the packets whose RTT exceeded the send
	// interval.
	pending []pingSample
	losses  []pingSample
	delayed int
	maxSeq  int

	// Straight from the trailer, when the run was allowed to finish.
	sent, recv int
	lossPct    float64
	duration   time.Duration
	rtt        map[string]float64 // "min","avg","max","mdev"/"stddev"

	hasTime bool
	skipped int
}

func parsePing(path string, r io.Reader) (*pingLog, error) {
	p := &pingLog{path: path, rtt: map[string]float64{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "PING "):
			if len(fields) > 1 {
				p.target = fields[1]
			}
		case strings.Contains(line, "packets transmitted"):
			p.scanTrailer(fields)
		case strings.HasPrefix(fields[0], "rtt") || strings.HasPrefix(fields[0], "round-trip"):
			p.scanRTT(fields)
		case strings.Contains(line, "no answer yet"), strings.Contains(line, "Unreachable"):
			p.scanLoss(fields)
		case strings.Contains(line, "icmp_seq="):
			p.scanReply(fields)
		case strings.HasPrefix(fields[0], "---"):
			// "--- 1.2.3.4 ping statistics ---", the trailer's own header.
		default:
			p.skipped++
		}
	}
	p.reconcile()
	return p, sc.Err()
}

// reconcile separates the packets that were merely late from the ones that never
// came back.  Without this every run reads as catastrophically lossy: 13,578 of
// 43,060 packets draw a "no answer yet" line purely because the 5 ms send
// interval is shorter than the 7 ms round trip.
func (p *pingLog) reconcile() {
	replied := make(map[int]bool, len(p.samples))
	for _, s := range p.samples {
		replied[s.seq] = true
		if s.seq > p.maxSeq {
			p.maxSeq = s.seq
		}
	}
	for _, s := range p.pending {
		if s.seq > p.maxSeq {
			p.maxSeq = s.seq
		}
		if replied[s.seq] {
			p.delayed++
			continue
		}
		p.losses = append(p.losses, s)
	}
}

// scanReply reads one echo reply.  Every field is optional: a log without -D has
// no stamp, and some builds omit ttl.
func (p *pingLog) scanReply(fields []string) {
	var s pingSample
	for i := 0; i < len(fields); i++ {
		t := fields[i]
		if at, ok := parseStamp(t); ok {
			s.at, p.hasTime = at, true
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		switch k {
		case "icmp_seq", "seq":
			s.seq, _ = strconv.Atoi(v)
		case "time":
			// The unit is either fused to the number or the next token.
			num, unit := splitUnit(v)
			if unit == "" && i+1 < len(fields) {
				unit = fields[i+1]
			}
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				continue
			}
			s.rtt = f * msScale(unit)
		}
	}
	p.samples = append(p.samples, s)
}

func (p *pingLog) scanLoss(fields []string) {
	var s pingSample
	for _, t := range fields {
		if at, ok := parseStamp(t); ok {
			s.at, p.hasTime = at, true
			continue
		}
		if k, v, ok := strings.Cut(t, "="); ok && (k == "icmp_seq" || k == "seq") {
			s.seq, _ = strconv.Atoi(v)
		}
	}
	p.pending = append(p.pending, s)
}

// scanTrailer reads "180418 packets transmitted, 180145 received, 0.151315%
// packet loss, time 1049993ms".  Here the label follows its value, so the scan
// runs the other way round: classify on the label and take the number before it.
func (p *pingLog) scanTrailer(fields []string) {
	clean := make([]string, 0, len(fields))
	for _, f := range fields {
		clean = append(clean, strings.TrimSuffix(f, ","))
	}
	for i, t := range clean {
		switch {
		case t == "transmitted":
			p.sent, _ = numBefore(clean, i)
		case t == "received":
			p.recv, _ = numBefore(clean, i)
		case strings.HasSuffix(t, "%"):
			p.lossPct, _ = strconv.ParseFloat(strings.TrimSuffix(t, "%"), 64)
		case t == "time" && i+1 < len(clean):
			num, unit := splitUnit(clean[i+1])
			if f, err := strconv.ParseFloat(num, 64); err == nil {
				p.duration = time.Duration(f * msScale(unit) * float64(time.Millisecond))
			}
		}
	}
}

// scanRTT zips the slash-separated labels onto the slash-separated values, so it
// does not care whether the fourth statistic is called mdev or stddev, nor how
// many there are.
func (p *pingLog) scanRTT(fields []string) {
	var labels, values string
	for i, t := range fields {
		if t != "=" || i == 0 || i+1 >= len(fields) {
			continue
		}
		labels, values = fields[i-1], fields[i+1]
	}
	if labels == "" {
		return
	}
	ls, vs := strings.Split(labels, "/"), strings.Split(strings.TrimSuffix(values, ","), "/")
	if len(ls) != len(vs) {
		return
	}
	for i, l := range ls {
		if f, err := strconv.ParseFloat(vs[i], 64); err == nil {
			p.rtt[l] = f
		}
	}
}

// numBefore walks back from a label to its value.  "180418 packets transmitted"
// puts a word between the two, and how many words that is has changed between
// iputils releases, so the distance is searched rather than assumed.
func numBefore(fields []string, i int) (int, bool) {
	for j := i - 1; j >= 0 && j >= i-3; j-- {
		if v, err := strconv.Atoi(fields[j]); err == nil {
			return v, true
		}
	}
	return 0, false
}

// parseStamp reads the -D prefix, "[1754238301.442913]".
func parseStamp(t string) (time.Time, bool) {
	if len(t) < 3 || t[0] != '[' || t[len(t)-1] != ']' {
		return time.Time{}, false
	}
	f, err := strconv.ParseFloat(t[1:len(t)-1], 64)
	if err != nil {
		return time.Time{}, false
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

func splitUnit(s string) (num, unit string) {
	for i, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != '-' && c != '+' {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func msScale(unit string) float64 {
	switch strings.TrimSuffix(unit, ",") {
	case "s":
		return 1000
	case "us", "µs":
		return 0.001
	case "ns":
		return 1e-6
	default: // ms, or absent
		return 1
	}
}

// stats derives the distribution from the samples, which is the only route when
// the run was cut short before ping printed its trailer.
func (p *pingLog) stats() (min, avg, max, p50, p90, p99, p999 float64, n int) {
	if len(p.samples) == 0 {
		return
	}
	v := make([]float64, 0, len(p.samples))
	var sum float64
	for _, s := range p.samples {
		v = append(v, s.rtt)
		sum += s.rtt
	}
	sort.Float64s(v)
	n = len(v)
	at := func(q float64) float64 {
		i := int(q * float64(n))
		if i >= n {
			i = n - 1
		}
		return v[i]
	}
	return v[0], sum / float64(n), v[n-1], at(0.50), at(0.90), at(0.99), at(0.999), n
}

// lost reports how many replies never came.  The trailer is authoritative when
// the run was allowed to finish; a run cut short has none, and the reconciled -O
// lines answer it instead.
func (p *pingLog) lost() int {
	if p.sent > 0 {
		return p.sent - p.recv
	}
	return len(p.losses)
}

// transmitted is the trailer's count, or the highest sequence number seen when
// the run was interrupted before ping could print one.
func (p *pingLog) transmitted() int {
	if p.sent > 0 {
		return p.sent
	}
	return p.maxSeq
}

// loss is the percentage, from the trailer when it exists and derived otherwise.
func (p *pingLog) loss() float64 {
	if p.sent > 0 {
		return p.lossPct
	}
	if p.maxSeq == 0 {
		return 0
	}
	return float64(len(p.losses)) / float64(p.maxSeq) * 100
}
