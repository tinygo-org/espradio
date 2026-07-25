package httputil

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestThrottleHandler_LimitsConcurrency(t *testing.T) {
	const maxConn = 2
	var active int64
	var maxSeen int64

	gate := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&active, 1)
		defer atomic.AddInt64(&active, -1)
		// track peak concurrency
		for {
			old := atomic.LoadInt64(&maxSeen)
			if cur <= old || atomic.CompareAndSwapInt64(&maxSeen, old, cur) {
				break
			}
		}
		<-gate // block until test releases
		w.WriteHeader(http.StatusOK)
	})

	h := ThrottleHandler(maxConn, inner)

	// Launch maxConn requests that will block in handler.
	var wg sync.WaitGroup
	for i := 0; i < maxConn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
		}()
	}

	// Wait until both slots occupied.
	for atomic.LoadInt64(&active) < int64(maxConn) {
		// spin-wait; in practice converges instantly
	}

	// Extra request should get 503 (non-blocking).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	// Release blocked handlers.
	close(gate)
	wg.Wait()

	if atomic.LoadInt64(&maxSeen) > int64(maxConn) {
		t.Errorf("peak concurrency %d exceeded maxConn %d", maxSeen, maxConn)
	}
}

func TestThrottleHandler_SetsConnectionClose(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := ThrottleHandler(1, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection header = %q, want %q", got, "close")
	}
}

func TestThrottleHandler_503SetsConnectionClose(t *testing.T) {
	gate := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
	})
	h := ThrottleHandler(1, inner)

	// Occupy the single slot.
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}()

	// Wait for slot to be taken (handler blocks on gate).
	// Send another request synchronously — it should get 503.
	// We need the first goroutine to actually enter the handler.
	// Use a small sync mechanism:
	occupied := make(chan struct{})
	innerWithSignal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(occupied)
		<-gate
	})
	h2 := ThrottleHandler(1, innerWithSignal)

	go func() {
		req := httptest.NewRequest(http.MethodGet, "/first", nil)
		rec := httptest.NewRecorder()
		h2.ServeHTTP(rec, req)
	}()
	<-occupied

	req := httptest.NewRequest(http.MethodGet, "/second", nil)
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if got := rec.Header().Get("Connection"); got != "close" {
		t.Errorf("503 response Connection header = %q, want %q", got, "close")
	}
	close(gate)
}

func TestThrottleHandler_NilHandlerUsesDefaultMux(t *testing.T) {
	// Register a handler on DefaultServeMux for this test.
	pattern := "/httputil-test-nil-handler"
	http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	h := ThrottleHandler(1, nil)
	req := httptest.NewRequest(http.MethodGet, pattern, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestThrottleHandler_MaxConnClampedToOne(t *testing.T) {
	var called int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&called, 1)
		w.WriteHeader(http.StatusOK)
	})

	// maxConn=0 should be treated as 1.
	h := ThrottleHandler(0, inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if atomic.LoadInt64(&called) != 1 {
		t.Error("handler was not called")
	}
}
