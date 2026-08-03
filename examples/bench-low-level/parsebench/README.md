# parsebench

Turns a `bench-low-level` run — the device's report stream plus ping's output on
the host — into a summary, or a side-by-side comparison of two runs.

```sh
go run ./build/bench-low-level/parsebench build/bench-new.log build/newping.log
go run ./build/bench-low-level/parsebench build/bench-old2.log build/oldping.log \
                                          build/bench-new.log build/newping.log
```

Arguments are classified by content, not by position or extension; each ping log
attaches to the bench log before it. Two bench logs produce an A/B with the first
as baseline. `-full` prints every joined interval, `-json` the whole series.

## Parsing

Both formats are read by scanning tokens rather than matching lines, because
neither the line breaks nor the field order carries meaning. In the bench log two
sections already share a line (`idle[ns] … inter[ns] …`), and adding a counter to
the device shifts everything after it; in ping, a reply line is a bag of `k=v`
tokens and iputils moves the rest around between releases. A leading token names a
section and switches the namespace, every other token is a key whose value is the
token after it, and an unrecognised key costs one counter increment instead of a
mis-parse. Namespacing matters because names repeat — `max` belongs to `svc`,
`idle` and `inter`; `drift` to both `heap` and `arena` — and a flat map would
silently keep whichever came last. Lookups pass every namespace a value could
plausibly land in, so moving a counter in the device does not silently zero a
column here.

## Capture

```sh
ping -D -O -c 100000 -i 0.005 <device-ip> | tee build/<branch>ping.log
```

`-D` timestamps each line, which is the only thing that lets a host-side RTT spike
be joined to a device-side interval; without it the tool falls back to one
distribution for the whole run. `-O` emits a line for each packet not yet
answered — these are **not** losses and are reconciled against the replies by
sequence number, since at any interval shorter than the RTT most packets draw one.
What is left over is real loss; what is reconciled away is reported as
`rtt>interval`. `-c` fixes the work so two runs are comparable.

## Reading it

`CHECKS` is the part to read first. It flags a capture that started mid-run, a
`svc min` of 0 ns (the timer failed to advance across a send, so the low end is
resolution-bound), reply accounting that does not balance, and — for an A/B — an
offered load or run length too different between the two for the latency figures
to mean anything.

`svc` percentiles are printed as bounds because the device buckets by octave. With
a mean near 75 µs almost everything lands in the same `<131 µs` bucket, so p50, p90
and p99 collapse onto one value; sub-octave buckets on the device would be needed
to separate them.
