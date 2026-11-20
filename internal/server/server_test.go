package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

var startedAt = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

var client = &http.Client{Timeout: 2 * time.Second}

func newTestServer() *Server {
	return New(Info{
		Version:   "1.2.3",
		Hostname:  "web-1",
		StartedAt: startedAt,
	}, slog.New(slog.DiscardHandler))
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestIndexReportsInstanceInfo(t *testing.T) {
	rec := serve(newTestServer(), http.MethodGet, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got Info
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Version != "1.2.3" || got.Hostname != "web-1" || !got.StartedAt.Equal(startedAt) {
		t.Errorf("got %+v, want version 1.2.3 on web-1 started at %s", got, startedAt)
	}
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
		{http.MethodOptions, "/healthz", http.StatusMethodNotAllowed},
	}
	s := newTestServer()
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if code := serve(s, tt.method, tt.path).Code; code != tt.want {
				t.Errorf("status = %d, want %d", code, tt.want)
			}
		})
	}
}

func TestMetricsCountRequestsByRouteAndCode(t *testing.T) {
	s := newTestServer()
	ts := httptest.NewServer(s)
	defer ts.Close()

	mustGet(t, ts.URL+"/")
	s.draining.Store(true)
	mustGet(t, ts.URL+"/healthz")

	const want = `
# HELP http_requests_total HTTP requests handled, by route, method and status code.
# TYPE http_requests_total counter
http_requests_total{code="200",method="get",route="GET /{$}"} 1
http_requests_total{code="503",method="get",route="GET /healthz"} 1
`
	if err := testutil.ScrapeAndCompare(ts.URL+"/metrics", strings.NewReader(want), "http_requests_total"); err != nil {
		t.Error(err)
	}
}
