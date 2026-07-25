// Package httputil provides HTTP utilities for memory-constrained espradio devices.
//
// The primary utility is [ThrottleHandler], a concurrency limiter designed for
// ESP32-class microcontrollers where RAM is scarce and unbounded goroutine
// creation from incoming HTTP requests can exhaust the heap. It uses a
// non-blocking semaphore so excess requests are shed immediately (503) rather
// than queued, keeping memory usage deterministic.
//
// Typical usage on an ESP32 with ~200 KB usable RAM:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/", handler)
//	err := http.ListenAndServe(":80", httputil.ThrottleHandler(2, mux))
package httputil

import (
	"net/http"
)

// ThrottleHandler returns an http.Handler that limits concurrent request
// processing to maxConn. When all slots are occupied, excess requests receive
// 503 Service Unavailable immediately with minimal memory allocation.
//
// All responses include a "Connection: close" header to discourage HTTP/1.1
// keep-alive pipelining on memory-constrained devices.
//
// If handler is nil, [http.DefaultServeMux] is used. If maxConn <= 0 it is
// clamped to 1.
func ThrottleHandler(maxConn int, handler http.Handler) http.Handler {
	if handler == nil {
		handler = http.DefaultServeMux
	}
	if maxConn <= 0 {
		maxConn = 1
	}
	sem := make(chan struct{}, maxConn)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			w.Header().Set("Connection", "close")
			handler.ServeHTTP(w, r)
		default:
			w.Header().Set("Connection", "close")
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}
	})
}
