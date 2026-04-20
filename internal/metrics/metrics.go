package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"frame4/internal/model"
)

// Store потокобезопасное хранилище счётчиков и оценок задержек.
type Store struct {
	mu sync.RWMutex

	successTransitions  int64
	errorTransitions    int64
	duplicateDeliveries int64
	compensations       int64

	stepDurations map[model.Step][]time.Duration
}

func NewStore() *Store {
	return &Store{stepDurations: make(map[model.Step][]time.Duration)}
}

func (s *Store) IncSuccessTransition() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successTransitions++
}

func (s *Store) IncErrorTransition() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorTransitions++
}

func (s *Store) IncDuplicate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duplicateDeliveries++
}

func (s *Store) IncCompensation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compensations++
}

func (s *Store) ObserveStepDuration(step model.Step, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stepDurations[step] = append(s.stepDurations[step], d)
}

func (s *Store) Snapshot() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := map[string]float64{
		"booking_success_transitions_total":  float64(s.successTransitions),
		"booking_error_transitions_total":    float64(s.errorTransitions),
		"booking_duplicate_deliveries_total": float64(s.duplicateDeliveries),
		"booking_compensations_total":        float64(s.compensations),
	}

	for step, values := range s.stepDurations {
		if len(values) == 0 {
			continue
		}
		var total time.Duration
		for _, d := range values {
			total += d
		}
		avgMS := float64(total.Milliseconds()) / float64(len(values))
		key := fmt.Sprintf("booking_step_latency_avg_ms{step=%q}", string(step))
		out[key] = avgMS
	}

	return out
}

// ToPrometheusFormat рендерит метрики в text/plain формате наподобие Prometheus exposition.
func ToPrometheusFormat(metrics map[string]float64) string {
	keys := make([]string, 0, len(metrics))
	for k := range metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("%s %v\n", k, metrics[k]))
	}
	return b.String()
}
