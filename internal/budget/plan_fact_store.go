package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Правка и возврат в план закрытых записей (simpleAI-1inl, ADR-009).
//
// У budget_transaction нет chat_id (транзакции общие для всех чатов), поэтому
// границу владения держат два признака: added_by = AddedByWeb (факт создал экран) и
// recurring_id IS NULL (не факт правила повтора: его воркер пересоздаст при догоне).
// Факты бота и воркера эти методы не трогают.

// FactEdit — новые значения факта. Валюта, тип и категория не меняются: правка
// названия, суммы и даты не должна тихо перекидывать факт в другую категорию.
type FactEdit struct {
	Description string
	AmountMinor int64
	Date        time.Time
}

// UpdateWebFact меняет факт, созданный экраном плана. false — факта нет, он не от
// экрана или он порождён правилом повтора.
func (s *Store) UpdateWebFact(ctx context.Context, id uuid.UUID, e FactEdit) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_transaction SET
		    description      = $2,
		    amount           = $3::bigint::numeric / 100,
		    transaction_date = $4
		WHERE id = $1 AND added_by = $5 AND recurring_id IS NULL`,
		id, e.Description, e.AmountMinor, e.Date, AddedByWeb)
	if err != nil {
		return false, fmt.Errorf("update web fact: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UnsettlePlanItem возвращает оплаченную запись в план: в одной транзакции БД
// снова открывает плановую запись и удаляет факт, который её закрыл. Плановая
// запись должна лежать в перечисленных чатах — так область чатов проверяется там,
// где у транзакций chat_id нет. false — факта нет, он не от экрана, порождён
// правилом, закрыл не плановую запись (planned_id пуст: старый факт, развязать
// его по описанию и сумме нельзя) или записи нет в этих чатах; тогда ничего не
// меняется.
func (s *Store) UnsettlePlanItem(ctx context.Context, chatIDs []int64, txID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("unsettle plan item: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // после Commit откат — no-op

	var plannedID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT planned_id FROM budget_transaction
		WHERE id = $1 AND added_by = $2 AND recurring_id IS NULL AND planned_id IS NOT NULL
		FOR UPDATE`, txID, AddedByWeb).Scan(&plannedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("unsettle plan item: read: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE budget_planned_expense SET settled = false
		WHERE id = $1 AND chat_id = ANY($2) AND settled`, plannedID, chatIDs)
	if err != nil {
		return false, fmt.Errorf("unsettle plan item: reopen: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM budget_transaction WHERE id = $1`, txID); err != nil {
		return false, fmt.Errorf("unsettle plan item: delete fact: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("unsettle plan item: commit: %w", err)
	}
	return true, nil
}
