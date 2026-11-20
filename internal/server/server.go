package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Info struct {
	Version   string    `json:"version"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

type Server struct {
	info            Info
	logger          *slog.Logger
	mux             *http.ServeMux
	draining        atomic.Bool
	shutdownTimeout time.Duration
	requests        *prometheus.CounterVec
	duration        *prometheus.HistogramVec
}

func New(info Info, logger *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	factory := promauto.With(reg)

	s := &Server{
		info:            info,
		logger:          logger,
		mux:             http.NewServeMux(),
		shutdownTimeout: 10 * time.Second,
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests handled, by route, method and status code.",
		}, []string{"route", "method", "code"}),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency, by route, method and status code.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "code"}),
	}

	s.handle("GET /{$}", s.handleIndex)
	s.handle("GET /healthz", s.handleHealthz)
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) Run(ctx context.Context, ln net.Listener, drainDelay time.Duration) error {
	hs := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- hs.Serve(ln) }()
	s.logger.Info("listening", "addr", ln.Addr().String(), "version", s.info.Version)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.logger.Info("draining", "delay", drainDelay.String())
	hs.SetKeepAlivesEnabled(false)
	s.draining.Store(true)
	time.Sleep(drainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		closeErr := hs.Close()
		<-serveErr
		return errors.Join(fmt.Errorf("shutdown: %w", err), closeErr)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	s.logger.Info("stopped")
	return nil
}

func (s *Server) handle(pattern string, h http.HandlerFunc) {
	route := prometheus.Labels{"route": pattern}
	s.mux.Handle(pattern, promhttp.InstrumentHandlerDuration(
		s.duration.MustCurryWith(route),
		promhttp.InstrumentHandlerCounter(s.requests.MustCurryWith(route), h),
	))
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.info)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if s.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
