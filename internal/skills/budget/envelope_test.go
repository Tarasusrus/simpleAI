package budgetskill

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// envelopeTestChatID — отдельный chat для тестов конверта, чтобы не задеть
// реальные конверты на реплике.
const envelopeTestChatID = int64(-70041)

// newEnvelopeTestSkill поднимает скилл на write-реплике. Без
// BOTCLIENT_DATABASE_URL_RW тест пропускается: проверять запись без записи
// нечем, а «зелёный» пропуск — пустой тест.
func newEnvelopeTestSkill(t *testing.T) (*BudgetSkill, *pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE chat_id = $1`, envelopeTestChatID); err != nil {
			t.Logf("cleanup budget_envelope: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope_limit_override WHERE chat_id = $1`, envelopeTestChatID); err != nil {
			t.Logf("cleanup override: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	skill := NewBudgetSkill(budget.NewStore(pool))
	return skill, pool, context.WithValue(ctx, agent.ChatIDKey{}, envelopeTestChatID)
}

// TestStartEnvelope_WritesSharesAndReply — гейт задачи 4/8.
//
// Главное утверждение — ПО ФАКТУ В БАЗЕ: после start_envelope у конверта есть
// доли и их сумма равна FreeAfterObligations. Оно и есть мутационная проверка:
// вырезав раскладку из startEnvelope, получим конверт без строк в
// budget_envelope_share, и тест покраснеет на запросе к БД, а не на тексте
// ответа (текст можно оставить прежним и мутацию не заметить).
func TestStartEnvelope_WritesSharesAndReply(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)

	reply, err := skill.Run(ctx, `{"action":"start_envelope","amount":127000,"currency":"RUB"}`)
	if err != nil {
		t.Fatalf("start_envelope: %v", err)
	}
	t.Logf("ответ бота:\n%s", reply)

	env, ok, err := skill.store.GetActiveEnvelope(ctx, envelopeTestChatID)
	if err != nil {
		t.Fatalf("GetActiveEnvelope: %v", err)
	}
	if !ok {
		t.Fatal("конверт не создан")
	}

	// --- ФАКТ В БАЗЕ: доли записаны ---
	shares, err := skill.store.ListShares(ctx, envelopeTestChatID, env.ID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(shares) == 0 {
		t.Fatal("конверт сохранён БЕЗ раскладки: в budget_envelope_share нет ни одной доли")
	}

	// Инвариант ADR-008 §4: Σ allocated = FreeAfterObligations. Свободные деньги
	// считаются здесь заново из стора, а не парсятся из ответа: иначе тест
	// сверял бы текст сам с собой.
	rates, err := skill.store.GetExchangeRates(ctx)
	if err != nil {
		t.Fatalf("rates: %v", err)
	}
	incomeTHB, okRate := budget.ToTHB(127000, "RUB", rates)
	if !okRate {
		t.Fatal("нет курса RUB→THB")
	}
	snap, err := skill.store.GetPeriodSnapshot(ctx, envelopeTestChatID, env.PeriodStart, env.PeriodEnd, rates)
	if err != nil {
		t.Fatalf("GetPeriodSnapshot: %v", err)
	}
	plannedTHB, _, err := skill.store.PlannedExpensesTHB(ctx, envelopeTestChatID, rates)
	if err != nil {
		t.Fatalf("PlannedExpensesTHB: %v", err)
	}
	wantFree := incomeTHB - snap.UpcomingRecurring - snap.ActiveDebtDue - plannedTHB

	var sum float64
	var hasFallback, hasSavings bool
	for _, sh := range shares {
		sum += sh.Allocated
		if strings.EqualFold(sh.Name, budget.FallbackShareName) {
			hasFallback = true
		}
		if sh.Kind == budget.ShareKindSave {
			hasSavings = true
		}
	}
	if math.Abs(sum-wantFree) > 0.01 {
		t.Errorf("Σ allocated = %.2f, а свободных %.2f — раскладка не сходится (ADR-008 §4)", sum, wantFree)
	}
	if !hasFallback {
		t.Errorf("нет доли-приёмника «%s»: трате в неизвестной категории некуда падать", budget.FallbackShareName)
	}
	if !hasSavings {
		t.Error("нет доли с kind=save: непокрытый остаток потерян")
	}

	// --- ОТВЕТ: строка по каждой доле + обязательные строки ---
	for _, sh := range shares {
		if sh.Kind == budget.ShareKindSave {
			continue // накопления показываются строкой «Свободно»
		}
		if !strings.Contains(strings.ToLower(reply), strings.ToLower(sh.Name)) {
			t.Errorf("в ответе нет строки по доле %q:\n%s", sh.Name, reply)
		}
	}
	// Строки утверждённого оператором формата (simpleAI-faeq.11). Прежние
	// «Приход / Обязательства / К раскладке / Вне конвертов» он отверг: сводная
	// строка обязательств прятала и сумму, и повод, а «вне конвертов» показывала
	// 0 ฿ при аренде 18 000, потому что считала ФАКТ прошедших трат.
	for _, want := range []string{"Пришло", "Курс", "Куда уйдут", "На день"} {
		if !strings.Contains(reply, want) {
			t.Errorf("в ответе нет обязательной строки %q:\n%s", want, reply)
		}
	}
	// Знак валюты у каждой суммы (simpleAI-302i): без него колонку нельзя
	// прочитать — баты там или рубли.
	if !strings.Contains(reply, "฿") {
		t.Errorf("в ответе нет знака валюты:\n%s", reply)
	}

	// Повторный приход деактивирует прошлый конверт, а не плодит второй активный.
	if _, err := skill.Run(ctx, `{"action":"start_envelope","amount":50000,"currency":"RUB"}`); err != nil {
		t.Fatalf("второй start_envelope: %v", err)
	}
	var activeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM budget_envelope WHERE chat_id = $1 AND active`, envelopeTestChatID).Scan(&activeCount); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("активных конвертов %d, ожидали 1", activeCount)
	}
}

// TestStartEnvelope_WarningsInReply — warnings аллокатора доезжают до ответа.
// Триггер детерминированный: ручной лимит на «накопления» запрещён всегда
// (они — непокрытый остаток, а не лимит), поэтому warning не зависит от того,
// какая история трат лежит на реплике.
func TestStartEnvelope_WarningsInReply(t *testing.T) {
	skill, _, ctx := newEnvelopeTestSkill(t)

	if err := skill.store.SetOverride(ctx, envelopeTestChatID, "накопления", 5000, "RUB"); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	reply, err := skill.Run(ctx, `{"action":"start_envelope","amount":127000,"currency":"RUB"}`)
	if err != nil {
		t.Fatalf("start_envelope: %v", err)
	}
	if !strings.Contains(reply, "⚠️") || !strings.Contains(reply, "накопления") {
		t.Errorf("warning аллокатора не доехал до ответа:\n%s", reply)
	}
}

// TestCreateEnvelopeWithShares_Atomic — конверт и раскладка пишутся одной
// транзакцией. Проверяется по факту отката: доли с дублем имени отбиваются
// UNIQUE(envelope_id,name), и после ошибки конверта в базе быть НЕ должно.
// Без общей транзакции конверт остался бы активным и пустым.
func TestCreateEnvelopeWithShares_Atomic(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)

	from := time.Now()
	dup := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 100, Source: budget.ShareSourceAuto, Position: 0},
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 200, Source: budget.ShareSourceAuto, Position: 1},
	}
	if _, err := skill.store.CreateEnvelopeWithShares(ctx, envelopeTestChatID, 1000, "RUB", from, from.AddDate(0, 0, 14), dup, time.Now()); err == nil {
		t.Fatal("дубль имени доли должен отбиваться UNIQUE(envelope_id,name)")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM budget_envelope WHERE chat_id = $1`, envelopeTestChatID).Scan(&count); err != nil {
		t.Fatalf("count envelopes: %v", err)
	}
	if count != 0 {
		t.Errorf("после провала раскладки осталось %d конвертов — запись не атомарна", count)
	}
}
