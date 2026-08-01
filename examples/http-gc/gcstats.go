// gcstats.go is the only thing this example adds to examples/http-app: a sampler
// that reports what the *Go* heap is doing while the HTTP server runs.  It exists
// to answer one question -- does a change to the driver change the load it puts on
// TinyGo's garbage collector -- by running the identical workload on two builds and
// comparing the numbers.
//
// It deliberately does not read espradio.Stats.  Those counters describe the blob's
// own arena, which is malloc'd outside the Go heap and is a separate question
// (examples/soak covers it).  Keeping this file free of them also means it compiles
// against a driver revision that predates them, which is what makes the A/B
// possible in the first place.
//
// What each sample reports, and why:
//
//   - live / obj -- the retained set, read immediately after a forced collection,
//     so it is reachable memory rather than reachable memory plus whatever garbage
//     had accumulated since the last cycle.  Drift here across hours of steady load
//     is a leak; that is the headline number.
//
//   - churn -- bytes and allocations since the previous sample.  This is the
//     pressure on the collector.  The heap size is fixed at link time, so the rate
//     at which a build allocates is the rate at which it forces collections, and a
//     build that allocates twice as much per request runs the GC twice as often
//     even if its retained set is identical.
//
//   - per-req -- churn divided by the connections handled in the interval.  This is
//     the figure to compare between branches, because it is independent of how many
//     requests each run happened to serve.
//
//   - est gc/hr -- churn rate divided by the free heap.  TinyGo's conservative
//     collector runs when an allocation cannot be satisfied, so this approximates
//     how often that happens.  It is a proxy, not a measurement: the runtime
//     exposes no cycle counter, and fragmentation makes the real figure somewhat
//     higher.  Compare it between builds; do not quote it as an absolute.
//
// Sampling costs allocations of its own -- fmt.Printf is not free.  Stats are read
// before the line is printed, so a sample's own cost lands in the next sample's
// churn.  It is a constant per sample and identical on both sides of a comparison,
// but it is the reason churn never reads zero on an idle radio.
package main

import (
	"fmt"
	"runtime"
	"time"
)

const (
	// gcSampleInterval is how often the heap is collected and reported.  Every
	// sample forces a GC, so this is also the collection cadence floor for the
	// whole program -- short enough for a readable series, long enough not to
	// become the workload.
	gcSampleInterval = 15 * time.Second

	// gcWarmupSamples is how many samples to discard before the baseline may be
	// latched.  Bring-up allocates once and counting that as drift would report a
	// leak on every run.  The baseline additionally waits for serverReady, so this
	// is a floor rather than the whole wait.
	gcWarmupSamples = 4
)

// reqCount is the number of connections handed to a worker.  Incremented by the
// worker goroutines and read by the sampler.
//
// Plain increment rather than sync/atomic: the ESP32-C3 is single-core and TinyGo
// schedules goroutines cooperatively, so no goroutine can be preempted between the
// load and the store.  Atomics on this target are also not free -- the chip has no
// A extension, so they compile to interrupt masking, which would be a real change
// to the timing of a path this test is trying to measure.
var reqCount uint32

// serverReady is set by main once the listener is up.  The baseline is not latched
// before this, so drift is measured against a stack that is fully constructed and
// idle rather than one still resolving DHCP.
var serverReady bool

// gcBaseline is the heap state that drift is measured against.
type gcBaseline struct {
	at   time.Time
	live uint64
	objs uint64
}

func gcReport() {
	start := time.Now()
	var m runtime.MemStats
	var base *gcBaseline
	var prevAlloc, prevMallocs uint64
	var prevReqs uint32
	var prevAt time.Time
	var maxLive, maxObjs uint64

	for n := 0; ; n++ {
		time.Sleep(gcSampleInterval)

		// Collect first, then read: HeapAlloc outside a collection includes garbage
		// that simply has not been swept yet, which would make the retained set look
		// like it was drifting when it was only sampling phase.
		runtime.GC()
		runtime.ReadMemStats(&m)
		now := time.Now()

		churn := m.TotalAlloc - prevAlloc
		mallocs := m.Mallocs - prevMallocs
		reqs := reqCount - prevReqs
		secs := int64(now.Sub(prevAt).Seconds())
		if secs < 1 {
			secs = 1
		}
		prevAlloc, prevMallocs, prevReqs, prevAt = m.TotalAlloc, m.Mallocs, reqCount, now

		if n < gcWarmupSamples || !serverReady {
			why := "warmup"
			if n >= gcWarmupSamples {
				why = "waiting-for-server"
			}
			fmt.Printf("gc %s  %s %d/%d  live %d B / %d obj  free %d B\r\n",
				gcElapsed(start), why, n+1, gcWarmupSamples, m.HeapAlloc, m.HeapObjects, m.HeapIdle)
			continue
		}

		if base == nil {
			base = &gcBaseline{at: now, live: m.HeapAlloc, objs: m.HeapObjects}
			maxLive, maxObjs = m.HeapAlloc, m.HeapObjects
			fmt.Printf("gc %s  BASELINE live %d B / %d obj  free %d B  heap %d B  gcmeta %d B\r\n",
				gcElapsed(start), m.HeapAlloc, m.HeapObjects, m.HeapIdle, m.Sys, m.GCSys)
			fmt.Printf("     start the load generator now; totals below are measured from here\r\n")
			continue
		}

		driftLive := int64(m.HeapAlloc) - int64(base.live)
		driftObjs := int64(m.HeapObjects) - int64(base.objs)
		if m.HeapAlloc > maxLive {
			maxLive = m.HeapAlloc
		}
		if m.HeapObjects > maxObjs {
			maxObjs = m.HeapObjects
		}

		// Per-hour extrapolation of the drift, in integer arithmetic so this stays
		// usable on builds without float formatting.
		sinceBase := int64(now.Sub(base.at).Seconds())
		if sinceBase < 1 {
			sinceBase = 1
		}
		fmt.Printf("gc %s  live %d B / %d obj  drift %+d B / %+d obj  per hr %+d B / %+d obj\r\n",
			gcElapsed(start), m.HeapAlloc, m.HeapObjects,
			driftLive, driftObjs,
			driftLive*3600/sinceBase, driftObjs*3600/sinceBase)

		// Churn is reported per interval rather than cumulatively so an idle phase
		// and a loaded phase can be read straight off the same series.
		fmt.Printf("     churn %d B / %d mallocs  %d B/s  reqs +%d (%d)",
			churn, mallocs, int64(churn)/secs, reqs, reqCount)
		if reqs > 0 {
			fmt.Printf("  per-req %d B / %d", churn/uint64(reqs), mallocs/uint64(reqs))
		}
		if m.HeapIdle > 0 {
			fmt.Printf("  est gc/hr %d", int64(churn)*3600/(secs*int64(m.HeapIdle)))
		}
		fmt.Printf("\r\n")

		// The peak is a sampled maximum taken after a forced collection, so it is a
		// lower bound on the true high-water mark: a spike that both began and ended
		// between two samples is invisible here.
		if maxLive > base.live || maxObjs > base.objs {
			fmt.Printf("     peak live %d B (+%d)  %d obj (+%d)\r\n",
				maxLive, maxLive-base.live, maxObjs, maxObjs-base.objs)
		}
	}
}

// gcElapsed formats a duration as hh:mm:ss.
func gcElapsed(since time.Time) string {
	d := int64(time.Since(since).Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", d/3600, (d/60)%60, d%60)
}
