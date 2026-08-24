package safetospend

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// futureEnvelopeStore — конверт, чей период ещё не начался (оператор завёл его
// заранее). Факта за такой конверт быть не может.
//
// SpentByCategoryExcludingRecurring повторяет контракт настоящего стора: при
// to < from он возвращает ОШИБКУ, а не пустой список (internal/budget/
// share_spent.go). Именно на этом ломался статус конвертов.
type futureEnvelopeStore struct {
	fakeStore
	env    *budget.Envelope
	shares []budget.EnvelopeShare
}

func (s futureEnvelopeStore) GetActiveEnvelope(context.Context, int64) (*budget.Envelope, bool, error) {
	return s.env, true, nil
}

func (s futureEnvelopeStore) ListShares(context.Context, int64, uuid.UUID) ([]budget.EnvelopeShare, error) {
	return s.shares, nil
}

func (s futureEnvelopeStore) SpentByCategoryExcludingRecurring(_ context.Context, from, to time.Time) ([]budget.CategorySpentRow, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("SpentByCategoryExcludingRecurring: to (%s) раньше from (%s)", to, from)
	}
	return nil, nil
}

// Конверт с периодом в будущем обязан отдавать статус конвертов, а не
// «временную ошибку»: факта за него нет, но лимиты уже расписаны и оператор
// имеет право их увидеть.
func TestRunShares_FutureEnvelopeReturnsStatus(t *testing.T) {
	start := time.Now().AddDate(0, 0, 7)
	st := futureEnvelopeStore{
		env: &budget.Envelope{
			ID:             uuid.New(),
			IncomeAmount:   100000,
			IncomeCurrency: "RUB",
			PeriodStart:    start,
			PeriodEnd:      start.AddDate(0, 0, 14),
		},
		shares: []budget.EnvelopeShare{
			{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5000, Position: 0,
				Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		},
	}

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
	skill := NewSafeToSpendSkill(st, fakeLLM{}, nil)
	out, err := skill.Run(ctx, `{"question":"сколько осталось в конвертах?"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "Временная ошибка") {
		t.Fatalf("будущий конверт отдал ошибку вместо статуса: %q", out)
	}
	if !strings.Contains(out, "Еда") {
		t.Fatalf("в статусе нет доли «Еда»: %q", out)
	}
}

// Тот же конверт, но через computeShareRemaining: факт нулевой, весь лимит на
// месте — обрезка окна факта не должна ничего «потратить».
func TestRunShares_FutureEnvelopeSpentIsZero(t *testing.T) {
	shares := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
	}
	items := computeShareRemaining(shares, nil, testRates)
	if len(items) != 1 {
		t.Fatalf("ожидали одну долю, got %d", len(items))
	}
	if items[0].SpentTHB != 0 || items[0].Remaining != 5000 {
		t.Fatalf("факт %.2f, остаток %.2f — ожидали 0 и 5000", items[0].SpentTHB, items[0].Remaining)
	}
}
