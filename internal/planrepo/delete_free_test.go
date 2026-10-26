package planrepo

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Удаление транзакции возвращает деньги в «Свободно» (сквозной тест на БД: хранилище → ридер → расчёт окна).
//
// Случай А: факт расхода без связи с планом. «Свободно» после удаления = «Свободно» до траты.
// Случай Б: факт закрыл плановый платёж. Удаление открывает платёж заново, поэтому «Свободно» возвращается к
// значению ДО оплаты (платёж снова вычитается как плановый), а не растёт на его сумму.
//
// Мутации: убрать DELETE в DeleteTx — «Свободно» не вернётся; убрать UPDATE settled=false — в случае Б плановая запись
// останется закрытой, «Свободно» вырастет на сумму платежа и тест краснеет.
func TestDeleteTransaction_ReturnsMoneyToFree(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — DB-тест пропущен (это НЕ зелёный результат)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := budget.NewStore(pool)
	const chat = int64(-880001)
	// Окно 2099-10-05..18 вне любых реальных данных; сегодня внутри окна.
	today := plan.Date{Year: 2099, Month: 10, Day: 10}
	scope := plan.Scope{ChatIDs: []int64{chat}}
	svc := plan.NewService(
		NewReader(store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithLimits(store).WithDismissed(store),
		NewWriter(store), func() plan.Date { return today }, scope, plan.WithFactWriter(NewFactWriter(store)))
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM tx_edit_log WHERE before->>'description' LIKE 'delete-free-test%'`) //nolint:errcheck // очистка после теста
		pool.Exec(ctx, `DELETE FROM budget_transaction WHERE description LIKE 'delete-free-test%'`)     //nolint:errcheck // очистка после теста
		pool.Exec(ctx, `DELETE FROM budget_planned_expense WHERE chat_id = $1`, chat)                   //nolint:errcheck // очистка после теста
	}
	cleanup()
	t.Cleanup(cleanup)

	// Поступление окна: плановое на 2099-10-05, закрываем «пришло».
	if _, err := store.AddPlanItem(ctx, budget.PlanItemInput{ChatID: chat, Direction: "income", Description: "delete-free-test доход",
		AmountMinor: ptr(10_000_000), Currency: "RUB", DueDate: tp(2099, 10, 5)}); err != nil {
		t.Fatal(err)
	}
	free := func() int64 {
		t.Helper()
		res, err := svc.ViewWith(ctx, plan.CurRUB, plan.ViewOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return res.View.CanSpend
	}
	base := free()

	// Случай А: расход без связи с планом.
	factA := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, added_by)
		VALUES ($1, 'expense', 1000, 'RUB', 'delete-free-test А', '2099-10-08', 'bot')`, factA); err != nil {
		t.Fatal(err)
	}
	if got := free(); got != base-1000 {
		t.Fatalf("после траты 1000 «Свободно» %d, ждали %d", got, base-1000)
	}
	row, err := store.GetTx(ctx, factA)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTx(ctx, factA, row.UpdatedAt, budget.TxSourceWeb); err != nil {
		t.Fatal(err)
	}
	if got := free(); got != base {
		t.Errorf("А: после удаления траты «Свободно» %d, ждали %d (до траты)", got, base)
	}

	// Случай Б: плановый платёж 2000 ₽ закрыт оплатой, затем оплата удалена.
	planned, err := store.AddPlanItem(ctx, budget.PlanItemInput{ChatID: chat, Direction: "expense", Description: "delete-free-test платёж",
		AmountMinor: ptr(200_000), Currency: "RUB", DueDate: tp(2099, 10, 9)})
	if err != nil {
		t.Fatal(err)
	}
	beforePay := free() // платёж уже вычитается как плановый
	if beforePay != base-2000 {
		t.Fatalf("плановый платёж 2000 не вычтен: %d, ждали %d", beforePay, base-2000)
	}
	if err := svc.Settle(ctx, "planned:"+planned.String(), nil); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := free(); got != beforePay {
		t.Fatalf("после «оплачено» «Свободно» %d, ждали то же, что до оплаты (%d)", got, beforePay)
	}
	var factID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM budget_transaction WHERE planned_id = $1`, planned).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	row, err = store.GetTx(ctx, factID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTx(ctx, factID, row.UpdatedAt, budget.TxSourceWeb); err != nil {
		t.Fatal(err)
	}
	if got := free(); got != beforePay {
		t.Errorf("Б: после удаления оплаты «Свободно» %d, ждали %d: платёж снова плановый и вычитается", got, beforePay)
	}
	var settled bool
	if err := pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, planned).Scan(&settled); err != nil || settled {
		t.Errorf("плановая запись не открыта заново: settled=%v %v", settled, err)
	}
}

func ptr(n int64) *int64 { return &n }

func tp(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}
