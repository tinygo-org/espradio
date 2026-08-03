// The instrument.  Two rules shape all of it.
//
// It never calls runtime.GC.  A forced collection before every read hides the
// garbage that has accumulated since the last cycle and, worse, pins the
// collection cadence to the sampler's own period -- which is how you end up
// having to estimate a GC rate instead of counting one.  Left alone, HeapAlloc
// traces a sawtooth: it only ever rises between collections, because TinyGo's
// free() is a no-op and blocks come back solely through the collector.  Sampling
// it faster than the sawtooth and counting the drops is an exact collection count.
//
// It never allocates.  Every figure is integer arithmetic into a fixed array and
// every line is printed through unsafe.String, because a sampler that allocates
// shows up in the numbers it is reporting.
package main

import (
	"math/bits"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"tinygo.org/x/espradio"
)

const (
	// heapTick has to be short against the sawtooth, since a collection that
	// begins and ends between two reads is a collection this program does not
	// count.  ReadMemStats walks the GC metadata and allocates nothing; its cost
	// is measured at baseline and printed so the overhead is not left to trust.
	heapTick = 100 * time.Millisecond

	// reportTick is the reporting period.  Long enough that the UART is not the
	// workload, short enough to tell a loaded phase from an idle one by eye.
	reportTick = 5 * time.Second
)

// Cumulative traffic counters.  Single-core, cooperatively scheduled, and all
// written from the one loop goroutine, so plain integers: sync/atomic on RISC-V
// without the A extension compiles to interrupt masking, which would perturb the
// timing this program is measuring.
var (
	nrx, ntx         int64
	rxBytes, txBytes int64
	rxErr, txErr     int64
	noReply          int64 // frames received that this program does not answer
)

// lat accumulates a duration series without storing it.
type lat struct {
	n        int64
	min, max int64
	sum      int64
}

func (l *lat) add(d time.Duration) {
	ns := int64(d)
	if ns < 0 {
		return
	}
	if l.n == 0 || ns < l.min {
		l.min = ns
	}
	if ns > l.max {
		l.max = ns
	}
	l.sum += ns
	l.n++
}

func (l *lat) avg() int64 {
	if l.n == 0 {
		return 0
	}
	return l.sum / l.n
}

func (l *lat) reset() { *l = lat{} }

var (
	// service is EthPoll handing over a frame until SendEthFrame returns: the TX
	// path with nothing else in it.
	service lat
	// idlePeriod is the wall period of a loop iteration that found no frame.  It
	// asks for idleSleep; the gap between that and what it gets is the scheduler.
	idlePeriod lat
	// interRx is frame arrival to frame arrival, which is the offered load rather
	// than anything the driver controls -- it is here to prove the two runs of a
	// comparison were actually driven the same way.
	interRx lat
)

// svcHist buckets service latency by power of two nanoseconds: index k holds
// samples below 1<<k ns.  An average hides the tail, and the tail is what an
// interrupt storm moves.
var svcHist [24]uint32

// noteService records one TX-path duration in both the accumulator and the
// histogram.  bits.Len is the bucket index directly -- it is the number of bits
// needed to hold the value, so a sample lands in the bucket whose label is the
// first power of two above it.
func noteService(d time.Duration) {
	service.add(d)
	k := bits.Len64(uint64(d))
	if k >= len(svcHist) {
		k = len(svcHist) - 1
	}
	svcHist[k]++
}

func noteRx(at time.Time, n int) {
	if !lastRx.IsZero() {
		interRx.add(at.Sub(lastRx))
	}
	lastRx = at
	nrx++
	rxBytes += int64(n)
}

var lastRx time.Time

// heapSnap is everything worth reading about memory at one instant.  ArenaStats
// is in here because runtime.MemStats alone cannot see driver memory: espradio
// takes one large []byte from the Go heap and arena.c sub-allocates the blob's
// every request inside it, so a driver leak moves ArenaUsed and leaves Mallocs
// and TotalAlloc exactly where they were.
type heapSnap struct {
	totalAlloc uint64
	mallocs    uint64
	heapAlloc  uint64
	heapObjs   uint64
	heapIdle   uint64
	arenaUsed  uint32
	arenaCap   uint32
}

func readHeap() (s heapSnap) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.totalAlloc, s.mallocs = m.TotalAlloc, m.Mallocs
	s.heapAlloc, s.heapObjs, s.heapIdle = m.HeapAlloc, m.HeapObjects, m.HeapIdle
	s.arenaUsed, s.arenaCap = espradio.ArenaStats()
	return s
}

var (
	startAt      time.Time
	base         heapSnap // latched after bring-up, before traffic
	prev         heapSnap // previous report, for interval deltas
	prevNRx      int64
	prevNTx      int64
	lastHeapPoll uint64 // HeapAlloc at the last heapTick, for the sawtooth
	gcCycles     int64
	prevGCCycles int64
	peakAlloc    uint64
	minIdle      uint64
	memStatsCost time.Duration
	nextHeap     time.Time
	nextReport   time.Time
)

// statsBaseline latches the reference point.  Called once the radio is up and
// associated and before any traffic, so drift is measured against a
// fully-constructed idle stack rather than one still bringing itself up.
func statsBaseline() {
	t0 := time.Now()
	s := readHeap()
	memStatsCost = time.Since(t0)

	startAt = t0
	base, prev = s, s
	lastHeapPoll = s.heapAlloc
	peakAlloc = s.heapAlloc
	minIdle = s.heapIdle
	nextHeap = t0.Add(heapTick)
	nextReport = t0.Add(reportTick)

	dst := outBuf[:0]
	dst = append(dst, "bench BASELINE "...)
	dst = kv(dst, "heapalloc", int64(s.heapAlloc))
	dst = kv(dst, "heapobj", int64(s.heapObjs))
	dst = kv(dst, "heapidle", int64(s.heapIdle))
	dst = kv(dst, "arenaused", int64(s.arenaUsed))
	dst = kv(dst, "arenacap", int64(s.arenaCap))
	dst = kv(dst, "memstats_ns", int64(memStatsCost))
	flush(dst)
}

// sample services both timers.  It is called from the loop rather than from a
// goroutine of its own: another goroutine would add a heap-allocated stack and a
// second participant to the scheduler, both of which this program is measuring.
func sample(now time.Time) {
	if now.Before(nextHeap) {
		return
	}
	nextHeap = now.Add(heapTick)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// A drop can only be a collection: free() is a no-op on this GC, so blocks
	// come back one way and one way only.
	if m.HeapAlloc < lastHeapPoll {
		gcCycles++
	}
	lastHeapPoll = m.HeapAlloc
	if m.HeapAlloc > peakAlloc {
		peakAlloc = m.HeapAlloc
	}
	if m.HeapIdle < minIdle {
		minIdle = m.HeapIdle
	}

	if now.Before(nextReport) {
		return
	}
	nextReport = now.Add(reportTick)
	report(now)
}

func report(now time.Time) {
	s := readHeap()

	dst := outBuf[:0]
	dst = appendElapsed(dst, now.Sub(startAt))
	dst = kv(dst, "rx", nrx)
	dst = kv(dst, "+rx", nrx-prevNRx)
	dst = kv(dst, "rxB", rxBytes)
	dst = kv(dst, "tx", ntx)
	dst = kv(dst, "+tx", ntx-prevNTx)
	dst = kv(dst, "txB", txBytes)
	dst = kv(dst, "norep", noReply)
	dst = kv(dst, "rxerr", rxErr)
	dst = kv(dst, "txerr", txErr)
	flush(dst)

	flush(appendReplyStats(outBuf[:0]))

	dst = outBuf[:0]
	dst = append(dst, "  svc[ns] "...)
	dst = kv(dst, "n", service.n)
	dst = kv(dst, "min", service.min)
	dst = kv(dst, "avg", service.avg())
	dst = kv(dst, "max", service.max)
	flush(dst)

	if service.n > 0 {
		dst = outBuf[:0]
		dst = append(dst, "  svc<ns "...)
		for k, c := range svcHist {
			if c == 0 {
				continue
			}
			dst = strconv.AppendInt(dst, int64(1)<<uint(k), 10)
			dst = append(dst, ':')
			dst = strconv.AppendUint(dst, uint64(c), 10)
			dst = append(dst, ' ')
		}
		flush(dst)
	}

	dst = outBuf[:0]
	dst = append(dst, "  idle[ns] "...)
	dst = kv(dst, "n", idlePeriod.n)
	dst = kv(dst, "avg", idlePeriod.avg())
	dst = kv(dst, "max", idlePeriod.max)
	dst = append(dst, " inter[ns] "...)
	dst = kv(dst, "n", interRx.n)
	dst = kv(dst, "avg", interRx.avg())
	dst = kv(dst, "max", interRx.max)
	flush(dst)

	dst = outBuf[:0]
	dst = append(dst, "  heap "...)
	dst = kv(dst, "alloc", int64(s.heapAlloc))
	dst = kv(dst, "obj", int64(s.heapObjs))
	dst = kv(dst, "idle", int64(s.heapIdle))
	dst = kv(dst, "peak", int64(peakAlloc))
	dst = kv(dst, "minidle", int64(minIdle))
	dst = kv(dst, "drift", int64(s.heapAlloc)-int64(base.heapAlloc))
	flush(dst)

	dst = outBuf[:0]
	dst = append(dst, "  churn "...)
	dst = kv(dst, "B", int64(s.totalAlloc-prev.totalAlloc))
	dst = kv(dst, "mallocs", int64(s.mallocs-prev.mallocs))
	dst = append(dst, " since_base "...)
	dst = kv(dst, "B", int64(s.totalAlloc-base.totalAlloc))
	dst = kv(dst, "mallocs", int64(s.mallocs-base.mallocs))
	dst = kv(dst, "gc", gcCycles)
	dst = kv(dst, "+gc", gcCycles-prevGCCycles)
	flush(dst)

	dst = outBuf[:0]
	dst = append(dst, "  arena "...)
	dst = kv(dst, "used", int64(s.arenaUsed))
	dst = kv(dst, "cap", int64(s.arenaCap))
	dst = kv(dst, "drift", int64(s.arenaUsed)-int64(base.arenaUsed))
	flush(dst)

	driverStats()

	// Intervals reset, cumulatives do not: an idle phase and a loaded phase then
	// read straight off the same series instead of being averaged together.
	service.reset()
	idlePeriod.reset()
	interRx.reset()
	for i := range svcHist {
		svcHist[i] = 0
	}
	prev, prevNRx, prevNTx, prevGCCycles = s, nrx, ntx, gcCycles
}

func printIdentity(mac [6]byte, ip [4]byte) {
	dst := outBuf[:0]
	dst = append(dst, "bench mac "...)
	for i, b := range mac {
		if i > 0 {
			dst = append(dst, ':')
		}
		dst = appendHex8(dst, b)
	}
	dst = append(dst, " ip "...)
	for i, b := range ip {
		if i > 0 {
			dst = append(dst, '.')
		}
		dst = strconv.AppendUint(dst, uint64(b), 10)
	}
	flush(dst)
}

// outBuf backs every line.  Sized so no line can grow it -- an append past cap
// would allocate, which is the one thing this file must not do.
var outBuf [768]byte

func flush(dst []byte) {
	if len(dst) == 0 {
		return
	}
	print(unsafe.String(&dst[0], len(dst)))
	print("\r\n")
}

func kv(dst []byte, k string, v int64) []byte {
	dst = append(dst, k...)
	dst = append(dst, ' ')
	dst = strconv.AppendInt(dst, v, 10)
	dst = append(dst, ' ')
	return dst
}

func appendElapsed(dst []byte, d time.Duration) []byte {
	s := int64(d / time.Second)
	dst = append(dst, "bench "...)
	dst = appendPad2(dst, s/3600)
	dst = append(dst, ':')
	dst = appendPad2(dst, (s/60)%60)
	dst = append(dst, ':')
	dst = appendPad2(dst, s%60)
	dst = append(dst, ' ')
	return dst
}

func appendPad2(dst []byte, v int64) []byte {
	if v < 10 {
		dst = append(dst, '0')
	}
	return strconv.AppendInt(dst, v, 10)
}

func appendHex8(dst []byte, b byte) []byte {
	const hexdigits = "0123456789abcdef"
	return append(dst, hexdigits[b>>4], hexdigits[b&0xf])
}
