package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"frame4/internal/metrics"
	"frame4/internal/service"
	"frame4/internal/transport/httpapi"
)

func main() {
	addr := envOrDefault("HTTP_ADDR", ":8080")

	metricsStore := metrics.NewStore()
	engine := service.NewEngine(metricsStore)
	h := httpapi.NewHandler(engine, metricsStore)

	mux := http.NewServeMux()
	h.Register(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("booking service started on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}

func envOrDefault(name string, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("level=INFO event=http_request method=%s path=%s duration_ms=%d", r.Method, r.URL.Path, time.Since(start).Milliseconds())
	})
}
