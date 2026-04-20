package service

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"frame4/internal/metrics"
	"frame4/internal/model"
)

var ErrDuplicateDelivery = errors.New("duplicate delivery")

const criticalFailureThreshold = 5

// Process хранит состояние одного процесса.
type Process struct {
	State               model.State
	ProcessedIDKeys     map[string]struct{}
	ProcessedEvents     int
	DuplicateDeliveries int
	LastCorrelationID   string
	LastStepTimings     []model.StepTiming
}

// Engine управляет машиной состояний.
type Engine struct {
	mu sync.RWMutex

	processes map[string]*Process
	metrics   *metrics.Store

	consecutiveFailures int
	degraded            bool
}

func NewEngine(metricsStore *metrics.Store) *Engine {
	return &Engine{
		processes: make(map[string]*Process),
		metrics:   metricsStore,
	}
}

func (e *Engine) HandleEvent(event model.Event) (*model.ProcessSnapshot, error) {
	if event.ProcessKey == "" || event.IdempotencyKey == "" {
		return nil, fmt.Errorf("process_key and idempotency_key are required")
	}
	if event.CorrelationID == "" {
		event.CorrelationID = fmt.Sprintf("corr-%d", time.Now().UnixNano())
	}

	e.mu.Lock()
	process := e.getOrCreateProcess(event.ProcessKey)
	if _, exists := process.ProcessedIDKeys[event.IdempotencyKey]; exists {
		process.DuplicateDeliveries++
		e.metrics.IncDuplicate()
		log.Printf("level=INFO correlation_id=%s process_key=%s event=duplicate_delivery idempotency_key=%s state=%s", event.CorrelationID, event.ProcessKey, event.IdempotencyKey, process.State)
		s := e.snapshotLocked(event.ProcessKey, process)
		e.mu.Unlock()
		return &s, ErrDuplicateDelivery
	}
	e.mu.Unlock()

	stepTimings := make([]model.StepTiming, 0, 4)

	if err := e.transition(event, model.StepHoldRoom, model.StateInit, model.StateRoomHeld, &stepTimings); err != nil {
		return nil, err
	}
	if err := e.transition(event, model.StepBookCalendar, model.StateRoomHeld, model.StateCalendarBooked, &stepTimings); err != nil {
		return nil, err
	}
	if err := e.transition(event, model.StepSendNotification, model.StateCalendarBooked, model.StateNotificationSent, &stepTimings); err != nil {
		if cErr := e.compensateCalendar(event); cErr != nil {
			return nil, cErr
		}
		return nil, err
	}
	if err := e.transition(event, model.StepFinalize, model.StateNotificationSent, model.StateCompleted, &stepTimings); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	process = e.getOrCreateProcess(event.ProcessKey)
	process.ProcessedIDKeys[event.IdempotencyKey] = struct{}{}
	process.ProcessedEvents++
	process.LastCorrelationID = event.CorrelationID
	process.LastStepTimings = stepTimings
	e.consecutiveFailures = 0
	e.degraded = false

	snapshot := e.snapshotLocked(event.ProcessKey, process)
	return &snapshot, nil
}

func (e *Engine) transition(event model.Event, step model.Step, expectedState model.State, nextState model.State, timings *[]model.StepTiming) error {
	start := time.Now()

	e.mu.Lock()
	process := e.getOrCreateProcess(event.ProcessKey)
	if process.State != expectedState {
		current := process.State
		e.mu.Unlock()
		e.metrics.IncErrorTransition()
		e.markFailure()
		return fmt.Errorf("invalid state for step %s: expected %s got %s", step, expectedState, current)
	}
	e.mu.Unlock()

	if event.InjectedLatency > 0 {
		time.Sleep(time.Duration(event.InjectedLatency) * time.Millisecond)
	}
	if event.FailAtStep == step {
		e.metrics.IncErrorTransition()
		e.markFailure()
		log.Printf("level=ERROR correlation_id=%s process_key=%s event=step_failed step=%s", event.CorrelationID, event.ProcessKey, step)
		return fmt.Errorf("step %s failed intentionally", step)
	}

	duration := time.Since(start)
	e.metrics.ObserveStepDuration(step, duration)
	e.metrics.IncSuccessTransition()

	e.mu.Lock()
	defer e.mu.Unlock()
	process = e.getOrCreateProcess(event.ProcessKey)
	process.State = nextState
	log.Printf("level=INFO correlation_id=%s process_key=%s event=transition step=%s from=%s to=%s latency_ms=%d", event.CorrelationID, event.ProcessKey, step, expectedState, nextState, duration.Milliseconds())
	*timings = append(*timings, model.StepTiming{Step: step, StartedAt: start, Duration: duration})
	return nil
}

func (e *Engine) compensateCalendar(event model.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	process := e.getOrCreateProcess(event.ProcessKey)
	if process.State != model.StateCalendarBooked {
		return fmt.Errorf("cannot compensate from state %s", process.State)
	}
	process.State = model.StateRoomHeld
	e.metrics.IncCompensation()
	log.Printf("level=WARN correlation_id=%s process_key=%s event=compensation action=rollback_calendar_booking from=%s to=%s", event.CorrelationID, event.ProcessKey, model.StateCalendarBooked, model.StateRoomHeld)
	return nil
}

func (e *Engine) GetProcess(processKey string) (model.ProcessSnapshot, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.processes[processKey]
	if !ok {
		return model.ProcessSnapshot{}, false
	}
	s := e.snapshotLocked(processKey, p)
	return s, true
}

func (e *Engine) IsReady() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return !e.degraded
}

func (e *Engine) getOrCreateProcess(key string) *Process {
	if p, ok := e.processes[key]; ok {
		return p
	}
	p := &Process{State: model.StateInit, ProcessedIDKeys: make(map[string]struct{})}
	e.processes[key] = p
	return p
}

func (e *Engine) snapshotLocked(processKey string, p *Process) model.ProcessSnapshot {
	stepTimings := make([]model.StepTiming, len(p.LastStepTimings))
	copy(stepTimings, p.LastStepTimings)
	return model.ProcessSnapshot{
		ProcessKey:          processKey,
		State:               p.State,
		ProcessedEvents:     p.ProcessedEvents,
		DuplicateDeliveries: p.DuplicateDeliveries,
		LastCorrelationID:   p.LastCorrelationID,
		LastStepTimings:     stepTimings,
	}
}

func (e *Engine) markFailure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutiveFailures++
	if e.consecutiveFailures >= criticalFailureThreshold {
		e.degraded = true
	}
}
