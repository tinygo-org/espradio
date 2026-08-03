package main

import (
	"bufio"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The bench log is a sequence of records.  A record begins with a "bench" token
// and runs until the next one, spanning however many lines the device happened to
// emit:
//
//	bench 00:11:12 rx 49189 +rx 925 rxB 4901540 tx 48623 ... txerr 0
//	  saw arp 26 arp4us 19 ipv4 49085 icmp4us 48604 echo4us 48604 other 78 announced 337
//	  svc[ns] n 925 min 17775 avg 74672 max 140275
//	  svc<ns 32768:52 65536:132 131072:740 262144:1
//	  idle[ns] n 4734 avg 1037698 max 1966225  inter[ns] n 925 avg 5446026 max 57465075
//	  heap alloc 85312 obj 52 idle 144448 peak 85312 minidle 144448 drift 0
//	  churn B 0 mallocs 0  since_base B 0 mallocs 0 gc 0 +gc 0
//	  arena used 30808 cap 49152 drift 208
//
// It is parsed by scanning tokens rather than by matching lines, because neither
// the line breaks nor the order carries meaning: two sections already share a line
// ("idle[ns] ... inter[ns] ..."), and adding a counter to the device shifts
// everything after it.  A leading token that names a section switches the
// namespace; every other token is a key whose value is the token after it.  Order
// is then free, within a line and between them, and an unrecognised key costs one
// increment of a counter instead of a mis-parse.
//
// The namespace matters because names repeat: "max" belongs to svc, idle and
// inter, and "drift" to both heap and arena.  A flat map would silently keep
// whichever came last.

// benchSections maps a section-introducing token to its namespace.
var benchSections = map[string]string{
	"bench":      "", // top-level counters live unprefixed
	"saw":        "saw",
	"svc[ns]":    "svc",
	"svc<ns":     "svc", // the histogram continues the svc namespace
	"idle[ns]":   "idle",
	"inter[ns]":  "inter",
	"heap":       "heap",
	"churn":      "churn",
	"since_base": "base",
	"arena":      "arena",
}

type benchRec struct {
	elapsed  time.Duration
	baseline bool
	v        map[string]int64
	meta     map[string]string // non-numeric values: the MAC and IP the device announces
	hist     map[int64]int64   // power-of-two upper bound in ns -> count
	unknown  int
}

func newBenchRec() *benchRec {
	return &benchRec{v: map[string]int64{}, meta: map[string]string{}, hist: map[int64]int64{}}
}

// benchText names the keys whose value is a word rather than a number.  Without
// this the identity line -- "bench mac 98:3d:ae:ac:34:38 ip 192.168.105.148" --
// parses as four unrecognised tokens and, worse, opens a record of its own.
var benchText = map[string]bool{"mac": true, "ip": true}

// measurement reports whether the record carries readings.  The identity line
// starts with "bench" like every other record but has nothing in it to measure,
// and counting it as an interval drags the run's start back to 00:00:00.
func (r *benchRec) measurement() bool {
	return !r.baseline && (len(r.v) > 0 || len(r.hist) > 0)
}

// get returns the first of the candidate keys that is present.  Callers pass
// every namespace a value could plausibly land in, so that moving a counter from
// one section to another in the device does not silently zero a column here --
// which is the failure mode this whole file is arranged to avoid.
func (r *benchRec) get(keys ...string) (int64, bool) {
	for _, k := range keys {
		if v, ok := r.v[k]; ok {
			return v, true
		}
	}
	return 0, false
}

func (r *benchRec) at(keys ...string) int64 {
	v, _ := r.get(keys...)
	return v
}

// scan folds one line's tokens into the record.
func (r *benchRec) scan(fields []string) {
	sec := ""
	for i := 0; i < len(fields); i++ {
		t := fields[i]
		if s, ok := benchSections[t]; ok {
			sec = s
			continue
		}
		if t == "BASELINE" {
			r.baseline = true
			continue
		}
		if d, ok := parseElapsed(t); ok {
			r.elapsed = d
			continue
		}
		if benchText[t] && i+1 < len(fields) {
			r.meta[t] = fields[i+1]
			i++
			continue
		}
		if ub, n, ok := parseHistPair(t); ok {
			r.hist[ub] += n
			continue
		}
		if i+1 < len(fields) {
			if v, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil {
				r.v[nsKey(sec, t)] = v
				i++
				continue
			}
		}
		r.unknown++
	}
}

func nsKey(sec, k string) string {
	if sec == "" {
		return k
	}
	return sec + "." + k
}

// parseElapsed accepts the HH:MM:SS stamp.  Three parts, so it cannot be confused
// with a histogram pair, which has one colon.
func parseElapsed(s string) (time.Duration, bool) {
	p := strings.Split(s, ":")
	if len(p) != 3 {
		return 0, false
	}
	var out [3]int64
	for i, f := range p {
		v, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, false
		}
		out[i] = v
	}
	return time.Duration(out[0]*3600+out[1]*60+out[2]) * time.Second, true
}

// parseHistPair accepts "65536:132" -- samples below 65536 ns, of which there were 132.
func parseHistPair(s string) (upper, n int64, ok bool) {
	k, v, found := strings.Cut(s, ":")
	if !found {
		return 0, 0, false
	}
	upper, err := strconv.ParseInt(k, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	n, err = strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return upper, n, true
}

type benchLog struct {
	path     string
	baseline *benchRec
	identity *benchRec   // the "bench mac ... ip ..." line
	recs     []*benchRec // measurement records, in file order
	skipped  int         // lines belonging to no record (boot banner, monitor noise)
	unknown  int         // tokens inside records that did not parse
}

func parseBench(path string, r io.Reader) (*benchLog, error) {
	b := &benchLog{path: path}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var cur *benchRec
	flush := func() {
		if cur == nil {
			return
		}
		b.unknown += cur.unknown
		switch {
		case cur.baseline:
			b.baseline = cur
		case cur.measurement():
			b.recs = append(b.recs, cur)
		default:
			b.identity = cur
		}
		cur = nil
	}

	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		sec, known := benchSections[fields[0]]
		switch {
		case fields[0] == "bench":
			flush()
			cur = newBenchRec()
		case known && cur != nil:
			_ = sec // a continuation of the record in progress
		default:
			// Boot banner, serial-monitor chatter, a line torn in half by a reset.
			// Counted so a log that is mostly noise cannot pass for a clean run.
			if cur == nil || !known {
				b.skipped++
				continue
			}
		}
		if cur != nil {
			cur.scan(fields)
		}
	}
	flush()
	return b, sc.Err()
}

// span is the wall time the measurement records cover.  The device stamps each
// record with its own uptime, so this is exact even when the capture started
// mid-run -- which is the normal case when the monitor is attached late.
func (b *benchLog) span() (from, to time.Duration) {
	if len(b.recs) == 0 {
		return 0, 0
	}
	return b.recs[0].elapsed, b.recs[len(b.recs)-1].elapsed
}

// mergedHist sums the per-interval histograms into one.
func (b *benchLog) mergedHist() map[int64]int64 {
	out := map[int64]int64{}
	for _, r := range b.recs {
		for ub, n := range r.hist {
			out[ub] += n
		}
	}
	return out
}

// histPercentile returns the bucket upper bound at or below which the given
// fraction of samples fall.  Buckets are powers of two, so this is a bound and
// not an interpolated value: the answer is "below X ns", never "X ns".
func histPercentile(h map[int64]int64, p float64) (upper int64, total int64, ok bool) {
	ubs := make([]int64, 0, len(h))
	for ub := range h {
		ubs = append(ubs, ub)
		total += h[ub]
	}
	if total == 0 {
		return 0, 0, false
	}
	sort.Slice(ubs, func(i, j int) bool { return ubs[i] < ubs[j] })
	want := int64(p * float64(total))
	var cum int64
	for _, ub := range ubs {
		cum += h[ub]
		if cum >= want {
			return ub, total, true
		}
	}
	return ubs[len(ubs)-1], total, true
}

// weighted returns the sample-count-weighted mean of a per-interval average.
// Averaging the averages would give a short interval the same say as a long one.
func (b *benchLog) weighted(avgKeys, nKeys []string) (mean int64, n int64) {
	var sum int64
	for _, r := range b.recs {
		c := r.at(nKeys...)
		if c == 0 {
			continue
		}
		sum += r.at(avgKeys...) * c
		n += c
	}
	if n == 0 {
		return 0, 0
	}
	return sum / n, n
}

// extreme walks the per-interval min/max, ignoring intervals with no samples --
// an interval that saw nothing reports a min of zero, which is not a measurement.
func (b *benchLog) extreme(keys, nKeys []string, max bool) (int64, bool) {
	var out int64
	var seen bool
	for _, r := range b.recs {
		if len(nKeys) > 0 && r.at(nKeys...) == 0 {
			continue
		}
		v, ok := r.get(keys...)
		if !ok {
			continue
		}
		if !seen || (max && v > out) || (!max && v < out) {
			out, seen = v, true
		}
	}
	return out, seen
}

// sum totals a per-interval delta across the run.
func (b *benchLog) sum(keys ...string) int64 {
	var out int64
	for _, r := range b.recs {
		out += r.at(keys...)
	}
	return out
}

// last returns the value from the final record, for counters the device reports
// cumulatively.
func (b *benchLog) last(keys ...string) int64 {
	if len(b.recs) == 0 {
		return 0
	}
	return b.recs[len(b.recs)-1].at(keys...)
}
