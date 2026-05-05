// Package observability отправляет события агента в Langfuse через ingestion API.
//
// Минимальный async-клиент: события буферизуются в канал, фоновая горутина
// шлёт батчи раз в 2 сек или по достижении 100 событий. Все методы nil-safe —
// если NewTracer вернул nil (не настроен), вызовы превращаются в no-op.
//
// Endpoint: POST {host}/api/public/ingestion
// Auth: Basic <publicKey>:<secretKey>
// Формат батча: {"batch":[{"id":<uuid>,"type":<event>,"timestamp":<RFC3339>,"body":{...}}]}
package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	flushInterval = 2 * time.Second
	batchSize     = 100
	bufferSize    = 1024
	httpTimeout   = 10 * time.Second
)

// Tracer — async-клиент Langfuse. nil-приёмник безопасен (no-op).
type Tracer struct {
	host       string
	publicKey  string
	secretKey  string
	httpClient *http.Client
	logger     *slog.Logger

	eventsCh chan event
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

type event struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Body      any    `json:"body"`
}

// NewTracer создаёт async-tracer. Если host/keys пустые — возвращает nil
// (вызовы методов на nil-Tracer становятся no-op). Запускает фоновый sender.
func NewTracer(host, publicKey, secretKey string, logger *slog.Logger) *Tracer {
	if host == "" || publicKey == "" || secretKey == "" {
		if logger != nil {
			logger.Info("langfuse tracer disabled (host or keys missing)")
		}
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tracer{
		host:       host,
		publicKey:  publicKey,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: httpTimeout},
		logger:     logger,
		eventsCh:   make(chan event, bufferSize),
		cancel:     cancel,
	}
	t.wg.Add(1)
	go t.run(ctx)
	logger.Info("langfuse tracer started", "host", host)
	return t
}

// Close останавливает sender и дожидается слива буфера.
func (t *Tracer) Close() {
	if t == nil {
		return
	}
	t.cancel()
	t.wg.Wait()
}

func (t *Tracer) emit(typ string, body any) {
	if t == nil {
		return
	}
	e := event{
		ID:        uuid.NewString(),
		Type:      typ,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Body:      body,
	}
	select {
	case t.eventsCh <- e:
	default:
		t.logger.Warn("langfuse buffer full, dropping event", "type", typ)
	}
}

func (t *Tracer) run(ctx context.Context) {
	defer t.wg.Done()
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var batch []event
	flush := func() {
		if len(batch) == 0 {
			return
		}
		t.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case e := <-t.eventsCh:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		case e := <-t.eventsCh:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (t *Tracer) send(batch []event) {
	body, err := json.Marshal(map[string]any{"batch": batch})
	if err != nil {
		t.logger.Warn("langfuse marshal failed", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.host+"/api/public/ingestion", bytes.NewReader(body))
	if err != nil {
		t.logger.Warn("langfuse req build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(t.publicKey, t.secretKey)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		t.logger.Warn("langfuse send failed", "err", err, "events", len(batch))
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.logger.Debug("langfuse body close", "err", err)
		}
	}()
	// 207 — Multi-Status (часть событий могла не пройти, но это нормально).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMultiStatus {
		t.logger.Warn("langfuse non-2xx", "status", resp.StatusCode, "events", len(batch))
	}
}

// --- Trace / Span / Generation handles ---

// Trace — корневой контейнер событий (один user request).
type Trace struct {
	tracer *Tracer
	ID     string
}

// StartTrace создаёт новый trace в Langfuse.
func (t *Tracer) StartTrace(name string, input any, sessionID, userID string) *Trace {
	if t == nil {
		return nil
	}
	id := uuid.NewString()
	body := map[string]any{
		"id":        id,
		"name":      name,
		"input":     input,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if sessionID != "" {
		body["sessionId"] = sessionID
	}
	if userID != "" {
		body["userId"] = userID
	}
	t.emit("trace-create", body)
	return &Trace{tracer: t, ID: id}
}

// End фиксирует output трейса. Идёт через trace-create — Langfuse upserts по id.
func (tr *Trace) End(output any) {
	if tr == nil {
		return
	}
	tr.tracer.emit("trace-create", map[string]any{
		"id":        tr.ID,
		"output":    output,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// Span — обычный шаг (tool call, retrieval).
type Span struct {
	tracer  *Tracer
	traceID string
	id      string
}

// StartSpan создаёт span внутри трейса.
func (tr *Trace) StartSpan(name string, input any) *Span {
	if tr == nil {
		return nil
	}
	id := uuid.NewString()
	tr.tracer.emit("span-create", map[string]any{
		"id":        id,
		"traceId":   tr.ID,
		"name":      name,
		"input":     input,
		"startTime": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return &Span{tracer: tr.tracer, traceID: tr.ID, id: id}
}

// End завершает span с output. err != nil → level=ERROR.
func (sp *Span) End(output any, err error) {
	if sp == nil {
		return
	}
	body := map[string]any{
		"id":      sp.id,
		"traceId": sp.traceID,
		"endTime": time.Now().UTC().Format(time.RFC3339Nano),
		"output":  output,
	}
	if err != nil {
		body["level"] = "ERROR"
		body["statusMessage"] = err.Error()
	}
	sp.tracer.emit("span-update", body)
}

// Generation — LLM-вызов. Langfuse показывает токены/стоимость только для generation.
type Generation struct {
	tracer  *Tracer
	traceID string
	id      string
}

// StartGeneration создаёт LLM-вызов внутри трейса.
func (tr *Trace) StartGeneration(name, model string, input any) *Generation {
	if tr == nil {
		return nil
	}
	id := uuid.NewString()
	tr.tracer.emit("generation-create", map[string]any{
		"id":        id,
		"traceId":   tr.ID,
		"name":      name,
		"model":     model,
		"input":     input,
		"startTime": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return &Generation{tracer: tr.tracer, traceID: tr.ID, id: id}
}

// End завершает generation с output. err != nil → level=ERROR.
func (g *Generation) End(output any, err error) {
	if g == nil {
		return
	}
	body := map[string]any{
		"id":      g.id,
		"traceId": g.traceID,
		"endTime": time.Now().UTC().Format(time.RFC3339Nano),
		"output":  output,
	}
	if err != nil {
		body["level"] = "ERROR"
		body["statusMessage"] = err.Error()
	}
	g.tracer.emit("generation-update", body)
}
