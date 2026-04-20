package model

import "time"

// State описывает состояние процесса бронирования.
type State string

const (
	StateInit             State = "INIT"
	StateRoomHeld         State = "ROOM_HELD"
	StateCalendarBooked   State = "CALENDAR_BOOKED"
	StateNotificationSent State = "NOTIFICATION_SENT"
	StateCompleted        State = "COMPLETED"
)

// Step описывает шаг обработки внутри одного события.
type Step string

const (
	StepHoldRoom         Step = "HOLD_ROOM"
	StepBookCalendar     Step = "BOOK_CALENDAR"
	StepSendNotification Step = "SEND_NOTIFICATION"
	StepFinalize         Step = "FINALIZE"
)

// Event входящее событие для запуска процесса.
type Event struct {
	ProcessKey      string `json:"process_key"`
	IdempotencyKey  string `json:"idempotency_key"`
	CorrelationID   string `json:"correlation_id,omitempty"`
	FailAtStep      Step   `json:"fail_at_step,omitempty"`
	InjectedLatency int    `json:"injected_latency_ms,omitempty"`
}

// StepTiming грубая оценка задержки конкретного шага.
type StepTiming struct {
	Step      Step          `json:"step"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration"`
}

// ProcessSnapshot срез состояния для ответов API.
type ProcessSnapshot struct {
	ProcessKey          string       `json:"process_key"`
	State               State        `json:"state"`
	ProcessedEvents     int          `json:"processed_events"`
	DuplicateDeliveries int          `json:"duplicate_deliveries"`
	LastCorrelationID   string       `json:"last_correlation_id"`
	LastStepTimings     []StepTiming `json:"last_step_timings"`
}
