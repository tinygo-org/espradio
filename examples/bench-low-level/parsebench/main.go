// parsebench turns the two logs a bench-low-level run produces -- the device's
// own report stream and ping's output on the host -- into a summary, or into a
// side-by-side comparison when given two runs.
//
//	parsebench bench-new.log newping.log
//	parsebench bench-old.log bench-new.log newping.log
//
// Arguments are classified by content rather than by position or extension, and
// each ping log attaches to the bench log before it.  With two runs the first is
// the baseline and every figure is reported with its delta.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	asJSON := flag.Bool("json", false, "emit the per-interval series and summary as JSON")
	full := flag.Bool("full", false, "print every joined interval instead of the worst few")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: parsebench [-json] [-full] <log>...

Logs may be given in any order; each is classified by its contents.  A ping log
attaches to the bench log preceding it.  Two bench logs produce an A/B, with the
first as the baseline.
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	runs, err := loadRuns(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "parsebench:", err)
		os.Exit(1)
	}

	switch {
	case *asJSON:
		err = emitJSON(runs)
	case len(runs) == 1:
		err = reportOne(runs[0], *full)
	default:
		err = reportAB(runs, *full)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "parsebench:", err)
		os.Exit(1)
	}
}

type run struct {
	name  string
	bench *benchLog
	ping  *pingLog
}

// loadRuns classifies each argument and groups it.  Sniffing the contents means
// the caller does not have to remember an order or a naming convention, and a
// ping log handed in before its bench log still lands in the right run.
func loadRuns(paths []string) ([]*run, error) {
	var runs []*run
	var pending []*pingLog

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		kind, err := sniff(p)
		if err != nil {
			f.Close()
			return nil, err
		}
		if kind == "ping" {
			pl, err := parsePing(p, f)
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			if len(runs) == 0 {
				pending = append(pending, pl)
			} else if last := runs[len(runs)-1]; last.ping == nil {
				last.ping = pl
			} else {
				return nil, fmt.Errorf("%s: run %q already has a ping log", p, last.name)
			}
			continue
		}
		bl, err := parseBench(p, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		r := &run{name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), bench: bl}
		if len(pending) > 0 {
			r.ping, pending = pending[0], pending[1:]
		}
		runs = append(runs, r)
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no bench log among %d file(s)", len(paths))
	}
	if len(runs) > 2 {
		return nil, fmt.Errorf("got %d bench logs, want 1 or 2", len(runs))
	}
	for _, r := range runs {
		if len(r.bench.recs) == 0 {
			return nil, fmt.Errorf("%s: no bench records found", r.bench.path)
		}
	}
	return runs, nil
}

// sniff reads enough of the head of a file to tell the two formats apart.
func sniff(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	switch {
	case strings.Contains(head, "icmp_seq="), strings.HasPrefix(head, "PING "):
		return "ping", nil
	case strings.Contains(head, "bench "), strings.Contains(head, "svc[ns]"):
		return "bench", nil
	}
	return "", fmt.Errorf("%s: not recognisable as a bench or ping log", path)
}

// ---------------------------------------------------------------- metrics

type metric struct {
	group string
	label string
	num   float64
	has   bool
	str   string
	// f is kept so the A/B delta can be rendered in the metric's own units.  A
	// change of "+5.058e+04" says much less than "+49,829 frames".
	f func(float64) string
	// dir says which way is better, for reading the A/B column: -1 lower, +1
	// higher, 0 neither.  Counters that are only ever "should be zero" use -1.
	dir int
}

func mNum(group, label string, v float64, f func(float64) string, dir int) metric {
	return metric{group: group, label: label, num: v, has: true, str: f(v), f: f, dir: dir}
}

func mStr(group, label, s string) metric {
	return metric{group: group, label: label, str: s}
}

func summarize(r *run) []metric {
	b := r.bench
	from, to := b.span()
	span := to - from
	if span <= 0 {
		span = time.Duration(len(b.recs)) * 5 * time.Second
	}
	secs := span.Seconds()

	var m []metric
	add := func(x metric) { m = append(m, x) }

	// ---- run
	add(mStr("run", "source", filepath.Base(b.path)))
	if id := b.identity; id != nil {
		add(mStr("run", "device", fmt.Sprintf("mac %s  ip %s", id.meta["mac"], id.meta["ip"])))
	}
	add(mStr("run", "window", fmt.Sprintf("%s -> %s (%s)", fmtDur(from), fmtDur(to), fmtDur(span))))
	add(mNum("run", "intervals", float64(len(b.recs)), fmtInt, 0))
	if b.baseline != nil {
		add(mStr("run", "baseline", fmt.Sprintf("heap %s  arena %s/%s",
			fmtBytes(float64(b.baseline.at("heapalloc"))),
			fmtBytes(float64(b.baseline.at("arenaused"))),
			fmtBytes(float64(b.baseline.at("arenacap"))))))
	} else {
		add(mStr("run", "baseline", "absent (capture started mid-run)"))
	}

	// ---- traffic
	rx, tx := float64(b.last("rx")), float64(b.last("tx"))
	rxB, txB := float64(b.last("rxB")), float64(b.last("txB"))
	add(mNum("traffic", "rx frames", rx, fmtInt, 0))
	add(mNum("traffic", "tx frames", tx, fmtInt, 0))
	add(mNum("traffic", "rx bytes", rxB, fmtBytes, 0))
	add(mNum("traffic", "tx bytes", txB, fmtBytes, 0))
	if rx > 0 {
		add(mNum("traffic", "mean rx frame", rxB/rx, fmtBytes, 0))
	}
	add(mNum("traffic", "rx rate", float64(b.sum("+rx"))/secs, func(v float64) string {
		return fmt.Sprintf("%.1f frame/s", v)
	}, +1))
	add(mNum("traffic", "unanswered", float64(b.last("norep")), fmtInt, -1))
	add(mNum("traffic", "rx errors", float64(b.last("rxerr")), fmtInt, -1))
	add(mNum("traffic", "tx errors", float64(b.last("txerr")), fmtInt, -1))

	// ---- what arrived
	for _, k := range []string{"arp", "arp4us", "ipv4", "icmp4us", "echo4us", "other", "announced"} {
		if v, ok := b.recs[len(b.recs)-1].get("saw." + k); ok {
			add(mNum("saw", k, float64(v), fmtInt, 0))
		}
	}

	// ---- service latency: the TX path with nothing else in it
	svcMean, svcN := b.weighted([]string{"svc.avg"}, []string{"svc.n"})
	add(mNum("svc", "samples", float64(svcN), fmtInt, 0))
	if mn, ok := b.extreme([]string{"svc.min"}, []string{"svc.n"}, false); ok {
		add(mNum("svc", "min", float64(mn), fmtNS, -1))
	}
	add(mNum("svc", "mean", float64(svcMean), fmtNS, -1))
	hist := b.mergedHist()
	for _, q := range []struct {
		label string
		p     float64
	}{{"p50", 0.50}, {"p90", 0.90}, {"p99", 0.99}} {
		if ub, _, ok := histPercentile(hist, q.p); ok {
			// Displayed as a bound because the device buckets by octave, but the
			// delta is rendered plainly -- "+<114 us" reads as a typo.
			m := mNum("svc", q.label, float64(ub), fmtNS, -1)
			m.str = "<" + m.str
			add(m)
		}
	}
	if mx, ok := b.extreme([]string{"svc.max"}, []string{"svc.n"}, true); ok {
		add(mNum("svc", "max", float64(mx), fmtNS, -1))
	}

	// ---- idle loop period: asks for idleSleep, reports what it got.  The gap is
	// scheduler overhead, which is the figure a driver change is most likely to move.
	idleMean, idleN := b.weighted([]string{"idle.avg"}, []string{"idle.n"})
	add(mNum("idle", "samples", float64(idleN), fmtInt, 0))
	add(mNum("idle", "mean period", float64(idleMean), fmtNS, -1))
	add(mNum("idle", "overshoot", float64(idleMean)-float64(idleSleepNS), fmtNS, -1))
	if mx, ok := b.extreme([]string{"idle.max"}, []string{"idle.n"}, true); ok {
		add(mNum("idle", "max period", float64(mx), fmtNS, -1))
	}

	// ---- offered load, which is the host's doing and not the driver's.  It is
	// here so that two runs can be shown to have been driven the same way.
	interMean, interN := b.weighted([]string{"inter.avg"}, []string{"inter.n"})
	add(mNum("inter", "samples", float64(interN), fmtInt, 0))
	add(mNum("inter", "mean gap", float64(interMean), fmtNS, 0))
	if mx, ok := b.extreme([]string{"inter.max"}, []string{"inter.n"}, true); ok {
		add(mNum("inter", "max gap", float64(mx), fmtNS, 0))
	}

	// ---- Go heap.  Churn and mallocs are exact and collector-independent; they
	// are the only place a driver's Go-heap allocation can show up.
	add(mNum("heap", "churn", float64(b.sum("churn.B")), fmtBytes, -1))
	add(mNum("heap", "mallocs", float64(b.sum("churn.mallocs")), fmtInt, -1))
	add(mNum("heap", "gc cycles", float64(b.last("base.gc", "gc")), fmtInt, -1))
	add(mNum("heap", "alloc final", float64(b.last("heap.alloc")), fmtBytes, -1))
	add(mNum("heap", "drift", float64(b.last("heap.drift")), fmtSignedBytes, -1))
	add(mNum("heap", "peak", float64(b.last("heap.peak")), fmtBytes, -1))
	add(mNum("heap", "min free", float64(b.last("heap.minidle")), fmtBytes, +1))

	// ---- the arena, which is where every driver allocation actually lives and
	// which runtime.MemStats cannot see.
	add(mNum("arena", "used final", float64(b.last("arena.used")), fmtBytes, -1))
	if mx, ok := b.extreme([]string{"arena.used"}, nil, true); ok {
		add(mNum("arena", "used max", float64(mx), fmtBytes, -1))
	}
	add(mNum("arena", "capacity", float64(b.last("arena.cap")), fmtBytes, 0))
	add(mNum("arena", "drift", float64(b.last("arena.drift")), fmtSignedBytes, -1))

	// ---- host side
	if p := r.ping; p != nil {
		mn, avg, mx, p50, p90, p99, p999, n := p.stats()
		if sent := p.transmitted(); sent > 0 {
			add(mNum("ping", "sent", float64(sent), fmtInt, 0))
		}
		add(mNum("ping", "replies", float64(n), fmtInt, +1))
		add(mNum("ping", "lost", float64(p.lost()), fmtInt, -1))
		add(mNum("ping", "loss", p.loss(), func(v float64) string { return fmt.Sprintf("%.4f%%", v) }, -1))
		if p.delayed > 0 {
			// Packets whose RTT outran the send interval.  A coarse tail measure,
			// but one that needs no timestamps and covers the whole run.
			add(mNum("ping", "rtt>interval", float64(p.delayed), fmtInt, -1))
			if sent := p.transmitted(); sent > 0 {
				add(mNum("ping", "rtt>interval %", float64(p.delayed)/float64(sent)*100,
					func(v float64) string { return fmt.Sprintf("%.2f%%", v) }, -1))
			}
		}
		if n > 0 {
			add(mNum("ping", "rtt min", mn, fmtMS, -1))
			add(mNum("ping", "rtt mean", avg, fmtMS, -1))
			add(mNum("ping", "rtt p50", p50, fmtMS, -1))
			add(mNum("ping", "rtt p90", p90, fmtMS, -1))
			add(mNum("ping", "rtt p99", p99, fmtMS, -1))
			add(mNum("ping", "rtt p99.9", p999, fmtMS, -1))
			add(mNum("ping", "rtt max", mx, fmtMS, -1))
		}
		if v, ok := p.rtt["mdev"]; ok {
			add(mNum("ping", "rtt mdev", v, fmtMS, -1))
		} else if v, ok := p.rtt["stddev"]; ok {
			add(mNum("ping", "rtt stddev", v, fmtMS, -1))
		}
	}
	return m
}

const idleSleepNS = 1_000_000 // the idleSleep the device asks for

// ---------------------------------------------------------------- warnings

func warnings(r *run) []string {
	var w []string
	b := r.bench
	if b.skipped > 0 {
		w = append(w, fmt.Sprintf("%d line(s) outside any record (boot banner or monitor noise)", b.skipped))
	}
	if b.unknown > 0 {
		w = append(w, fmt.Sprintf("%d token(s) inside records did not parse -- the device may report a field this tool does not know", b.unknown))
	}
	if b.baseline == nil {
		w = append(w, "no BASELINE record: drift is relative to the device's own baseline, which this capture did not include")
	}
	if v := b.sum("churn.B"); v != 0 {
		w = append(w, fmt.Sprintf("Go heap churn is %s, not zero -- something allocated during the run", fmtBytes(float64(v))))
	}
	if v := b.last("arena.drift"); v != 0 {
		w = append(w, fmt.Sprintf("arena ended %s off its baseline -- driver memory this run did not give back", fmtSignedBytes(float64(v))))
	}
	if v := b.last("txerr"); v > 0 {
		w = append(w, fmt.Sprintf("%d TX error(s)", v))
	}
	if v := b.last("rxerr"); v > 0 {
		w = append(w, fmt.Sprintf("%d RX error(s)", v))
	}
	// A send that appears to take no time at all means the clock did not advance
	// across it, so the minimum -- and to a lesser degree the mean -- is bounded
	// by the timer rather than by the driver.
	if mn, ok := b.extreme([]string{"svc.min"}, []string{"svc.n"}, false); ok && mn == 0 {
		w = append(w, "svc min is 0 ns: the timer did not advance across at least one send, so the low end is resolution-bound")
	}
	// Every received frame was either answered or counted unanswered, and the
	// device counts announcements apart from replies, so tx and rx-norep have to
	// meet exactly.  If they do not, a reply was built and then lost between the
	// poll and the send.
	rx, norep, tx := b.last("rx"), b.last("norep"), b.last("tx")
	if want := rx - norep; tx != want {
		w = append(w, fmt.Sprintf("reply accounting is off by %d: rx-norep=%d but tx=%d", want-tx, want, tx))
	}
	if p := r.ping; p != nil {
		if !p.hasTime {
			w = append(w, "ping log has no timestamps: re-run with -D -O to join RTT spikes to device intervals")
		}
		if p.lossPct > 1 {
			w = append(w, fmt.Sprintf("ping loss %.3f%% is high enough to distort the comparison", p.lossPct))
		}
		if p.skipped > 0 {
			w = append(w, fmt.Sprintf("%d ping line(s) unrecognised", p.skipped))
		}
	}
	return w
}

// compareChecks guards the comparison itself.  Latency and churn per unit time
// only mean something between two runs that were driven the same way, and
// nothing in the logs forces that to be true -- the operator sets the ping rate
// and the run length by hand, on a channel whose conditions they do not control.
func compareChecks(base, head *run) []string {
	var w []string
	pct := func(a, b float64) float64 {
		if a == 0 {
			return 0
		}
		return (b - a) / a * 100
	}
	rate := func(r *run) float64 {
		from, to := r.bench.span()
		if to <= from {
			return 0
		}
		return float64(r.bench.sum("+rx")) / (to - from).Seconds()
	}
	if a, b := rate(base), rate(head); a > 0 && b > 0 {
		if d := pct(a, b); absf(d) > 5 {
			w = append(w, fmt.Sprintf("offered load differs by %.1f%% (%.1f vs %.1f frame/s): latency figures are not comparable at different loads", d, a, b))
		}
	}
	aFrom, aTo := base.bench.span()
	bFrom, bTo := head.bench.span()
	if a, b := (aTo - aFrom).Seconds(), (bTo - bFrom).Seconds(); a > 0 && b > 0 {
		if d := pct(a, b); absf(d) > 25 {
			w = append(w, fmt.Sprintf("run lengths differ by %.0f%% (%s vs %s): drift and peak figures favour the shorter run", d, fmtDur(aTo-aFrom), fmtDur(bTo-bFrom)))
		}
	}
	if base.ping == nil || head.ping == nil {
		w = append(w, "only one run has a ping log, so the host-side column cannot be compared")
	}
	return w
}

// ---------------------------------------------------------------- reports

func reportOne(r *run, full bool) error {
	out := new(strings.Builder)
	fmt.Fprintf(out, "== %s ==\n", r.name)
	writeGroups(out, summarize(r), nil)
	writeWarnings(out, r.name, warnings(r))
	writeJoin(out, r, full)
	fmt.Print(out.String())
	return nil
}

func reportAB(runs []*run, full bool) error {
	base, head := runs[0], runs[1]
	out := new(strings.Builder)
	fmt.Fprintf(out, "== %s (baseline) vs %s ==\n", base.name, head.name)
	writeGroups(out, summarize(head), summarize(base))
	writeWarnings(out, "comparison", compareChecks(base, head))
	writeWarnings(out, base.name, warnings(base))
	writeWarnings(out, head.name, warnings(head))
	writeJoin(out, base, full)
	writeJoin(out, head, full)
	fmt.Print(out.String())
	return nil
}

// writeGroups prints one column, or two with a delta when a baseline is given.
func writeGroups(out *strings.Builder, cur, base []metric) {
	byLabel := map[string]metric{}
	for _, m := range base {
		byLabel[m.group+"/"+m.label] = m
	}
	group := ""
	for _, m := range cur {
		if m.group != group {
			group = m.group
			fmt.Fprintf(out, "\n%s\n", strings.ToUpper(group))
		}
		if base == nil {
			fmt.Fprintf(out, "  %-14s %s\n", m.label, m.str)
			continue
		}
		b, ok := byLabel[m.group+"/"+m.label]
		if !ok {
			fmt.Fprintf(out, "  %-14s %22s %22s\n", m.label, "-", m.str)
			continue
		}
		fmt.Fprintf(out, "  %-14s %22s %22s  %s\n", m.label, b.str, m.str, delta(b, m))
	}
}

// delta renders the change from baseline to current, marking the direction only
// where one is meaningfully better than the other.
func delta(b, c metric) string {
	if !b.has || !c.has {
		return ""
	}
	d := c.num - b.num
	if d == 0 {
		return "="
	}
	amount := fmt.Sprintf("%+.4g", d)
	if c.f != nil {
		amount = c.f(d)
		if d > 0 && !strings.HasPrefix(amount, "+") {
			amount = "+" + amount
		}
	}
	var pct string
	if b.num != 0 {
		pct = fmt.Sprintf(" (%+.1f%%)", d/absf(b.num)*100)
	}
	mark := ""
	if c.dir != 0 {
		if (d < 0) == (c.dir < 0) {
			mark = " better"
		} else {
			mark = " worse"
		}
	}
	return amount + pct + mark
}

func writeWarnings(out *strings.Builder, name string, w []string) {
	if len(w) == 0 {
		return
	}
	fmt.Fprintf(out, "\nCHECKS (%s)\n", name)
	for _, s := range w {
		fmt.Fprintf(out, "  ! %s\n", s)
	}
}

// ---------------------------------------------------------------- joining

// joined is one bench interval with the ping samples that fell inside it.
type joined struct {
	Elapsed  time.Duration `json:"elapsed"`
	RxDelta  int64         `json:"rx_delta"`
	SvcMax   int64         `json:"svc_max_ns"`
	IdleMax  int64         `json:"idle_max_ns"`
	PingN    int           `json:"ping_replies"`
	PingP99  float64       `json:"ping_p99_ms"`
	PingMax  float64       `json:"ping_max_ms"`
	PingLost int           `json:"ping_lost"`
}

// join places each ping sample in a bench interval.  The device stamps its
// records with uptime and ping stamps its lines with wall clock, so there is no
// shared origin; the offset is recovered by sliding one series against the other
// and taking the alignment where the two packet-count series agree best.  That
// only works if ping was run with -D.
func join(r *run) ([]joined, float64, bool) {
	b, p := r.bench, r.ping
	if p == nil || !p.hasTime || len(b.recs) < 3 {
		return nil, 0, false
	}
	width := 5 * time.Second
	if len(b.recs) >= 2 {
		if d := b.recs[1].elapsed - b.recs[0].elapsed; d > 0 {
			width = d
		}
	}
	var t0 time.Time
	for _, s := range p.samples {
		if !s.at.IsZero() {
			t0 = s.at
			break
		}
	}
	if t0.IsZero() {
		return nil, 0, false
	}
	nbin := int(p.samples[len(p.samples)-1].at.Sub(t0)/width) + 1
	if nbin < 2 {
		return nil, 0, false
	}
	binN := make([]int, nbin)
	binRTT := make([][]float64, nbin)
	for _, s := range p.samples {
		i := int(s.at.Sub(t0) / width)
		if i < 0 || i >= nbin {
			continue
		}
		binN[i]++
		binRTT[i] = append(binRTT[i], s.rtt)
	}
	binLost := make([]int, nbin)
	for _, s := range p.losses {
		if i := int(s.at.Sub(t0) / width); i >= 0 && i < nbin {
			binLost[i]++
		}
	}

	dev := make([]float64, len(b.recs))
	for i, rec := range b.recs {
		dev[i] = float64(rec.at("+rx"))
	}
	host := make([]float64, nbin)
	for i, n := range binN {
		host[i] = float64(n)
	}
	off, score := bestOffset(dev, host)

	var out []joined
	for i, rec := range b.recs {
		j := joined{
			Elapsed: rec.elapsed,
			RxDelta: rec.at("+rx"),
			SvcMax:  rec.at("svc.max"),
			IdleMax: rec.at("idle.max"),
		}
		if k := i + off; k >= 0 && k < nbin {
			j.PingN, j.PingLost = binN[k], binLost[k]
			if v := binRTT[k]; len(v) > 0 {
				sorted := append([]float64(nil), v...)
				sort.Float64s(sorted)
				j.PingP99 = sorted[min(len(sorted)-1, int(0.99*float64(len(sorted))))]
				j.PingMax = sorted[len(sorted)-1]
			}
		}
		out = append(out, j)
	}
	return out, score, true
}

// bestOffset slides host against dev and returns the shift with the highest
// normalised correlation.
func bestOffset(dev, host []float64) (int, float64) {
	best, bestScore := 0, -2.0
	lo, hi := -len(host), len(host)
	for off := lo; off <= hi; off++ {
		var sxy, sxx, syy float64
		var n int
		for i := range dev {
			k := i + off
			if k < 0 || k >= len(host) {
				continue
			}
			sxy += dev[i] * host[k]
			sxx += dev[i] * dev[i]
			syy += host[k] * host[k]
			n++
		}
		if n < 3 || sxx == 0 || syy == 0 {
			continue
		}
		if s := sxy / sqrt(sxx*syy); s > bestScore {
			best, bestScore = off, s
		}
	}
	return best, bestScore
}

func writeJoin(out *strings.Builder, r *run, full bool) {
	rows, score, ok := join(r)
	if !ok {
		return
	}
	fmt.Fprintf(out, "\nJOINED INTERVALS (%s, alignment score %.3f)\n", r.name, score)
	if score < 0.5 {
		fmt.Fprintf(out, "  ! alignment is weak; treat the pairing as unreliable\n")
	}
	show := rows
	if !full {
		sorted := append([]joined(nil), rows...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].PingMax > sorted[j].PingMax })
		if len(sorted) > 12 {
			sorted = sorted[:12]
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Elapsed < sorted[j].Elapsed })
		show = sorted
		fmt.Fprintf(out, "  (worst 12 by host RTT; -full for all %d)\n", len(rows))
	}
	fmt.Fprintf(out, "  %-9s %7s %10s %10s %7s %9s %9s %6s\n",
		"elapsed", "+rx", "svc max", "idle max", "pings", "rtt p99", "rtt max", "lost")
	for _, j := range show {
		fmt.Fprintf(out, "  %-9s %7d %10s %10s %7d %9s %9s %6d\n",
			fmtDur(j.Elapsed), j.RxDelta, fmtNS(float64(j.SvcMax)), fmtNS(float64(j.IdleMax)),
			j.PingN, fmtMS(j.PingP99), fmtMS(j.PingMax), j.PingLost)
	}
}

// ---------------------------------------------------------------- json

func emitJSON(runs []*run) error {
	type outRun struct {
		Name      string            `json:"name"`
		Bench     string            `json:"bench_log"`
		Ping      string            `json:"ping_log,omitempty"`
		Summary   map[string]string `json:"summary"`
		Warnings  []string          `json:"warnings"`
		Intervals []map[string]any  `json:"intervals"`
		Joined    []joined          `json:"joined,omitempty"`
	}
	var out []outRun
	for _, r := range runs {
		o := outRun{Name: r.name, Bench: r.bench.path, Summary: map[string]string{}, Warnings: warnings(r)}
		if r.ping != nil {
			o.Ping = r.ping.path
		}
		for _, m := range summarize(r) {
			o.Summary[m.group+"."+m.label] = m.str
		}
		for _, rec := range r.bench.recs {
			iv := map[string]any{"elapsed_s": int64(rec.elapsed.Seconds())}
			for k, v := range rec.v {
				iv[k] = v
			}
			if len(rec.hist) > 0 {
				h := map[string]int64{}
				for ub, n := range rec.hist {
					h[fmt.Sprint(ub)] = n
				}
				iv["svc.hist"] = h
			}
			o.Intervals = append(o.Intervals, iv)
		}
		if j, _, ok := join(r); ok {
			o.Joined = j
		}
		out = append(out, o)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---------------------------------------------------------------- formatting

func fmtInt(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.0f", v)
	var b []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, c)
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func fmtBytes(v float64) string {
	switch a := absf(v); {
	case a >= 1<<20:
		return fmt.Sprintf("%.2f MiB", v/(1<<20))
	case a >= 1<<10:
		return fmt.Sprintf("%.2f KiB", v/(1<<10))
	default:
		return fmt.Sprintf("%.0f B", v)
	}
}

func fmtSignedBytes(v float64) string {
	if v > 0 {
		return "+" + fmtBytes(v)
	}
	return fmtBytes(v)
}

func fmtNS(v float64) string {
	switch a := absf(v); {
	case a >= 1e9:
		return fmt.Sprintf("%.3f s", v/1e9)
	case a >= 1e6:
		return fmt.Sprintf("%.3f ms", v/1e6)
	case a >= 1e3:
		return fmt.Sprintf("%.2f us", v/1e3)
	default:
		return fmt.Sprintf("%.0f ns", v)
	}
}

func fmtMS(v float64) string { return fmt.Sprintf("%.3f ms", v) }

func fmtDur(d time.Duration) string {
	s := int64(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s/60)%60, s%60)
}

func absf(v float64) float64 { return math.Abs(v) }

func sqrt(v float64) float64 { return math.Sqrt(v) }
