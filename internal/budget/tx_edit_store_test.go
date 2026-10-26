package budget

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Правка любой транзакции (бот, экран, правило): поля меняются, версия растёт, в журнале снимок «до» и значения
// «после». Устаревшая версия — конфликт, ничего не меняется и журнал не пишется.
//
// Мутации: убрать запись в tx_edit_log — краснеет проверка журнала; убрать сравнение версии — краснеет «конфликт»;
// не писать currency в SET — краснеет проверка валюты; писать category_id без COALESCE — краснеет «категория не тронута».
func TestEditTx(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	newDay := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	cat := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_category (id, name, type) VALUES ($1, 'plan-store-test cat', 'expense')`, cat); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM tx_edit_log WHERE before->>'description' LIKE 'plan-store-test%'`) //nolint:errcheck // очистка после теста: ошибка не влияет на результат
		s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE category_id = $1`, cat)                   //nolint:errcheck // очистка после теста: ошибка не влияет на результат
		s.pool.Exec(ctx, `DELETE FROM budget_category WHERE id = $1`, cat)                               //nolint:errcheck // очистка после теста: ошибка не влияет на результат
	})
	for _, by := range []string{"bot", "web"} {
		id := uuid.New()
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date, added_by)
			VALUES ($1, 'expense', 10, 'RUB', $2, 'plan-store-test fact', $3, $4)`, id, cat, day, by); err != nil {
			t.Fatal(err)
		}
		before, err := s.GetTx(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		change := TxChange{Description: "plan-store-test edited", AmountMinor: 12345, Currency: "THB", Date: newDay}
		got, err := s.EditTx(ctx, id, before.UpdatedAt, TxSourceWeb, change)
		if err != nil {
			t.Fatalf("%s: правка: %v", by, err)
		}
		if got.Description != change.Description || got.AmountMinor != 12345 || got.Currency != "THB" ||
			got.Date.Format("2006-01-02") != "2026-03-12" {
			t.Errorf("%s: после правки %+v", by, got)
		}
		if got.CategoryID == nil || *got.CategoryID != cat {
			t.Errorf("%s: категория тронута при CategoryID=nil: %v", by, got.CategoryID)
		}
		if !got.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("%s: версия не выросла", by)
		}
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tx_edit_log WHERE tx_id = $1 AND action = 'edit'
			AND before->>'description' = 'plan-store-test fact' AND after->>'description' = 'plan-store-test edited'`, id).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: в журнале записей правки: %d %v", by, n, err)
		}
		// Тот же снимок версии второй раз: строку уже изменили.
		if _, err := s.EditTx(ctx, id, before.UpdatedAt, TxSourceWeb, change); !errors.Is(err, ErrTxConflict) {
			t.Errorf("%s: устаревшая версия: %v, ждали ErrTxConflict", by, err)
		}
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tx_edit_log WHERE tx_id = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: конфликт записал в журнал: %d %v", by, n, err)
		}
	}
	if _, err := s.EditTx(ctx, uuid.New(), time.Now(), TxSourceWeb, TxChange{}); !errors.Is(err, ErrTxNotFound) {
		t.Errorf("нет транзакции: %v", err)
	}
}

// Удаление: строки нет, в журнале снимок целиком; оплата по плану открывает плановую запись (любая транзакция со
// связью planned_id, без ветки по происхождению); конфликт версий не удаляет ничего.
//
// Мутации: убрать UPDATE settled=false — краснеет «запись снова открыта»; убрать запись в журнал — краснеет «снимок»;
// убрать сравнение версии — краснеет конфликт; сделать DELETE до проверки planned — краснеет «чужая запись не тронута».
func TestDeleteTx(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM tx_edit_log WHERE before->>'description' LIKE 'plan-store-test%'`) //nolint:errcheck // очистка после теста: ошибка не влияет на результат
	})
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	planned, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{planned, other} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_planned_expense (id, chat_id, direction, description, amount, currency, settled)
			VALUES ($1, $2, 'expense', 'plan-store-test', 806, 'THB', true)`, id, planStoreChat); err != nil {
			t.Fatal(err)
		}
	}
	linked, plain := uuid.New(), uuid.New()
	for _, r := range []struct {
		id      uuid.UUID
		planned *uuid.UUID
	}{{linked, &planned}, {plain, nil}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, added_by, planned_id)
			VALUES ($1, 'expense', 806, 'THB', 'plan-store-test fact', $2, 'bot', $3)`, r.id, day, r.planned); err != nil {
			t.Fatal(err)
		}
	}
	row, err := s.GetTx(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTx(ctx, linked, row.UpdatedAt.Add(-time.Second), TxSourceWeb); !errors.Is(err, ErrTxConflict) {
		t.Fatalf("устаревшая версия: %v, ждали ErrTxConflict", err)
	}
	if _, err := s.GetTx(ctx, linked); err != nil {
		t.Fatalf("конфликт версий удалил строку: %v", err)
	}
	if err := s.DeleteTx(ctx, linked, row.UpdatedAt, TxSourceWeb); err != nil {
		t.Fatalf("удаление: %v", err)
	}
	if _, err := s.GetTx(ctx, linked); !errors.Is(err, ErrTxNotFound) {
		t.Errorf("строка осталась: %v", err)
	}
	var settled bool
	if err := s.pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, planned).Scan(&settled); err != nil || settled {
		t.Errorf("плановая запись не открыта: settled=%v %v", settled, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, other).Scan(&settled); err != nil || !settled {
		t.Errorf("чужая плановая запись тронута: settled=%v %v", settled, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tx_edit_log WHERE tx_id = $1 AND action = 'delete' AND after IS NULL
		AND before->>'description' = 'plan-store-test fact' AND before->>'planned_id' = $2`, linked, planned.String()).Scan(&n); err != nil || n != 1 {
		t.Errorf("снимок удаления в журнале: %d %v", n, err)
	}

	// Транзакция без связи с планом удаляется тем же путём.
	p, err := s.GetTx(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTx(ctx, plain, p.UpdatedAt, TxSourceWeb); err != nil {
		t.Fatalf("удаление без связи: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, other).Scan(&settled); err != nil || !settled {
		t.Errorf("удаление без связи тронуло плановую запись: settled=%v %v", settled, err)
	}
}
