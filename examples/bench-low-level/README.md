# bench-low-level

Measures espradio with no stack between the instrument and the driver: brings the
radio up, associates, then polls the raw `NetDev` and answers ARP and ICMP echo in
place. Depends only on `espradio`, `strconv`, `time`, `unsafe`, `math/bits`.

## Why it is built this way

**Driver memory is invisible to `runtime.MemStats`.** espradio hands the blob one
48 KB `[]byte` from the Go heap ([arena_pool_default.go](../../arena_pool_default.go))
and `arena.c` sub-allocates every blob request inside it, so a driver that leaks
leaves `Mallocs` and `TotalAlloc` exactly where they were. `espradio.ArenaStats()`
is sampled alongside `MemStats`; it is the only view of that memory, and it exists
on revisions predating `ReadStats`, which is what makes an A/B against `main`
possible.

**Forcing a collection destroys the measurement.** This program never calls
`runtime.GC`. `HeapAlloc` only rises between collections — TinyGo's `free()` is a
no-op — so it traces a sawtooth, and counting the drops at a 100 ms tick is an
exact collection count rather than an estimate.

It also never allocates: integer arithmetic into fixed arrays, printed through
`unsafe.String`.

## Run

```sh
tinygo flash -target xiao-esp32c3 -monitor \
  -ldflags="-X main.ssid=SSID -X main.password=PASS -X main.ip=192.168.1.99" \
  ./examples/bench-low-level | tee build/results/bench-new.log
```

`main.ip` is static — DHCP needs the stack this program avoids. Pick an address
outside the AP's pool. A gratuitous ARP goes out every 2 s so the AP learns the
binding unprompted.

Load from the host once `ready:` prints:

```sh
ping -D -O -c 100000 -i 0.005 192.168.1.99 | tee build/results/newping.log
```

`-D` timestamps each line, which is what lets a host RTT spike be joined to a
device interval. `-O` marks packets not yet answered; these are **not** losses and
are reconciled by sequence number. `-c` fixes the work so two runs compare.

**`-i` alone stops pacing before you think it does.** Printing a line per packet
costs enough that ping falls behind its own timer: `-i 0.002` asked for 500
frame/s and delivered 167–184. Worse for an A/B, the rate it does reach tracks the
firmware under test, because device RTT feeds back into ping's loop — so two
branches run at two different loads and the RTT comparison is confounded.

Add `-f` to fix the pacing. With a non-zero interval it needs no root, and it
replaces per-packet lines with dots, so the interval is met exactly:

```sh
ping -f -i 0.005 -c 300000 192.168.1.99      # 500 frame/s, paced
```

The trailer then reports `ipg`, the gap actually achieved — check it against what
you asked for. The cost is that there are no per-packet lines left to timestamp,
so `parsebench` gets the summary but cannot join RTT to device intervals. Use
`-D -O` at `-i 0.01` when you want the joined timeline, `-f -i` when you want load.

Nothing so far has overrun the device — loss stayed at 0.4% and `rx` tracked
packets sent — so its ceiling is still unmeasured. `-f`, `-s 1472` (15× the bytes
at the same packet rate; the default 98 B frame never exercises the TX path at
MTU), or two pingers from separate hosts are the ways to find it.

Analyse with [parsebench](../../build/bench-low-level/parsebench/):

```sh
go run ./build/bench-low-level/parsebench \
  build/results/bench-old.log build/results/oldping.log \
  build/results/bench-new.log build/results/newping.log
```

Before flashing, `-print-allocs='tinygo.org/x/espradio.*'` answers "does the driver
allocate on the Go heap" at compile time, with no hardware.

## Output

Every 5 s. Interval figures reset, cumulative ones do not.

| line | meaning |
|---|---|
| `bench` | cumulative rx/tx frames and bytes, unanswered, errors |
| `saw` | frame classification — says *why* a quiet log is quiet |
| `svc[ns]` | `EthPoll` handing over a frame until `SendEthFrame` returns: the TX path, nothing else |
| `svc<ns` | octave histogram of the above |
| `idle[ns]` | period of a loop iteration that found no frame. Asks for 1 ms; the excess is scheduler overhead |
| `inter[ns]` | arrival to arrival — the offered load, to show two runs were driven alike |
| `heap` | `HeapAlloc`/objects/free, peak, drift from baseline |
| `churn` | `TotalAlloc` and `Mallocs` deltas: exact, collector-independent, the only place Go-heap allocation can appear |
| `arena` | blob memory used/capacity/drift |

Limits: `svc` percentiles collapse when the mean sits inside one octave —
sub-octave buckets would separate them. A `svc min` of 0 ns means the timer did not
advance across a send.

## Build tags

- `l2echo` — bounce every frame back with the MACs swapped, no protocol parsing.
  Needs a raw-socket sender instead of ping, and answers broadcast, so two boards
  on one AP will echo each other until the channel is full.
- `espradio_stats` — also dump `espradio.ReadStats()`. Branch-only; the default
  build compiles against `main`.
