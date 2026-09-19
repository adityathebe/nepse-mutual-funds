package sources

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	m.Run()
}

// serve counts requests and lets each case decide what the official site returns.
func serve(t *testing.T, handler func(attempt int64, w http.ResponseWriter)) (*httptest.Server, *int64) {
	t.Helper()
	var attempts int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler == nil {
			return
		}
		handler(atomic.AddInt64(&attempts, 1), w)
	}))
	t.Cleanup(server.Close)
	return server, &attempts
}

func TestRequestRetriesTransientStatus(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusBadGateway, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server, attempts := serve(t, func(attempt int64, w http.ResponseWriter) {
				if attempt == 1 {
					w.WriteHeader(status)
					return
				}
				io.WriteString(w, `{"nav":"10.5"}`)
			})
			var result struct {
				NAV string `json:"nav"`
			}
			if err := request(t.Context(), server.Client(), server.URL, nil, &result); err != nil {
				t.Fatalf("request: %v", err)
			}
			if result.NAV != "10.5" {
				t.Errorf("nav = %q, want 10.5", result.NAV)
			}
			if got := atomic.LoadInt64(attempts); got != 2 {
				t.Errorf("attempts = %d, want 2", got)
			}
		})
	}
}

func TestRequestDoesNotRetryPermanentStatus(t *testing.T) {
	server, attempts := serve(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := request(t.Context(), server.Client(), server.URL, nil, &struct{}{})
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) || statusErr.status != http.StatusNotFound {
		t.Fatalf("err = %v, want HTTP 404", err)
	}
	if got := atomic.LoadInt64(attempts); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// A malformed payload is the source's answer, not a transport fault, so retrying it
// would only re-decode the same bytes into an already-populated result.
func TestRequestDoesNotRetryInvalidJSON(t *testing.T) {
	server, attempts := serve(t, func(_ int64, w http.ResponseWriter) {
		io.WriteString(w, "not json")
	})
	if err := request(t.Context(), server.Client(), server.URL, nil, &struct{}{}); err == nil {
		t.Fatal("request: expected an error")
	}
	if got := atomic.LoadInt64(attempts); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestRequestReturnsLastErrorWhenAttemptsExhausted(t *testing.T) {
	server, attempts := serve(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	err := request(t.Context(), server.Client(), server.URL, nil, &struct{}{})
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) || statusErr.status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want HTTP 503", err)
	}
	if got := atomic.LoadInt64(attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

// The per-source deadline must cut the backoff short rather than outlive it.
func TestRequestStopsRetryingOnContextCancellation(t *testing.T) {
	server, attempts := serve(t, func(_ int64, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadGateway)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if err := request(ctx, server.Client(), server.URL, nil, &struct{}{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
	if got := atomic.LoadInt64(attempts); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// A dropped connection is the failure mode that cost a full source in CI.
func TestRequestRetriesTransportFailure(t *testing.T) {
	var attempts int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&attempts, 1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
			return
		}
		io.WriteString(w, `{"nav":"9.1"}`)
	}))
	defer server.Close()
	var result struct {
		NAV string `json:"nav"`
	}
	if err := request(t.Context(), server.Client(), server.URL, nil, &result); err != nil {
		t.Fatalf("request: %v", err)
	}
	if result.NAV != "9.1" {
		t.Errorf("nav = %q, want 9.1", result.NAV)
	}
	if got := atomic.LoadInt64(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// POST bodies carry read-only filters and must survive being re-sent.
func TestRequestReplaysFormOnRetry(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server, _ := serve(t, nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(b))
		first := len(bodies) == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		io.WriteString(w, `{}`)
	})
	form := url.Values{"scheme_id": {"3"}, "type": {"weekly"}}
	if err := request(t.Context(), server.Client(), server.URL, form, &struct{}{}); err != nil {
		t.Fatalf("request: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("bodies = %d, want 2", len(bodies))
	}
	if bodies[0] != bodies[1] || bodies[0] != form.Encode() {
		t.Errorf("bodies = %q, want both %q", bodies, form.Encode())
	}
}

func TestTransientStatus(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusAccepted: true, http.StatusRequestTimeout: true, http.StatusTooManyRequests: true,
		http.StatusInternalServerError: true, http.StatusServiceUnavailable: true,
		http.StatusOK: false, http.StatusNotFound: false, http.StatusForbidden: false, http.StatusBadRequest: false,
	} {
		if got := transientStatus(status); got != want {
			t.Errorf("transientStatus(%d) = %t, want %t", status, got, want)
		}
	}
}
