# Практическое занятие №4 — учебная веб-служба на Go

Этот проект реализует **учебную веб-службу бронирования переговорки** с явной машиной состояний, идемпотентной обработкой событий, компенсацией при частичном сбое, health-check эндпоинтами и метриками наблюдаемости.

Главная цель проекта — показать, как строить сервис, который:
- корректно работает при **повторной доставке одного и того же события**;
- сохраняет **предсказуемое состояние** при частичных отказах;
- прозрачен для эксплуатации через **логи, метрики, liveness/readiness**.

---

## 1) Что моделирует сервис

Сервис обрабатывает событие бронирования переговорки в **4 шага**:

1. `HOLD_ROOM` — удерживаем переговорку (`INIT -> ROOM_HELD`)
2. `BOOK_CALENDAR` — создаём запись в календаре (`ROOM_HELD -> CALENDAR_BOOKED`)
3. `SEND_NOTIFICATION` — отправляем уведомление (`CALENDAR_BOOKED -> NOTIFICATION_SENT`)
4. `FINALIZE` — финализируем процесс (`NOTIFICATION_SENT -> COMPLETED`)

Если шаг `SEND_NOTIFICATION` падает, выполняется **компенсация**:
- отменяем результат предыдущего шага `BOOK_CALENDAR`;
- переводим процесс из `CALENDAR_BOOKED` обратно в `ROOM_HELD`.

Так система остаётся в согласованном, предсказуемом состоянии.

---

## 2) Термины и важные ключи

- **Process Key** (`process_key`) — уникальный идентификатор процесса бронирования.
- **Idempotency Key** (`idempotency_key`) — уникальный идентификатор события внутри процесса.
- **Correlation ID** (`correlation_id`) — сквозной идентификатор для трассировки логов и ответа API.

Повторная доставка с тем же `process_key + idempotency_key` не меняет состояние: событие считается дублем.

---

## 3) Архитектура и структура проекта

```text
.
├── cmd/server/main.go                  # Точка входа: запуск HTTP-сервера
├── internal/model/types.go             # Доменные типы: состояния, шаги, событие, снапшот
├── internal/service/machine.go         # Машина состояний, идемпотентность, компенсация, ready-логика
├── internal/service/machine_test.go    # Юнит-тесты ключевой логики
├── internal/metrics/metrics.go         # Счётчики и оценка задержек шагов
├── internal/transport/httpapi/handler.go # HTTP API: events/process/health/metrics
├── go.mod
└── README.md
```

### Ключевые модули

- `internal/service/machine.go`
  - хранит процессы в памяти;
  - проверяет идемпотентность;
  - выполняет переходы машины состояний;
  - запускает компенсацию при нужном сценарии сбоя;
  - переводит readiness в degraded при критической деградации.

- `internal/metrics/metrics.go`
  - ведёт счётчики:
    - успешных переходов;
    - ошибочных переходов;
    - повторных доставок;
    - компенсаций;
  - собирает грубую оценку задержки шагов (среднее значение в ms).

- `internal/transport/httpapi/handler.go`
  - `POST /events` — обработка события;
  - `GET /process/{key}` — текущее состояние процесса;
  - `GET /health/live` — liveness;
  - `GET /health/ready` — readiness;
  - `GET /metrics` — метрики в текстовом формате.

---

## 4) API

## `POST /events`

Обрабатывает событие. Тело запроса:

```json
{
  "process_key": "room-2026-04-20-10-00",
  "idempotency_key": "evt-0001",
  "correlation_id": "corr-abc-123",
  "fail_at_step": "",
  "injected_latency_ms": 0
}
```

Поля:
- `process_key` (обязательно)
- `idempotency_key` (обязательно)
- `correlation_id` (опционально; если не передан — сгенерируется)
- `fail_at_step` (опционально: `HOLD_ROOM`, `BOOK_CALENDAR`, `SEND_NOTIFICATION`, `FINALIZE`)
- `injected_latency_ms` (опционально: искусственная задержка каждого шага)

Варианты ответа:
- `200 processed` — событие обработано.
- `200 duplicate_ignored` — дубль, состояние не изменено.
- `500` — ошибка перехода/шага.

---

## `GET /process/{process_key}`

Возвращает текущий срез процесса:
- состояние;
- число обработанных уникальных событий;
- число дублей;
- последний `correlation_id`;
- тайминги последней успешной обработки по шагам.

---

## `GET /health/live`

Всегда `200` при живом процессе сервера:

```json
{"status":"alive"}
```

## `GET /health/ready`

- `200 {"status":"ready"}` при нормальном состоянии;
- `503 {"status":"degraded"}` при критической деградации.

Критическая деградация наступает после серии ошибок (порог в коде — `5` подряд).

---

## `GET /metrics`

Возвращает текстовые метрики (совместимый стиль с Prometheus exposition):

- `booking_success_transitions_total`
- `booking_error_transitions_total`
- `booking_duplicate_deliveries_total`
- `booking_compensations_total`
- `booking_step_latency_avg_ms{step="..."}`

---

## 5) Логи и наблюдаемость

Логи пишутся через `log.Printf` и содержат:
- `correlation_id`
- `process_key`
- тип события (`transition`, `duplicate_delivery`, `step_failed`, `compensation`)
- шаг, состояние до/после, задержку

Это позволяет восстановить цепочку событий по `correlation_id`.

---

## 6) Быстрый старт

### Требования
- Go 1.22+

### Запуск

```bash
go run ./cmd/server
```

Сервис стартует на `:8080` (или `HTTP_ADDR`, если задано).

### Пример: успешная обработка

```bash
curl -s -X POST http://localhost:8080/events \
  -H 'Content-Type: application/json' \
  -d '{
    "process_key":"booking-1",
    "idempotency_key":"evt-1",
    "correlation_id":"corr-1"
  }' | jq
```

### Пример: повторная доставка (идемпотентность)

Повторите тот же запрос с тем же `idempotency_key`; получите `duplicate_ignored`.

### Пример: частичный сбой + компенсация

```bash
curl -s -X POST http://localhost:8080/events \
  -H 'Content-Type: application/json' \
  -d '{
    "process_key":"booking-2",
    "idempotency_key":"evt-1",
    "correlation_id":"corr-fail-1",
    "fail_at_step":"SEND_NOTIFICATION"
  }' | jq
```

После ошибки проверьте состояние:

```bash
curl -s http://localhost:8080/process/booking-2 | jq
```

Ожидаемо будет `ROOM_HELD` (после компенсации откатили календарь).

---

## 7) Проверки и тестирование

Юнит-тесты:

```bash
go test ./...
```

Покрыты сценарии:
- идемпотентность (повторное событие);
- компенсация при сбое на `SEND_NOTIFICATION`;
- переход readiness в degraded после критической серии ошибок.

---

## 8) Что можно улучшить дальше

Для production-уровня можно добавить:
- персистентное хранилище состояния (PostgreSQL/Redis);
- outbox/inbox-паттерны для надёжной доставки;
- полноценные Prometheus histograms и OpenTelemetry tracing;
- retry-политику и circuit-breaker на внешние интеграции;
- разделение команды/события (CQRS-style) и версионирование контрактов.

---

## 9) Почему этот проект полезен

Проект показывает реальные инженерные практики, востребованные в event-driven системах:
- детерминированная state machine;
- идемпотентная обработка;
- компенсация побочных эффектов;
- операционная наблюдаемость и health probes.

Это базовый, но качественный фундамент для дальнейшего развития в сторону полноценного workflow-сервиса.
