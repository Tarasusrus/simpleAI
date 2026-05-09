// Package rates обновляет курсы валют из открытого API и хранит их в БД.
package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// RateStore — интерфейс для сохранения курсов.
type RateStore interface {
	SaveExchangeRate(ctx context.Context, currency string, rateToRUB float64) error
}

// Worker раз в сутки обновляет курсы THB, USD, EUR из open.er-api.com.
type Worker struct {
	store      RateStore
	currencies []string
	interval   time.Duration
}

// New создаёт Worker с дефолтным интервалом 24 часа.
func New(store RateStore) *Worker {
	return &Worker{
		store:      store,
		currencies: []string{"THB", "USD", "EUR"},
		interval:   24 * time.Hour,
	}
}

// Run запускает фоновый цикл обновления курсов. Блокирующий, запускать в горутине.
func (w *Worker) Run(ctx context.Context) {
	// Обновляем сразу при старте.
	w.fetchAndSave(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.fetchAndSave(ctx)
		}
	}
}

type erAPIResponse struct {
	Result string             `json:"result"`
	Rates  map[string]float64 `json:"rates"`
}

func (w *Worker) fetchAndSave(ctx context.Context) {
	rates, err := fetchRates(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "rates fetch failed, using existing DB rates", "err", err)
		return
	}
	for _, cur := range w.currencies {
		invRate, ok := rates[cur]
		if !ok || invRate == 0 {
			continue
		}
		// open.er-api.com возвращает "1 RUB = X currency", нам нужно "1 currency = Y RUB"
		rateToRUB := 1.0 / invRate
		if err := w.store.SaveExchangeRate(ctx, cur, rateToRUB); err != nil {
			slog.Default().WarnContext(ctx, "save exchange rate failed", "currency", cur, "err", err)
		}
	}
	slog.Default().InfoContext(ctx, "exchange rates updated", "currencies", w.currencies)
}

func fetchRates(ctx context.Context) (map[string]float64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		"https://open.er-api.com/v6/latest/RUB", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rates: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Default().Warn("close response body", "err", err)
		}
	}()

	var data erAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode rates: %w", err)
	}
	if data.Result != "success" {
		return nil, fmt.Errorf("rates API error: result=%s", data.Result)
	}
	return data.Rates, nil
}
