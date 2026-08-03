//go:build espradio_stats

package main

import "tinygo.org/x/espradio"

// espradio.ReadStats exists only on revisions that added the driver counters, so
// it lives behind a tag.  The default build has to compile against the revision
// being compared to as well, and a comparison you cannot build is not one.
//
// Turn it on with -tags espradio_stats for the run that supports it: RxDrops,
// TxRetries, SchedPassesPerSec and HWISRPerSec are what explain a difference in
// the figures the untagged build reports on both sides.
func driverStats() {
	var s espradio.Stats
	espradio.ReadStats(&s)
	s.Print()
}
