package safetospend

import (
	"context"
	"regexp"
	"testing"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

type fakeStore struct{}

func (fakeStore) GetExchangeRates(context.Context) (map[string]float64, error) {
	return map[string]float64{"RUB": 1.0, "THB": 2.6}, nil
}
func (fakeStore) GetPeriodSnapshot(context.Context, int64, time.Time, time.Time, map[string]float64) (*budget.AdvisorSnapshot, error) {
	// recurring 20000 RUB → /2.6 в THB (snapshot хранит THB). Здесь сразу THB.
	return &budget.AdvisorSnapshot{
		UpcomingRecurring: 20000 / 2.6,
		ActiveDebtDue:     0,
		SpentByCategory:   map[string]float64{"Еда": 100},
	}, nil
}
func (fakeStore) GetForecastData(context.Context, int, map[string]float64) ([]budget.CategoryForecast, error) {
	return []budget.CategoryForecast{{CategoryName: "Еда", Currency: "RUB", ForecastAmount: 30000}}, nil
}
func (fakeStore) PlannedExpensesTHB(context.Context, int64, map[string]float64) (float64, int, error) {
	return 0, 0, nil
}
func (fakeStore) ListPlannedExpenses(context.Context, int64) ([]budget.PlannedExpense, error) {
	return nil, nil
}
func (fakeStore) GetActiveEnvelope(context.Context, int64) (*budget.Envelope, bool, error) {
	return nil, false, nil
}

// fakeLLM возвращает заданную строку (для проверки, что числа от неё не зависят).
type fakeLLM struct{ resp string }

func (f fakeLLM) Ask(context.Context, string) (string, error)                   { return f.resp, nil }
func (f fakeLLM) AskWithSystem(context.Context, string, string) (string, error) { return f.resp, nil }

// numericLines извлекает строки с числовыми суммами (₽) — детерминированный вывод.
func numericLines(s string) []string {
	re := regexp.MustCompile(`(?m)^.*[-\d]+ ₽.*$`)
	return re.FindAllString(s, -1)
}

// TestNumbersIndependentOfLLM (ADR-007 §3): мутация ответа LLM НЕ меняет числа.
func TestNumbersIndependentOfLLM(t *testing.T) {
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
	payload := `{"amount":127000,"currency":"RUB","question":"сколько свободно?"}`

	s1 := NewSafeToSpendSkill(fakeStore{}, fakeLLM{resp: "Совет A"}, nil)
	out1, err := s1.Run(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	// Другой ответ LLM с фейковыми числами.
	s2 := NewSafeToSpendSkill(fakeStore{}, fakeLLM{resp: "Потратьте 999999 на ерунду"}, nil)
	out2, err := s2.Run(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}

	n1, n2 := numericLines(out1), numericLines(out2)
	if len(n1) == 0 {
		t.Fatal("нет числовых строк в ответе")
	}
	if len(n1) != len(n2) {
		t.Fatalf("число числовых строк изменилось от LLM: %d vs %d", len(n1), len(n2))
	}
	for i := range n1 {
		if n1[i] != n2[i] {
			t.Errorf("числовая строка зависит от LLM:\n  A: %q\n  B: %q", n1[i], n2[i])
		}
	}

	// И проверим само число: приход 127000 − recurring 20000 = 107000.
	if !regexpContains(out1, `Остаётся до повседневных трат: 107000 ₽`) {
		t.Errorf("ожидалось 'Остаётся до повседневных трат: 107000 ₽', got:\n%s", out1)
	}
}

// Без суммы и без активного конверта — приглашение назвать приход/завести конверт.
func TestNoAmountNoEnvelope(t *testing.T) {
	s := NewSafeToSpendSkill(fakeStore{}, fakeLLM{}, nil)
	out, err := s.Run(context.Background(), `{"amount":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if !regexpContains(out, `Активного конверта нет`) {
		t.Errorf("ожидалось сообщение об отсутствии конверта, got: %q", out)
	}
}

func regexpContains(s, pat string) bool {
	return regexp.MustCompile(pat).MatchString(s)
}
