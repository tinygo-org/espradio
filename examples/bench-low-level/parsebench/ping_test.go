package main

import (
	"strings"
	"testing"
)

// icmp_seq is 16 bits, so a long run reuses it -- the captured runs wrapped three
// and four times.  Reconciling on the raw value lets a packet answered before a
// wrap cancel a different packet lost after one, which silently understates loss.
func TestReconcileAcrossSeqWrap(t *testing.T) {
	const log = `[1.0] 64 bytes from 10.0.0.1: icmp_seq=5 ttl=64 time=1.00 ms
[1.1] 64 bytes from 10.0.0.1: icmp_seq=65535 ttl=64 time=1.00 ms
[1.2] no answer yet for icmp_seq=5
`
	p, err := parsePing("test", strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	// The unanswered seq 5 is the one after the wrap, a different packet from the
	// seq 5 answered before it.
	if got := p.lost(); got != 1 {
		t.Errorf("lost = %d, want 1", got)
	}
	if p.delayed != 0 {
		t.Errorf("delayed = %d, want 0", p.delayed)
	}
	if want := 65536 + 5; p.maxSeq != want {
		t.Errorf("maxSeq = %d, want %d", p.maxSeq, want)
	}
	if got := p.transmitted(); got != 65541 {
		t.Errorf("transmitted = %d, want 65541", got)
	}
}

// A "no answer yet" line is not a loss.  At any interval shorter than the round
// trip most packets draw one and are answered moments later.
func TestNoAnswerReconciledAsDelayed(t *testing.T) {
	const log = `[1.0] no answer yet for icmp_seq=1
[1.1] 64 bytes from 10.0.0.1: icmp_seq=1 ttl=64 time=21.0 ms
[1.2] no answer yet for icmp_seq=2
`
	p, err := parsePing("test", strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if p.delayed != 1 {
		t.Errorf("delayed = %d, want 1", p.delayed)
	}
	if got := p.lost(); got != 1 {
		t.Errorf("lost = %d, want 1", got)
	}
}

// The trailer puts a word between each count and its label, and how many has
// changed between iputils releases.
func TestTrailerCounts(t *testing.T) {
	const log = "204645 packets transmitted, 203774 received, 0.425615% packet loss, time 1227953ms\n"
	p, err := parsePing("test", strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if p.sent != 204645 || p.recv != 203774 {
		t.Errorf("sent/recv = %d/%d, want 204645/203774", p.sent, p.recv)
	}
	if p.lossPct != 0.425615 {
		t.Errorf("lossPct = %v, want 0.425615", p.lossPct)
	}
	if got := p.duration.Seconds(); got < 1227 || got > 1228 {
		t.Errorf("duration = %v, want ~1227.953s", p.duration)
	}
	// The trailer wins over the derived figure when both are available.
	if got := p.lost(); got != 871 {
		t.Errorf("lost = %d, want 871", got)
	}
}

// The trailer's statistic groups vary with the mode ping ran in.  Plain runs end
// with "pipe N"; flood runs replace it with "ipg/ewma", the achieved inter-packet
// gap -- the only direct evidence in the log that the interval asked for is the
// interval that was sent at.
func TestScanRTTGroups(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want map[string]float64
	}{{
		name: "pipe",
		line: "rtt min/avg/max/mdev = 2.271/11.464/329.064/17.915 ms, pipe 26",
		want: map[string]float64{"min": 2.271, "avg": 11.464, "max": 329.064, "mdev": 17.915},
	}, {
		name: "ipg/ewma",
		line: "rtt min/avg/max/mdev = 0.004/0.038/0.133/0.031 ms, ipg/ewma 4.995/0.043 ms",
		want: map[string]float64{
			"min": 0.004, "avg": 0.038, "max": 0.133, "mdev": 0.031,
			"ipg": 4.995, "ewma": 0.043,
		},
	}, {
		// Some builds label the fourth statistic stddev and omit the "=".
		name: "stddev, no equals",
		line: "round-trip min/avg/max/stddev 1.0/2.0/3.0/4.0 ms",
		want: map[string]float64{"min": 1.0, "avg": 2.0, "max": 3.0, "stddev": 4.0},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parsePing("test", strings.NewReader(tc.line+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.want {
				got, ok := p.rtt[k]
				if !ok {
					t.Errorf("%q missing", k)
					continue
				}
				if got != want {
					t.Errorf("%q = %v, want %v", k, got, want)
				}
			}
			if len(p.rtt) != len(tc.want) {
				t.Errorf("got %d statistics %v, want %d", len(p.rtt), p.rtt, len(tc.want))
			}
		})
	}
}
