#!/usr/bin/env bash
#
# loadgen.sh drives examples/http-gc with a fixed, repeatable workload so the two
# runs of an A/B comparison see the same load.
#
# Repeatability is the whole point, so the load is defined by a request count
# rather than by a duration.  A slower build served for a fixed time would handle
# fewer requests and allocate less in total, which would read as an improvement.
# Fixing the count instead means both runs do identical work and the per-request
# figures in the device's output are directly comparable.
#
#   ./loadgen.sh 192.168.1.42              # 2000 requests, 2 at a time
#   ./loadgen.sh 192.168.1.42 5000 4       # longer run, full pool concurrency
#
# Concurrency defaults to 2 and should not exceed the server's maxConns (4).  Past
# that, requests queue in the listener and the run measures the queue rather than
# the server.
#
# Requests alternate between the landing page and /toggle-led, which is what makes
# both response paths -- the large templated body and the empty one -- part of the
# measurement.  The callsign cycles through a fixed set so the action ring buffer
# stays populated and the landing page keeps rendering its full list; an empty ring
# would render a much smaller page and understate the allocation per request.
set -u

host=${1:-}
total=${2:-2000}
conc=${3:-2}

if [ -z "$host" ]; then
	echo "usage: $0 <device-ip> [requests] [concurrency]" >&2
	exit 2
fi

# --max-time bounds a request that the device drops rather than refuses, so a
# wedged server ends the run instead of hanging it.  -o /dev/null keeps the body
# off the terminal without discarding the transfer itself: the server only frees
# the connection once the body has gone out, so the read has to happen.  -f turns
# a non-2xx into a failure, because both URLs this sends should answer 200 -- a run
# quietly serving 404s would allocate nothing like a real one and must not pass for
# a valid measurement.
curl_one() {
	curl -sS -f -o /dev/null --max-time 10 "$1" || echo "request failed: $1" >&2
}

callsigns=(ABCD EFGH IJKL MNOP)
start=$(date +%s)
sent=0
# A threshold rather than a modulo on the count: a wave can step straight over any
# given multiple when the concurrency does not divide it, which would silently drop
# progress lines.
report_every=200
next_report=$report_every

echo "loadgen: $total requests to $host at concurrency $conc"

while [ "$sent" -lt "$total" ]; do
	# One wave of $conc requests, then wait.  Waves keep the in-flight count at or
	# below the pool size without needing a job-control scheduler, and make the
	# total exact rather than approximate.
	for _ in $(seq 1 "$conc"); do
		if [ "$sent" -ge "$total" ]; then break; fi
		if [ $((sent % 2)) -eq 0 ]; then
			curl_one "http://$host/" &
		else
			cs=${callsigns[$(((sent / 2) % ${#callsigns[@]}))]}
			curl_one "http://$host/toggle-led?callsign=$cs" &
		fi
		sent=$((sent + 1))
	done
	wait

	if [ "$sent" -ge "$next_report" ]; then
		elapsed=$(($(date +%s) - start))
		[ "$elapsed" -eq 0 ] && elapsed=1
		echo "loadgen: $sent/$total  ${elapsed}s  $((sent / elapsed)) req/s"
		next_report=$((sent + report_every))
	fi
done

elapsed=$(($(date +%s) - start))
[ "$elapsed" -eq 0 ] && elapsed=1
echo "loadgen: done  $sent requests in ${elapsed}s  $((sent / elapsed)) req/s"
