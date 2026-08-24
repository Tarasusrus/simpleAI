package budget

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Матчинг категории в долю (чистая логика, БД не нужна) ---

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }

// shareFixture — раскладка из трёх долей: «еда» с категорией по id,
// «жильё» с категорией только по имени, и fallback «прочее» без категорий.
func shareFixture(foodID uuid.UUID) []EnvelopeShare {
	return []EnvelopeShare{
		{
			Name: "Еда", Kind: ShareKindSpend, Allocated: 12000, Source: ShareSourceAuto, Position: 0,
			Categories: []EnvelopeShareCategory{{CategoryID: ptrUUID(foodID), CategoryName: "еда"}},
		},
		{
			Name: "Жильё", Kind: ShareKindSpend, Allocated: 20000, Source: ShareSourceOverride, Position: 1,
			Categories: []EnvelopeShareCategory{{CategoryName: "жильё"}},
		},
		{
			Name: "Прочее", Kind: ShareKindSpend, Allocated: 3000, Source: ShareSourceAuto, Position: 2,
		},
	}
}

// Транзакция с category_id матчится в долю по id, даже если имя пришло другое
// (в БД имя могло быть переименовано после раскладки).
func TestResolveShare_ByCategoryID(t *testing.T) {
	foodID := uuid.New()
	shares := shareFixture(foodID)

	got := ResolveShare(shares, &foodID, "Продукты и кафе")
	if got == nil || got.Name != "Еда" {
		t.Fatalf("ожидали долю «Еда» по category_id, got %v", got)
	}
}

// Без category_id матчинг идёт по имени и обязан быть регистронезависимым:
// уникальный индекс budget_category(name,type) регистрозависим, поэтому в
// транзакциях встречаются и «Жильё», и «жильё», и «ЖИЛЬЁ».
func TestResolveShare_ByNameCaseInsensitive(t *testing.T) {
	shares := shareFixture(uuid.New())

	for _, name := range []string{"жильё", "Жильё", "ЖИЛЬЁ", "  Жильё  "} {
		got := ResolveShare(shares, nil, name)
		if got == nil || got.Name != "Жильё" {
			t.Errorf("имя %q: ожидали долю «Жильё», got %v", name, got)
		}
	}
}

// Нормализация нужна с ОБЕИХ сторон: раскладка, собранная в памяти (до
// CreateShares, который сам приводит имена к нижнему регистру), держит имена
// категорий как есть — «Развлечения». Матч обязан состояться и в этом случае.
func TestResolveShare_StoredNameNotNormalized(t *testing.T) {
	shares := []EnvelopeShare{
		{Name: "Развлечения", Kind: ShareKindSpend, Position: 0,
			Categories: []EnvelopeShareCategory{{CategoryName: "Развлечения"}}},
		{Name: "Прочее", Kind: ShareKindSpend, Position: 1},
	}

	for _, name := range []string{"развлечения", "Развлечения", "РАЗВЛЕЧЕНИЯ"} {
		got := ResolveShare(shares, nil, name)
		if got == nil || got.Name != "Развлечения" {
			t.Errorf("имя %q: ожидали долю «Развлечения», got %v", name, got)
		}
	}
}

// category_id IS NULL и имени нет — трата обязана уйти в fallback «прочее»,
// а не потеряться и не попасть в первую попавшуюся долю.
func TestResolveShare_NilCategoryGoesToFallback(t *testing.T) {
	shares := shareFixture(uuid.New())

	got := ResolveShare(shares, nil, "")
	if got == nil || got.Name != "Прочее" {
		t.Fatalf("трата без категории должна попасть в «Прочее», got %v", got)
	}

	// uuid.Nil трактуется как отсутствие категории.
	nilID := uuid.Nil
	if got := ResolveShare(shares, &nilID, ""); got == nil || got.Name != "Прочее" {
		t.Fatalf("uuid.Nil должен вести в «Прочее», got %v", got)
	}
}

// Неизвестная категория (есть и id, и имя, но ни одна доля их не содержит) —
// тоже в fallback.
func TestResolveShare_UnknownCategoryGoesToFallback(t *testing.T) {
	shares := shareFixture(uuid.New())
	other := uuid.New()

	got := ResolveShare(shares, &other, "Развлечения")
	if got == nil || got.Name != "Прочее" {
		t.Fatalf("неизвестная категория должна попасть в «Прочее», got %v", got)
	}
}

// Доля заведена по имени без id (категории не было в budget_category на момент
// раскладки), а транзакция пришла уже с id — матч обязан состояться по имени.
func TestResolveShare_IDMissFallsBackToName(t *testing.T) {
	shares := shareFixture(uuid.New())
	housingID := uuid.New()

	got := ResolveShare(shares, &housingID, "Жильё")
	if got == nil || got.Name != "Жильё" {
		t.Fatalf("ожидали матч по имени при промахе по id, got %v", got)
	}
}

// Раскладка без fallback-доли: возвращаем nil, а не случайную долю — вызывающий
// сам решает, что делать с нераспределённой тратой.
func TestResolveShare_NoFallbackShare(t *testing.T) {
	shares := shareFixture(uuid.New())[:2] // без «Прочее»

	if got := ResolveShare(shares, nil, "Развлечения"); got != nil {
		t.Fatalf("без fallback-доли ожидали nil, got %v", got)
	}
}

// --- Контракт схемы ---

const shareMigrationPath = "../db/migrations/00017_budget_envelope_shares.sql"

// squashSQL убирает переносы и лишние пробелы, чтобы искать конструкции
// независимо от форматирования.
func squashSQL(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("читаю миграцию: %v", err)
	}
	return strings.ToLower(regexp.MustCompile(`\s+`).ReplaceAllString(string(raw), " "))
}

// Имя доли — ключ переноса накоплений между приходами: остаток «Отпуска» из
// прошлого периода ищется в новом ПО ИМЕНИ. Две доли с одним именем в одном
// конверте делают перенос неоднозначным. Уникальность должна быть структурной
// (constraint в схеме), а не проверкой в коде — иначе гонка двух раскладок
// пролезет мимо. Тест на неоднозначность ключа переноса.
//
// Прогон в CI без БД защищает только этот инвариант схемы; поведенческую
// проверку (INSERT дубликата отбивается) делает TestEnvelopeShares_Integration.
func TestShareMigration_TransferKeyIsUnambiguous(t *testing.T) {
	sql := squashSQL(t, shareMigrationPath)

	unique := regexp.MustCompile(`unique\s*\(\s*envelope_id\s*,\s*name\s*\)`)
	if !unique.MatchString(sql) {
		t.Fatal("в 00017 нет UNIQUE(envelope_id, name): ключ переноса накоплений между приходами становится неоднозначным — две доли с одним именем в одном конверте")
	}
}

// Доли и их категории не должны переживать свой конверт: осиротевшая раскладка
// молча исказит следующий период.
func TestShareMigration_CascadeFromEnvelope(t *testing.T) {
	sql := squashSQL(t, shareMigrationPath)

	for _, want := range []string{
		"references budget_envelope(id) on delete cascade",
		"references budget_envelope_share(id) on delete cascade",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("в 00017 нет %q — раскладка переживёт удаление конверта", want)
		}
	}
}

// Миграция обязана иметь down-секцию: без неё goose down на чистой базе не
// откатывается.
func TestShareMigration_HasDown(t *testing.T) {
	raw, err := os.ReadFile(shareMigrationPath)
	if err != nil {
		t.Fatalf("читаю миграцию: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "-- +goose Down") {
		t.Fatal("в 00017 нет секции -- +goose Down")
	}
	for _, table := range []string{"budget_envelope_share", "budget_envelope_share_category", "budget_envelope_limit_override"} {
		if !strings.Contains(text, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("down-секция не удаляет %s", table)
		}
	}
}

// --- Интеграция с БД ---

// TestEnvelopeShares_Integration проверяет каскадное удаление долей вместе с
// конвертом, изоляцию по chat_id и отбой дубликата имени доли. Требует WRITE —
// BOTCLIENT_DATABASE_URL_RW; иначе пропуск.
func TestEnvelopeShares_Integration(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	s := NewStore(pool)

	const chatA, chatB = int64(-70011), int64(-70012)
	cleanup := func() {
		if _, err := pool.Exec(ctx, "DELETE FROM budget_envelope WHERE chat_id IN ($1,$2)", chatA, chatB); err != nil {
			t.Logf("cleanup budget_envelope: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM budget_envelope_limit_override WHERE chat_id IN ($1,$2)", chatA, chatB); err != nil {
			t.Logf("cleanup override: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)
	envA, err := s.CreateEnvelope(ctx, chatA, 127000, "RUB", from, to)
	if err != nil {
		t.Fatalf("create envelope A: %v", err)
	}

	// Категория из справочника — для матчинга по id.
	var foodID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE type = 'expense' ORDER BY sort_order LIMIT 1`).Scan(&foodID); err != nil {
		t.Fatalf("нет ни одной расходной категории: %v", err)
	}

	shares := []EnvelopeShare{
		{Name: "Еда", Kind: ShareKindSpend, Allocated: 12000, Source: ShareSourceAuto, Position: 0,
			Categories: []EnvelopeShareCategory{{CategoryID: &foodID, CategoryName: "ЕДА"}}},
		{Name: "Отпуск", Kind: ShareKindSave, Allocated: 5000, CarriedIn: 1500, Source: ShareSourceOverride, Position: 1},
		{Name: "Прочее", Kind: ShareKindSpend, Allocated: 3000, Source: ShareSourceAuto, Position: 2},
	}
	if err := s.CreateShares(ctx, envA, shares); err != nil {
		t.Fatalf("CreateShares: %v", err)
	}

	got, err := s.ListShares(ctx, chatA, envA)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ожидали 3 доли, got %d", len(got))
	}
	if got[0].Name != "Еда" || len(got[0].Categories) != 1 {
		t.Fatalf("первая доля должна быть «Еда» с одной категорией, got %+v", got[0])
	}
	// Имя категории обязано лечь в нижнем регистре — это ключ матчинга.
	if got[0].Categories[0].CategoryName != "еда" {
		t.Errorf("category_name должно храниться в нижнем регистре, got %q", got[0].Categories[0].CategoryName)
	}
	if got[1].CarriedIn != 1500 {
		t.Errorf("carried_in потерян: got %v", got[1].CarriedIn)
	}
	// Матчинг по id на реальных данных.
	if sh := ResolveShare(got, &foodID, "что угодно"); sh == nil || sh.Name != "Еда" {
		t.Errorf("матч по category_id не сработал: %v", sh)
	}
	// Матчинг по имени в другом регистре.
	if sh := ResolveShare(got, nil, "Еда"); sh == nil || sh.Name != "Еда" {
		t.Errorf("матч по имени не сработал: %v", sh)
	}
	// category_id IS NULL и неизвестное имя — в fallback.
	if sh := ResolveShare(got, nil, "Развлечения"); sh == nil || sh.Name != "Прочее" {
		t.Errorf("fallback не сработал: %v", sh)
	}

	// Дубликат имени доли в одном конверте отбивается схемой: имя — ключ
	// переноса накоплений, второй «Отпуск» сделал бы перенос неоднозначным.
	dup := []EnvelopeShare{{Name: "Отпуск", Kind: ShareKindSave, Allocated: 999, Source: ShareSourceAuto, Position: 9}}
	if err := s.CreateShares(ctx, envA, dup); err == nil {
		t.Error("вторая доля с именем «Отпуск» в том же конверте должна быть отбита UNIQUE(envelope_id, name)")
	}

	// chat-scope: chatB не видит раскладку chatA даже зная envelope_id.
	if other, err := s.ListShares(ctx, chatB, envA); err != nil {
		t.Fatalf("ListShares chatB: %v", err)
	} else if len(other) != 0 {
		t.Errorf("chat-scope нарушен: chatB прочитал %d долей чужого конверта", len(other))
	}

	// Каскад: удаление конверта уносит доли и их категории.
	var catCount int
	if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE id = $1`, envA); err != nil {
		t.Fatalf("delete envelope: %v", err)
	}
	var shareCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM budget_envelope_share WHERE envelope_id = $1`, envA).Scan(&shareCount); err != nil {
		t.Fatalf("count shares: %v", err)
	}
	if shareCount != 0 {
		t.Errorf("доли пережили удаление конверта: %d", shareCount)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM budget_envelope_share_category sc
		LEFT JOIN budget_envelope_share sh ON sh.id = sc.share_id
		WHERE sh.id IS NULL
	`).Scan(&catCount); err != nil {
		t.Fatalf("count orphan categories: %v", err)
	}
	if catCount != 0 {
		t.Errorf("осиротевшие категории долей: %d", catCount)
	}

	// Ручные лимиты: upsert, изоляция по chat_id, удаление.
	if err := s.SetOverride(ctx, chatA, "Отпуск", 8000, "THB"); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	if err := s.SetOverride(ctx, chatA, "отпуск ", 9000, "THB"); err != nil {
		t.Fatalf("SetOverride upsert: %v", err)
	}
	ovs, err := s.ListOverrides(ctx, chatA)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	if len(ovs) != 1 || ovs[0].Amount != 9000 {
		t.Fatalf("ожидали один лимит на 9000 (upsert по нормализованному имени), got %+v", ovs)
	}
	if ovs[0].ShareName != "отпуск" {
		t.Errorf("share_name должно нормализоваться, got %q", ovs[0].ShareName)
	}
	if other, err := s.ListOverrides(ctx, chatB); err != nil || len(other) != 0 {
		t.Errorf("chat-scope лимитов нарушен: %d (err=%v)", len(other), err)
	}
	if err := s.DeleteOverride(ctx, chatA, "ОТПУСК"); err != nil {
		t.Fatalf("DeleteOverride: %v", err)
	}
	if ovs, err := s.ListOverrides(ctx, chatA); err != nil || len(ovs) != 0 {
		t.Errorf("лимит не удалён: %d (err=%v)", len(ovs), err)
	}
}
