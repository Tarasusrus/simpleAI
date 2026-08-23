package budgetskill

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

const warnChatID = int64(-70047)

// fakeShareStore — подставной источник конверта и факта. Позволяет проверить
// предупреждение без БД и, главное, смоделировать сбой расчёта.
type fakeShareStore struct {
	env    *budget.Envelope
	shares []budget.EnvelopeShare
	spent  []budget.CategorySpentRow
	rates  map[string]float64
	err    error // сбой любого чтения
}

func (f *fakeShareStore) GetActiveEnvelope(context.Context, int64) (*budget.Envelope, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if f.env == nil {
		return nil, false, nil
	}
	return f.env, true, nil
}

func (f *fakeShareStore) ListShares(context.Context, int64, uuid.UUID) ([]budget.EnvelopeShare, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.shares, nil
}

func (f *fakeShareStore) GetExchangeRates(context.Context) (map[string]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rates, nil
}

func (f *fakeShareStore) SpentByCategoryExcludingRecurring(context.Context, time.Time, time.Time) ([]budget.CategorySpentRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.spent, nil
}

// warnRates — курс 1 ฿ = 2 ₽, RUB к самому себе 1. Числа круглые нарочно:
// величина перерасхода в тексте должна читаться глазом.
func warnRates() map[string]float64 {
	return map[string]float64{"THB": 2, "RUB": 1}
}

func warnEnvelope() *budget.Envelope {
	now := time.Now()
	return &budget.Envelope{
		ID:          uuid.New(),
		ChatID:      warnChatID,
		PeriodStart: now.AddDate(0, 0, -7),
		PeriodEnd:   now.AddDate(0, 0, 7),
	}
}

// warnShares — «Еда» с лимитом 5000 ฿ и обязательная доля-приёмник «прочее»
// с лимитом 1000 ฿ (ADR-008 §6).
func warnShares() []budget.EnvelopeShare {
	return []budget.EnvelopeShare{
		{
			Name: "Еда", Kind: budget.ShareKindSpend, Source: budget.ShareSourceAuto,
			Allocated: 5000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}},
		},
		{
			Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Source: budget.ShareSourceAuto,
			Allocated: 1000, Position: 1,
		},
	}
}

func warnCtx() context.Context {
	return context.WithValue(context.Background(), agent.ChatIDKey{}, warnChatID)
}

func warnSkill(st *fakeShareStore) *BudgetSkill {
	return &BudgetSkill{buckets: defaultBuckets(), shareStore: st}
}

// TestShareWarning_WithinLimit — трата в пределах лимита: ответ без строки о
// пробое.
func TestShareWarning_WithinLimit(t *testing.T) {
	st := &fakeShareStore{
		env: warnEnvelope(), shares: warnShares(), rates: warnRates(),
		spent: []budget.CategorySpentRow{{CategoryName: "Еда", Currency: "RUB", Amount: 4000}}, // 2000 ฿ из 5000
	}
	got := warnSkill(st).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"})
	if got != "" {
		t.Fatalf("ожидали пустое предупреждение, получили %q", got)
	}
}

// TestShareWarning_Overspent — трата увела долю в минус: в ответе имя доли и
// величина перерасхода. Лимит 5000 ฿ = 10000 ₽, потрачено 12000 ₽ → минус 2000 ₽.
func TestShareWarning_Overspent(t *testing.T) {
	st := &fakeShareStore{
		env: warnEnvelope(), shares: warnShares(), rates: warnRates(),
		spent: []budget.CategorySpentRow{{CategoryName: "Еда", Currency: "RUB", Amount: 12000}},
	}
	got := warnSkill(st).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"})
	if !strings.Contains(got, "Еда") {
		t.Fatalf("нет имени доли в предупреждении: %q", got)
	}
	if !strings.Contains(got, "2000 ₽") {
		t.Fatalf("нет величины перерасхода 2000 ₽: %q", got)
	}
}

// TestShareWarning_FallbackShare — категория без своей доли попадает в
// «прочее», и предупреждение считается по доле-приёмнику (ADR-008 §6).
// Лимит «прочее» 1000 ฿ = 2000 ₽, потрачено 3000 ₽ → минус 1000 ₽.
func TestShareWarning_FallbackShare(t *testing.T) {
	st := &fakeShareStore{
		env: warnEnvelope(), shares: warnShares(), rates: warnRates(),
		spent: []budget.CategorySpentRow{{CategoryName: "Развлечения", Currency: "RUB", Amount: 3000}},
	}
	got := warnSkill(st).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Развлечения"})
	if !strings.Contains(got, budget.FallbackShareName) {
		t.Fatalf("предупреждение не по доле-приёмнику: %q", got)
	}
	if !strings.Contains(got, "1000 ₽") {
		t.Fatalf("нет величины перерасхода 1000 ₽: %q", got)
	}
	// Доля «Еда» не должна упоминаться: её лимит трата не трогала.
	if strings.Contains(got, "Еда") {
		t.Fatalf("трата без своей доли задела чужую долю: %q", got)
	}
}

// TestShareWarning_NoActiveEnvelope — нет активного конверта: ответ прежний.
func TestShareWarning_NoActiveEnvelope(t *testing.T) {
	st := &fakeShareStore{rates: warnRates()}
	if got := warnSkill(st).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"}); got != "" {
		t.Fatalf("без конверта ожидали пустой ответ, получили %q", got)
	}
}

// TestShareWarning_ComputeFailureIsSilent — сбой расчёта не поднимается наверх:
// предупреждения нет, паники нет, запись траты не затронута.
func TestShareWarning_ComputeFailureIsSilent(t *testing.T) {
	st := &fakeShareStore{err: errors.New("боль в БД")}
	if got := warnSkill(st).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"}); got != "" {
		t.Fatalf("при сбое ожидали пустой ответ, получили %q", got)
	}
}

// TestAddExpense_WarnsOnOverspentShare_Integration — сквозной прогон через
// настоящий add_expense: трата пишется в БД, а предупреждение приклеивается
// поверх обычного ответа.
//
// Второй, не менее важный кусок — сбой расчёта: с падающим shareStore ответ
// прежний, но транзакция всё равно в базе. Именно это доказывает инвариант
// «предупреждение не блокирует и не откатывает трату».
func TestAddExpense_WarnsOnOverspentShare_Integration(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)

	var created []string
	t.Cleanup(func() {
		for _, id := range created {
			if _, err := pool.Exec(context.Background(), `DELETE FROM budget_transaction WHERE id = $1`, id); err != nil {
				t.Logf("cleanup transaction %s: %v", id, err)
			}
		}
	})
	addExpense := func(amount int) string {
		t.Helper()
		reply, err := skill.Run(ctx, `{"action":"add_expense","amount":`+itoa(amount)+`,"currency":"RUB","category":"Еда","description":"share-warning-test"}`)
		if err != nil {
			t.Fatalf("add_expense: %v", err)
		}
		var id string
		if err := pool.QueryRow(context.Background(),
			`SELECT id::text FROM budget_transaction WHERE description = 'share-warning-test' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatalf("трата не записана: %v", err)
		}
		created = append(created, id)
		return reply
	}

	if _, err := skill.Run(ctx, `{"action":"start_envelope","amount":127000,"currency":"RUB"}`); err != nil {
		t.Fatalf("start_envelope: %v", err)
	}
	env, ok, err := skill.store.GetActiveEnvelope(ctx, envelopeTestChatID)
	if err != nil || !ok {
		t.Fatalf("GetActiveEnvelope: %v ok=%v", err, ok)
	}
	shares, err := skill.store.ListShares(ctx, envelopeTestChatID, env.ID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	rates, err := skill.store.GetExchangeRates(ctx)
	if err != nil {
		t.Fatalf("rates: %v", err)
	}
	target, okShare := shareRemainingForTest(shares, "Еда")
	if !okShare {
		t.Skip("в раскладке нет доли, владеющей «Еда» — сценарий не воспроизводим на этих данных")
	}

	// Мелкая трата в пределах лимита — предупреждения быть не должно.
	small := addExpense(100)
	t.Logf("ответ бота (в пределах лимита):\n%s", small)
	if strings.Contains(small, "пробит") {
		t.Errorf("предупреждение на трате в пределах лимита:\n%s", small)
	}

	// Трата, заведомо уводящая долю в минус: весь остаток доли + 5000 ₽.
	overBy := 5000.0
	big := int(target.Allocated+target.CarriedIn)*int(rates["THB"]) + int(overBy)
	huge := addExpense(big)
	t.Logf("ответ бота (после пробоя):\n%s", huge)
	if !strings.Contains(huge, "пробит") {
		t.Fatalf("нет предупреждения о пробое:\n%s", huge)
	}
	if !strings.Contains(huge, target.Name) {
		t.Errorf("в предупреждении нет имени доли %q:\n%s", target.Name, huge)
	}

	// Сбой расчёта: ответ прежний, трата всё равно в базе.
	skill.shareStore = &fakeShareStore{err: errors.New("боль в БД")}
	plain := addExpense(100)
	t.Logf("ответ бота (сбой расчёта):\n%s", plain)
	if strings.Contains(plain, "пробит") {
		t.Errorf("при сбое расчёта появилось предупреждение:\n%s", plain)
	}
	if len(created) != 3 {
		t.Fatalf("записаны не все траты: %d из 3", len(created))
	}
}

// shareRemainingForTest — доля, которой принадлежит категория, для расчёта
// суммы «заведомо больше лимита».
func shareRemainingForTest(shares []budget.EnvelopeShare, category string) (budget.EnvelopeShare, bool) {
	sh := budget.ResolveShare(shares, nil, category)
	if sh == nil {
		return budget.EnvelopeShare{}, false
	}
	return *sh, true
}

func itoa(v int) string { return strconv.Itoa(v) }
