package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type running struct {
	url    string
	cancel context.CancelFunc
	wait   func() error
}

func startRun(t *testing.T, s *Server, drainDelay time.Duration) running {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, ln, drainDelay) }()

	wait := sync.OnceValue(func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			return errors.New("Run did not return within 5s")
		}
	})
	t.Cleanup(func() {
		cancel()
		wait()
	})
	return running{url: "http://" + ln.Addr().String(), cancel: cancel, wait: wait}
}

func waitForStatus(t *testing.T, url string, want int) *http.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := mustGet(t, url)
		if resp.StatusCode == want {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: status = %d, want %d", url, resp.StatusCode, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func blockingRoute(s *Server) (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	s.mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	return started, release
}

func TestRunDrainsBeforeShutdown(t *testing.T) {
	r := startRun(t, newTestServer(), time.Second)

	if code := mustGet(t, r.url+"/healthz").StatusCode; code != http.StatusOK {
		t.Fatalf("before shutdown: /healthz = %d, want %d", code, http.StatusOK)
	}

	r.cancel()
	resp := waitForStatus(t, r.url+"/healthz", http.StatusServiceUnavailable)
	if !resp.Close {
		t.Error("draining response did not ask the client to close the connection")
	}
	if code := mustGet(t, r.url+"/").StatusCode; code != http.StatusOK {
		t.Errorf("while draining: / = %d, want %d", code, http.StatusOK)
	}

	if err := r.wait(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp, err := client.Get(r.url + "/healthz"); err == nil {
		resp.Body.Close()
		t.Error("server still accepts connections after Run returned")
	}
}

func TestRunWaitsForInFlightRequests(t *testing.T) {
	s := newTestServer()
	started, release := blockingRoute(s)
	r := startRun(t, s, 0)

	status := make(chan int, 1)
	go func() {
		resp, err := client.Get(r.url + "/slow")
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()

	<-started
	r.cancel()
	time.Sleep(100 * time.Millisecond)
	close(release)

	if code := <-status; code != http.StatusOK {
		t.Errorf("in-flight request: status = %d, want %d", code, http.StatusOK)
	}
	if err := r.wait(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestRunForceClosesAfterShutdownTimeout(t *testing.T) {
	s := newTestServer()
	s.shutdownTimeout = 100 * time.Millisecond
	started, release := blockingRoute(s)
	defer close(release)
	r := startRun(t, s, 0)

	go func() {
		if resp, err := client.Get(r.url + "/slow"); err == nil {
			resp.Body.Close()
		}
	}()

	<-started
	r.cancel()
	if err := r.wait(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v, want a shutdown deadline error", err)
	}
}

func TestRunWithCancelledContextStopsCleanly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := newTestServer().Run(ctx, ln, 0); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if conn, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		conn.Close()
		t.Error("listener still open after Run returned")
	}
}

func TestRunReturnsServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	if err := newTestServer().Run(t.Context(), ln, 0); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Run = %v, want an error wrapping net.ErrClosed", err)
	}
}
