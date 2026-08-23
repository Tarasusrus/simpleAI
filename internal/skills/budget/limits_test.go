package budgetskill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// Ручная правка лимита конверта (ADR-008 §2, задача simpleAI-faeq.8).
//
// Тесты интеграционные: проверяемое поведение — «правка сохранилась в базе И
// применилась к раскладке», и оба слагаемых наблюдаемы только через БД. Мок
// стора здесь доказывал бы только то, что мок вызвали.

// shareLimitEnv поднимает стор к реплике. Без write-доступа тест пропускается:
// правка лимита ПИШЕТ (override + перезапись долей), read-only реплика её не
// проверит.
func shareLimitEnv(t *testing.T) (*budget.Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return budget.NewStore(pool), pool
}

func cleanupChat(t *testing.T, pool *pgxpool.Pool, chatID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE chat_id = $1`, chatID); err != nil {
		t.Logf("cleanup budget_envelope: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope_limit_override WHERE chat_id = $1`, chatID); err != nil {
		t.Logf("cleanup override: %v", err)
	}
}

// findShare ищет долю по имени регистронезависимо — имя доли приходит из
// прогноза в отображаемом регистре («Еда»), а оператор называет её как угодно.
func findShare(shares []budget.EnvelopeShare, name string) (budget.EnvelopeShare, bool) {
	for _, sh := range shares {
		if strings.EqualFold(strings.TrimSpace(sh.Name), name) {
			return sh, true
		}
	}
	return budget.EnvelopeShare{}, false
}

func mustRun(t *testing.T, skill *BudgetSkill, ctx context.Context, input string) string {
	t.Helper()
	out, err := skill.Run(ctx, input)
	if err != nil {
		t.Fatalf("Run(%s): %v", input, err)
	}
	return out
}

// overrideTHB — сколько THB должен получить конверт с ручным лимитом amount в
// currency. Считается ТЕМ ЖЕ курсом, что и раскладка: сверять с константой
// значило бы вшить курс дня в тест.
func overrideTHB(t *testing.T, store *budget.Store, amount float64, currency string) float64 {
	t.Helper()
	rates, err := store.GetExchangeRates(context.Background())
	if err != nil {
		t.Fatalf("курсы: %v", err)
	}
	thb, ok := budget.ToTHB(amount, currency, rates)
	if !ok {
		t.Fatalf("нет курса %s", currency)
	}
	return thb
}

// «на еду хватит 15000»: правка обязана и сохраниться правилом (override в базе),
// и немедленно применяться к ТЕКУЩЕМУ конверту — оператор поправил лимит, чтобы
// пользоваться им сейчас, а не со следующего прихода.
func TestSetShareLimit_SavesOverrideAndReplansActiveEnvelope(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatID = int64(-70031)
	cleanupChat(t, pool, chatID)
	t.Cleanup(func() { cleanupChat(t, pool, chatID) })

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, chatID)
	from := time.Now()
	envID, err := store.CreateEnvelope(ctx, chatID, 300000, "RUB", from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}

	skill := NewBudgetSkill(store)
	reply := mustRun(t, skill, ctx, `{"action":"set_share_limit","name":"еда","amount":15000,"currency":"RUB"}`)
	if !strings.Contains(reply, "еда") {
		t.Errorf("в ответе нет имени конверта: %q", reply)
	}

	// 1. Правило сохранено — иначе оно не переживёт текущий конверт.
	overrides, err := store.ListOverrides(ctx, chatID)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	if len(overrides) != 1 || overrides[0].ShareName != "еда" || overrides[0].Amount != 15000 || overrides[0].Currency != "RUB" {
		t.Fatalf("ожидали один override «еда» 15000 RUB, got %+v", overrides)
	}

	// 2. Текущий конверт пересчитан — доля «еда» ровно на заданную сумму.
	shares, err := store.ListShares(ctx, chatID, envID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(shares) == 0 {
		t.Fatal("после правки лимита у конверта нет ни одной доли — раскладку не перезаписали")
	}
	food, ok := findShare(shares, "еда")
	if !ok {
		t.Fatalf("в пересчитанной раскладке нет доли «еда»: %+v", shareNames(shares))
	}
	if food.Source != budget.ShareSourceOverride {
		t.Errorf("доля «еда» должна быть помечена как ручная, got source=%q", food.Source)
	}
	want := overrideTHB(t, store, 15000, "RUB")
	if diff := food.Allocated - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("лимит доли «еда» = %.2f ฿, ожидали %.2f ฿", food.Allocated, want)
	}
}

// «убери лимит на еду»: правило удаляется, а конверт пересчитывается обратно —
// доля перестаёт быть ручной. Проверяем оба следствия: удалённая строка без
// пересчёта оставила бы на экране старый лимит.
func TestClearShareLimit_DeletesOverrideAndReplans(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatID = int64(-70032)
	cleanupChat(t, pool, chatID)
	t.Cleanup(func() { cleanupChat(t, pool, chatID) })

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, chatID)
	from := time.Now()
	envID, err := store.CreateEnvelope(ctx, chatID, 300000, "RUB", from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}

	skill := NewBudgetSkill(store)
	mustRun(t, skill, ctx, `{"action":"set_share_limit","name":"еда","amount":15000,"currency":"RUB"}`)
	mustRun(t, skill, ctx, `{"action":"clear_share_limit","name":"еда"}`)

	overrides, err := store.ListOverrides(ctx, chatID)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("после снятия лимита override'ов быть не должно, got %+v", overrides)
	}

	shares, err := store.ListShares(ctx, chatID, envID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(shares) == 0 {
		t.Fatal("после снятия лимита у конверта нет ни одной доли — раскладку не перезаписали")
	}
	if food, ok := findShare(shares, "еда"); ok && food.Source == budget.ShareSourceOverride {
		t.Errorf("доля «еда» осталась ручной после снятия лимита: %+v", food)
	}
}

// Главное свойство правки: она — ПРАВИЛО, а не разовая подмена. Следующий приход
// раскладывается уже с ней, хотя конверт другой и раскладка считается заново.
//
// Мутация, под которую написан тест: если setShareLimit перестанет писать
// override в базу и ограничится пересчётом текущего конверта, этот тест краснеет
// (в новом конверте доля «еда» вернётся к авто-лимиту), а два предыдущих — нет.
func TestShareLimitOverride_SurvivesNextEnvelope(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatID = int64(-70033)
	cleanupChat(t, pool, chatID)
	t.Cleanup(func() { cleanupChat(t, pool, chatID) })

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, chatID)
	skill := NewBudgetSkill(store)

	from := time.Now()
	if _, err := store.CreateEnvelope(ctx, chatID, 300000, "RUB", from, from.AddDate(0, 0, 14)); err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	mustRun(t, skill, ctx, `{"action":"set_share_limit","name":"еда","amount":15000,"currency":"RUB"}`)

	// Следующий приход — обычным путём оператора, а не подкладыванием строк в БД.
	mustRun(t, skill, ctx, `{"action":"start_envelope","amount":300000,"currency":"RUB"}`)

	env, ok, err := store.GetActiveEnvelope(ctx, chatID)
	if err != nil || !ok {
		t.Fatalf("активный конверт после нового прихода: ok=%v err=%v", ok, err)
	}
	shares, err := store.ListShares(ctx, chatID, env.ID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	food, ok := findShare(shares, "еда")
	if !ok {
		t.Fatalf("в новом конверте нет доли «еда»: %v", shareNames(shares))
	}
	if food.Source != budget.ShareSourceOverride {
		t.Fatalf("ручной лимит не пережил новый приход: доля «еда» source=%q, allocated=%.2f", food.Source, food.Allocated)
	}
	want := overrideTHB(t, store, 15000, "RUB")
	if diff := food.Allocated - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("в новом конверте лимит «еда» = %.2f ฿, ожидали %.2f ฿", food.Allocated, want)
	}
}

// carried_in — факт прошлого периода (ADR-008 §9), а не результат раскладки.
// Правка лимита перезаписывает доли целиком, поэтому перенос обязан быть
// сохранён явно: иначе одна фраза «на еду хватит 15000» молча обнулила бы
// накопленное в конверте «Отпуск».
func TestSetShareLimit_KeepsCarriedIn(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatID = int64(-70034)
	cleanupChat(t, pool, chatID)
	t.Cleanup(func() { cleanupChat(t, pool, chatID) })

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, chatID)
	from := time.Now()
	envID, err := store.CreateEnvelope(ctx, chatID, 300000, "RUB", from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	if err := store.CreateShares(ctx, envID, []budget.EnvelopeShare{
		{Name: "Отпуск", Kind: budget.ShareKindSave, Allocated: 1000, CarriedIn: 2500, Source: budget.ShareSourceOverride, Position: 0},
		{Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Allocated: 0, Source: budget.ShareSourceAuto, Position: 1},
	}); err != nil {
		t.Fatalf("CreateShares: %v", err)
	}
	if err := store.SetOverride(ctx, chatID, "отпуск", 1000, "THB"); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}

	skill := NewBudgetSkill(store)
	mustRun(t, skill, ctx, `{"action":"set_share_limit","name":"еда","amount":15000,"currency":"RUB"}`)

	shares, err := store.ListShares(ctx, chatID, envID)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	vacation, ok := findShare(shares, "отпуск")
	if !ok {
		t.Fatalf("доля «Отпуск» исчезла после пересчёта: %v", shareNames(shares))
	}
	if vacation.CarriedIn != 2500 {
		t.Errorf("перенос с прошлого конверта обнулён: carried_in = %.2f, ожидали 2500", vacation.CarriedIn)
	}
}

// Правка без активного конверта — не ошибка: правило сохраняется и ждёт прихода.
func TestSetShareLimit_NoActiveEnvelope(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatID = int64(-70035)
	cleanupChat(t, pool, chatID)
	t.Cleanup(func() { cleanupChat(t, pool, chatID) })

	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, chatID)
	skill := NewBudgetSkill(store)
	reply := mustRun(t, skill, ctx, `{"action":"set_share_limit","name":"еда","amount":15000,"currency":"RUB"}`)
	if !strings.Contains(reply, "следующий приход") {
		t.Errorf("ответ должен объяснять, что лимит применится к следующему приходу: %q", reply)
	}
	overrides, err := store.ListOverrides(ctx, chatID)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("ожидали сохранённый override, got %+v", overrides)
	}
}

// Чужой конверт нельзя переписать даже с валидным envelope_id (ADR-004): у долей
// своего chat_id нет, изоляция держится только этой проверкой.
func TestReplaceShares_ForeignEnvelopeRejected(t *testing.T) {
	store, pool := shareLimitEnv(t)
	const chatA, chatB = int64(-70036), int64(-70037)
	cleanupChat(t, pool, chatA)
	cleanupChat(t, pool, chatB)
	t.Cleanup(func() { cleanupChat(t, pool, chatA); cleanupChat(t, pool, chatB) })

	ctx := context.Background()
	from := time.Now()
	envA, err := store.CreateEnvelope(ctx, chatA, 100000, "RUB", from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	err = store.ReplaceShares(ctx, chatB, envA, []budget.EnvelopeShare{
		{Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Source: budget.ShareSourceAuto},
	})
	if err == nil {
		t.Fatal("чужой конверт переписан — изоляция по chat_id не работает")
	}
	if _, err := store.ListShares(ctx, chatA, envA); err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if err := store.ReplaceShares(ctx, chatA, uuid.Nil, nil); err == nil {
		t.Error("пустой envelopeID должен отбиваться")
	}
}

// shareNames — имена долей для сообщений об ошибке.
func shareNames(shares []budget.EnvelopeShare) []string {
	out := make([]string, 0, len(shares))
	for _, sh := range shares {
		out = append(out, fmt.Sprintf("%s(%s,%.0f)", sh.Name, sh.Source, sh.Allocated))
	}
	return out
}
