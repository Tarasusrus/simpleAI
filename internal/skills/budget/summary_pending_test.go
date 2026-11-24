package budgetskill

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

func summaryFixture() *budget.Summary {
	return &budget.Summary{
		Period: budget.Period{
			From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
		},
		Currencies: []budget.CurrencyGroup{{Currency: "RUB", TotalIncome: 262900, TotalExpense: 215563, Balance: 47337}},
	}
}

// Прогноз посчитан — «недоступен» не печатается, строки «Средний расход» нет.
func TestFormatSummaryL1_ForecastAvailable_NoAvgLine(t *testing.T) {
	rates := map[string]float64{"RUB": 1}
	out, _ := formatSummaryL1(summaryFixture(), pendingForecast{Available: true}, rates, "2026-11")
	for _, bad := range []string{"Прогноз временно недоступен", "Средний расход"} {
		if strings.Contains(out, bad) {
			t.Errorf("лишнее %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "Фонд Да: +47337") {
		t.Errorf("нет фонда:\n%s", out)
	}
}

// Ошибка расчёта — честное «недоступен», итоги всё равно отвечены.
func TestFormatSummaryL1_ForecastUnavailable(t *testing.T) {
	out, _ := formatSummaryL1(summaryFixture(), pendingForecast{}, map[string]float64{"RUB": 1}, "2026-11")
	if !strings.Contains(out, "Прогноз временно недоступен") {
		t.Errorf("нет пометки:\n%s", out)
	}
}

// Прогноз берётся по chatID чата и без ошибки остаётся доступным; сбой — не паника, а Available=false.
func TestPendingOrUnavailable(t *testing.T) {
	var gotChat int64
	s := &BudgetSkill{pendingFn: func(_ context.Context, chatID int64, _ budget.Period, _ map[string]float64) (pendingForecast, error) {
		gotChat = chatID
		return pendingForecast{Available: true, ExpenseRUB: 18000}, nil
	}}
	got := s.pendingOrUnavailable(context.Background(), 42, budget.Period{}, nil)
	if gotChat != 42 || !got.Available || got.ExpenseRUB != 18000 {
		t.Fatalf("chat=%d got=%+v", gotChat, got)
	}
	s.pendingFn = func(context.Context, int64, budget.Period, map[string]float64) (pendingForecast, error) {
		return pendingForecast{Available: true}, errors.New("db down")
	}
	if got := s.pendingOrUnavailable(context.Background(), 42, budget.Period{}, nil); got.Available {
		t.Fatalf("ошибка должна давать Available=false: %+v", got)
	}
}

// Первый ответ «Итоги» берёт прогноз по chatID из контекста и не пишет «недоступен» (баг: раньше всегда писал).
// Мутация: вернуть в summaryL1 pendingForecast{} — тест краснеет.
func TestSummaryL1_UsesChatForecast(t *testing.T) {
	var gotChat int64
	s := &BudgetSkill{pendingFn: func(_ context.Context, chatID int64, _ budget.Period, _ map[string]float64) (pendingForecast, error) {
		gotChat = chatID
		return pendingForecast{Available: true, ExpenseRUB: 18000}, nil
	}}
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(42))
	out, _ := s.summaryL1(ctx, summaryFixture(), summaryFixture().Period, map[string]float64{"RUB": 1}, "2026-11")
	if gotChat != 42 {
		t.Fatalf("прогноз не по чату: %d", gotChat)
	}
	if strings.Contains(out, "временно недоступен") || !strings.Contains(out, "до конца месяца") {
		t.Fatalf("итоги без прогноза:\n%s", out)
	}
}

// Без chat_id в контексте прогноз честно «недоступен», а не «ничего не ждём».
func TestSummaryL1_NoChatIsUnavailable(t *testing.T) {
	s := &BudgetSkill{pendingFn: func(context.Context, int64, budget.Period, map[string]float64) (pendingForecast, error) {
		return pendingForecast{Available: true}, nil
	}}
	out, _ := s.summaryL1(context.Background(), summaryFixture(), summaryFixture().Period, map[string]float64{"RUB": 1}, "2026-11")
	if !strings.Contains(out, "Прогноз временно недоступен") {
		t.Fatalf("нет пометки:\n%s", out)
	}
}
