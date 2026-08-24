package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Перезапись раскладки уже заведённого конверта (ADR-008 §2): оператор поправил
// лимит доли словами — базовый прогноз пересчитывается, override ложится сверху,
// доли переписываются. Отдельный файл от store.go: запись раскладки «поверх»
// живёт только здесь и не путается со вставкой новой раскладки (CreateShares /
// CreateEnvelopeWithShares).

// ReplaceShares перезаписывает раскладку конверта ОДНОЙ транзакцией: старые доли
// удаляются, новые встают на их место.
//
// Одна транзакция — не оптимизация. Между DELETE и INSERT конверт остаётся без
// единой доли: budget.ResolveShare в этот момент возвращает nil (трате некуда
// падать), а «сколько осталось в конвертах» отвечать нечем. Двумя транзакциями
// это состояние становится наблюдаемым при любой ошибке между ними.
//
// Принадлежность конверта чату проверяется внутри той же транзакции: у долей
// своего chat_id нет (ADR-004 изоляция держится через budget_envelope), и без
// проверки чужой envelope_id переписал бы чужую раскладку.
func (s *Store) ReplaceShares(ctx context.Context, chatID int64, envelopeID uuid.UUID, shares []EnvelopeShare) error {
	if envelopeID == uuid.Nil {
		return fmt.Errorf("ReplaceShares: envelopeID пуст")
	}
	if len(shares) == 0 {
		// Пустая раскладка стёрла бы доли и не поставила ни одной новой. Это не
		// «нечего делать» (как в CreateShares, где конверт ещё без долей), а
		// порча уже рабочего состояния — поэтому ошибка, а не тихий выход.
		return fmt.Errorf("ReplaceShares: пустая раскладка")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ReplaceShares begin: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.Warn("ReplaceShares rollback", "err", rbErr)
		}
	}()

	var owned bool
	err = tx.QueryRow(ctx, `
		SELECT true FROM budget_envelope WHERE id = $1 AND chat_id = $2
	`, envelopeID, chatID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("ReplaceShares: конверт %s не найден в чате %d", envelopeID, chatID)
	}
	if err != nil {
		return fmt.Errorf("ReplaceShares owner check: %w", err)
	}

	// Категории долей уходят каскадом (FK ON DELETE CASCADE в 00017).
	if _, err := tx.Exec(ctx, `DELETE FROM budget_envelope_share WHERE envelope_id = $1`, envelopeID); err != nil {
		return fmt.Errorf("ReplaceShares delete: %w", err)
	}
	if err := insertSharesTx(ctx, tx, envelopeID, shares); err != nil {
		return fmt.Errorf("ReplaceShares insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ReplaceShares commit: %w", err)
	}
	return nil
}
