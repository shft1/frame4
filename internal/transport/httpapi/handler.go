package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"frame4/internal/metrics"
	"frame4/internal/model"
	"frame4/internal/service"
)

type Handler struct {
	engine  *service.Engine
	metrics *metrics.Store
}

func NewHandler(engine *service.Engine, metricsStore *metrics.Store) *Handler {
	return &Handler{engine: engine, metrics: metricsStore}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/events", h.handleEvent)
	mux.HandleFunc("/process/", h.handleGetProcess)
	mux.HandleFunc("/health/live", h.handleLive)
	mux.HandleFunc("/health/ready", h.handleReady)
	mux.HandleFunc("/metrics", h.handleMetrics)
}

func (h *Handler) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var event model.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	snapshot, err := h.engine.HandleEvent(event)
	if err != nil {
		if errors.Is(err, service.ErrDuplicateDelivery) {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "duplicate_ignored",
				"state":  snapshot,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "processed",
		"state":  snapshot,
	})
}

func (h *Handler) handleGetProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	processKey := strings.TrimPrefix(r.URL.Path, "/process/")
	if processKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "process key is required"})
		return
	}

	snapshot, ok := h.engine.GetProcess(processKey)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "process not found"})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !h.engine.IsReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	m := h.metrics.Snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(metrics.ToPrometheusFormat(m)))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
