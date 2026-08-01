# http-gc — comparing Go GC load across two driver builds

This is `examples/http-app` with a Go heap sampler attached. It exists to answer a
single question: **does a change to the driver change the load the application puts
on TinyGo's garbage collector?** You answer it by running the identical workload on
two builds of the driver and comparing the numbers.

The server is a verbatim copy of `examples/http-app` — three statements differ, each
marked `gcstats:` — and it should stay that way. If the example's own allocation
behaviour drifts from http-app's, the comparison stops describing the driver.

## What is and is not measured

Measured: the **Go** heap. Retained bytes and objects after a forced collection,
cumulative allocation churn, and both of those divided by connections served.

Not measured: the blob's arena. That memory is `malloc`'d outside the Go heap and
never touches the GC; it is a separate question with its own soak in
`examples/soak`. `gcstats.go` deliberately does not import `espradio.Stats`, which
is also what lets this example compile against driver revisions that predate those
counters — a requirement for any A/B against an older branch.

## Procedure

Both runs must differ in exactly one thing: the driver. Same board, same AP, same
TinyGo version, same request count. A run on a congested 2.4 GHz channel retries
more, allocates more, and will read as a regression that is not there — so run the
two back to back rather than on different days.

**1. Run the branch under test.**

```sh
tinygo flash -target xiao-esp32c3 \
  -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" \
  -monitor ./examples/http-gc | tee /tmp/gc-branch.log
```

Wait for the `BASELINE` line. It appears once the listener is up and warmup has
passed, roughly a minute in, and everything after it is measured from there. Note
the IP printed at startup.

**2. Apply the load**, from another terminal, once the baseline has printed:

```sh
./examples/http-gc/loadgen.sh 192.168.1.42 2000 2
```

The load is a fixed request *count*, not a duration — see the comment at the top of
`loadgen.sh` for why that distinction decides whether the comparison is valid.

Let the device keep sampling for a few minutes after the load finishes. The
post-load samples are what show whether the retained set comes back down; a drift
figure read at the moment load stops is still holding connection state.

**3. Repeat on the other branch.** A worktree avoids disturbing your working tree,
and the example is untracked, so copy it across:

```sh
git worktree add /tmp/espradio-main main
cp -r examples/http-gc /tmp/espradio-main/examples/
cd /tmp/espradio-main
tinygo flash -target xiao-esp32c3 \
  -ldflags="-X main.ssid=YourSSID -X main.password=YourPassword" \
  -monitor ./examples/http-gc | tee /tmp/gc-main.log
```

Then run `loadgen.sh` again with the **same request count and concurrency**.

## Reading the result

Compare, in this order:

1. **`per-req` churn.** The primary figure. Bytes and mallocs per connection is
   independent of how many requests each run happened to serve, so it compares
   directly. A difference here means the driver allocates differently on the packet
   path, and it scales with traffic.
2. **`drift` per hour.** Retained growth under steady load. Non-zero and roughly
   constant on one branch but not the other is a leak introduced by that branch.
   Both runs should be long enough for this to be more than sampling noise.
3. **`live` at BASELINE.** A one-time difference in retained memory after bring-up.
   Costs heap headroom but does not compound.
4. **`est gc/hr`.** A proxy derived from churn and free heap — the runtime exposes
   no cycle counter. Compare between builds; do not quote it as an absolute.

A `per-req` difference under about 5% is not worth acting on: the callsign ring
buffer fills during the first samples and lengthens the landing page, and TCP
retries vary with RF conditions. Both move churn by a few percent between otherwise
identical runs.

## Static RAM

Worth recording alongside the heap numbers, because the Go heap gets what static
allocation leaves behind — more `bss` means a smaller heap, which means more
frequent collections at the same churn rate. Measured for this example on
`xiao-esp32c3` with TinyGo 0.42.0-dev:

| build              | code    | data   | bss    | ram     |
|--------------------|---------|--------|--------|---------|
| `main` (2e21d40)   | 877,422 | 31,988 | 101,692| 133,680 |
| `wifi-improvements`| 878,982 | 32,008 | 102,144| 134,152 |

That is 472 bytes of heap given up on the branch — small enough not to move the GC
cadence on its own, but re-measure it if the branch grows, with:

```sh
tinygo build -size short -target xiao-esp32c3 \
  -ldflags="-X main.ssid=X -X main.password=Y" -o /dev/null ./examples/http-gc
```
