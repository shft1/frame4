package service

import (
	"fmt"
	"testing"

	"frame4/internal/metrics"
	"frame4/internal/model"
)

func TestEngine_Idempotency(t *testing.T) {
	e := NewEngine(metrics.NewStore())
	event := model.Event{ProcessKey: "p-1", IdempotencyKey: "id-1", CorrelationID: "c-1"}

	if _, err := e.HandleEvent(event); err != nil {
		t.Fatalf("unexpected error on first event: %v", err)
	}
	if _, err := e.HandleEvent(event); err == nil {
		t.Fatal("expected duplicate error")
	}

	s, ok := e.GetProcess("p-1")
	if !ok {
		t.Fatal("process not found")
	}
	if s.DuplicateDeliveries != 1 {
		t.Fatalf("expected 1 duplicate, got %d", s.DuplicateDeliveries)
	}
	if s.ProcessedEvents != 1 {
		t.Fatalf("expected 1 processed event, got %d", s.ProcessedEvents)
	}
}

func TestEngine_CompensationOnFailure(t *testing.T) {
	e := NewEngine(metrics.NewStore())
	event := model.Event{
		ProcessKey:     "p-2",
		IdempotencyKey: "id-1",
		CorrelationID:  "c-2",
		FailAtStep:     model.StepSendNotification,
	}

	if _, err := e.HandleEvent(event); err == nil {
		t.Fatal("expected error")
	}

	s, ok := e.GetProcess("p-2")
	if !ok {
		t.Fatal("process not found")
	}
	if s.State != model.StateRoomHeld {
		t.Fatalf("expected state %s after compensation, got %s", model.StateRoomHeld, s.State)
	}
}

func TestEngine_ReadinessAfterCriticalFailures(t *testing.T) {
	e := NewEngine(metrics.NewStore())

	for i := 0; i < criticalFailureThreshold; i++ {
		_, _ = e.HandleEvent(model.Event{
			ProcessKey:     "p-3",
			IdempotencyKey: fmt.Sprintf("id-fail-%d", i),
			CorrelationID:  "c",
			FailAtStep:     model.StepHoldRoom,
		})
	}

	if e.IsReady() {
		t.Fatal("engine should be degraded after critical failures")
	}
}
