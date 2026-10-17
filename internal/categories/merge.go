package categories

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Таблицы журнала category_merge_log.
const (
	logCategory  = "budget_category" // снимок удалённой категории (есть всегда)
	logTx        = "budget_transaction"
	logPlanned   = "budget_planned_expense"
	logRecurring = "budget_recurring"
	logShare     = "budget_envelope_share_category"
	logShareDrop = "budget_envelope_share_category:dropped" // строка доли, схлопнутая с уже имеющейся целевой
	logAlias     = "budget_category_alias"
	mergeLogCols = `(table_name, row_id, old_category_id, new_category_id, old_category_name,
		merge_id, new_category_name, source_category_type, source_category_icon, source_category_sort, old_share_category_name)`
)

// Merge сливает Source в Target одной транзакцией (ADR-010 §3). Жёсткого удаления нет:
// старое имя остаётся алиасом новой категории.
func (s *Store) Merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	if req.Source == req.Target {
		return MergeResult{}, errors.New("слияние категории с самой собой")
	}
	if req.Decider == "" {
		req.Decider = "manual"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // после commit откат — no-op

	// Блокировка в порядке id: два встречных слияния не уходят в deadlock.
	rows, err := tx.Query(ctx, `SELECT `+catCols+` FROM budget_category WHERE id = ANY($1) ORDER BY id FOR UPDATE`,
		[]uuid.UUID{req.Source, req.Target})
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge lock: %w", err)
	}
	var src, dst Category
	var found int
	for rows.Next() {
		c, err := scanCat(rows)
		if err != nil {
			rows.Close()
			return MergeResult{}, fmt.Errorf("merge scan: %w", err)
		}
		if c.ID == req.Source {
			src = c
		} else {
			dst = c
		}
		found++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MergeResult{}, fmt.Errorf("merge lock rows: %w", err)
	}
	if found != 2 {
		return MergeResult{}, errors.New("одна из категорий не найдена (уже слита?)")
	}
	if src.Type != dst.Type {
		return MergeResult{}, fmt.Errorf("типы категорий различаются: %s и %s", src.Type, dst.Type)
	}

	mid := uuid.New()
	logRows := func(table, rowExpr, from, where, shareName string) error {
		_, err := tx.Exec(ctx, `INSERT INTO category_merge_log `+mergeLogCols+`
			SELECT $1::text, `+rowExpr+`, $2::uuid, $3::uuid, $4::text, $5::uuid, $6::text, $7::text, $8::text, $9::int, `+shareName+` FROM `+from+` WHERE `+where,
			table, src.ID, dst.ID, src.Name, mid, dst.Name, src.Type, src.Icon, src.SortOrder)
		return err
	}
	// Снимок самой категории: без него откат невозможен, даже если ссылок не было.
	if _, err := tx.Exec(ctx, `INSERT INTO category_merge_log `+mergeLogCols+`
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL)`,
		logCategory, src.ID.String(), src.ID, dst.ID, src.Name, mid, dst.Name, src.Type, src.Icon, src.SortOrder); err != nil {
		return MergeResult{}, fmt.Errorf("merge log category: %w", err)
	}
	for _, t := range []struct{ table string }{{logTx}, {logPlanned}, {logRecurring}} {
		if err := logRows(t.table, "t.id::text", t.table+" t", "t.category_id = $2", "NULL"); err != nil {
			return MergeResult{}, fmt.Errorf("merge log %s: %w", t.table, err)
		}
	}
	// Доли: строка схлопывается, если у той же доли уже есть целевая категория (PK share_id+имя).
	if _, err := tx.Exec(ctx, `INSERT INTO category_merge_log `+mergeLogCols+`
		SELECT CASE WHEN EXISTS (SELECT 1 FROM budget_envelope_share_category o
		                         WHERE o.share_id = t.share_id AND o.category_id = $3)
		            THEN $10::text ELSE $1::text END,
		       t.share_id::text, $2::uuid, $3::uuid, $4::text, $5::uuid, $6::text, $7::text, $8::text, $9::int, t.category_name
		FROM budget_envelope_share_category t WHERE t.category_id = $2`,
		logShare, src.ID, dst.ID, src.Name, mid, dst.Name, src.Type, src.Icon, src.SortOrder, logShareDrop); err != nil {
		return MergeResult{}, fmt.Errorf("merge log shares: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO category_merge_log `+mergeLogCols+`
		SELECT $1::text, a.id::text, $2::uuid, $3::uuid, $4::text, $5::uuid, $6::text, $7::text, $8::text, $9::int, NULL
		FROM budget_category_alias a WHERE a.category_id = $2`,
		logAlias, src.ID, dst.ID, src.Name, mid, dst.Name, src.Type, src.Icon, src.SortOrder); err != nil {
		return MergeResult{}, fmt.Errorf("merge log aliases: %w", err)
	}

	var moved Counts
	for _, u := range []struct {
		table string
		n     *int
	}{{logTx, &moved.Transactions}, {logPlanned, &moved.Planned}, {logRecurring, &moved.Recurring}} {
		tag, err := tx.Exec(ctx, `UPDATE `+u.table+` SET category_id = $1 WHERE category_id = $2`, dst.ID, src.ID)
		if err != nil {
			return MergeResult{}, fmt.Errorf("merge move %s: %w", u.table, err)
		}
		*u.n = int(tag.RowsAffected())
	}
	// Доли: category_id и category_name меняются вместе; дубль по PK удаляется.
	if _, err := tx.Exec(ctx, `DELETE FROM budget_envelope_share_category t WHERE t.category_id = $1
		AND EXISTS (SELECT 1 FROM budget_envelope_share_category o WHERE o.share_id = t.share_id AND o.category_id = $2)`,
		src.ID, dst.ID); err != nil {
		return MergeResult{}, fmt.Errorf("merge drop duplicate shares: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE budget_envelope_share_category SET category_id = $1, category_name = lower($3) WHERE category_id = $2`,
		dst.ID, src.ID, dst.Name)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge move shares: %w", err)
	}
	moved.Shares = int(tag.RowsAffected())

	// Алиасы: цепочек нет — всё, что смотрело на источник, смотрит на цель; плюс новый алиас.
	if _, err := tx.Exec(ctx, `UPDATE budget_category_alias SET category_id = $1 WHERE category_id = $2`, dst.ID, src.ID); err != nil {
		return MergeResult{}, fmt.Errorf("merge redirect aliases: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO budget_category_alias (alias_lower, type, category_id, source)
		VALUES (lower($1), $2, $3, 'merge')
		ON CONFLICT (alias_lower, type) DO UPDATE SET category_id = EXCLUDED.category_id`, src.Name, src.Type, dst.ID); err != nil {
		return MergeResult{}, fmt.Errorf("merge add alias: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO category_decision_log
		(decider, action, source_category_id, target_category_id, source_name, target_name, reason, merge_id)
		VALUES ($1, 'merge', $2, $3, $4, $5, $6, $7)`,
		req.Decider, src.ID, dst.ID, src.Name, dst.Name, req.Reason, mid); err != nil {
		return MergeResult{}, fmt.Errorf("merge decision log: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM budget_category WHERE id = $1`, src.ID); err != nil {
		return MergeResult{}, fmt.Errorf("merge delete source: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MergeResult{}, fmt.Errorf("merge commit: %w", err)
	}
	return MergeResult{MergeID: mid, Moved: moved}, nil
}

// Rollback откатывает слияние по журналу (ADR-010 §4): категория возвращается с прежним id,
// ссылки — на неё, алиасы — на прежнюю цель. Строки, изменённые после слияния, не трогаются.
// Слияния без merge_id (ручной скрипт) откатить нельзя.
func (s *Store) Rollback(ctx context.Context, mergeID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rollback begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // после commit откат — no-op

	type logRow struct {
		table, rowID, shareName  string
		src, dst                 uuid.UUID
		name, dstName, typ, icon string
		sort                     int
	}
	rows, err := tx.Query(ctx, `SELECT table_name, row_id, old_category_id, new_category_id, old_category_name,
		COALESCE(new_category_name, ''), COALESCE(source_category_type, ''), COALESCE(source_category_icon, ''),
		COALESCE(source_category_sort, 0), COALESCE(old_share_category_name, '')
		FROM category_merge_log WHERE merge_id = $1 AND rolled_back_at IS NULL`, mergeID)
	if err != nil {
		return fmt.Errorf("rollback read log: %w", err)
	}
	var log []logRow
	for rows.Next() {
		var r logRow
		if err := rows.Scan(&r.table, &r.rowID, &r.src, &r.dst, &r.name, &r.dstName, &r.typ, &r.icon, &r.sort, &r.shareName); err != nil {
			rows.Close()
			return fmt.Errorf("rollback scan: %w", err)
		}
		log = append(log, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rollback log rows: %w", err)
	}
	if len(log) == 0 {
		return errors.New("слияние не найдено в журнале или уже откатано")
	}
	var snap *logRow
	for i := range log {
		if log[i].table == logCategory {
			snap = &log[i]
		}
	}
	if snap == nil {
		return errors.New("в журнале нет снимка категории: откат невозможен")
	}
	var dstExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM budget_category WHERE id = $1)`, snap.dst).Scan(&dstExists); err != nil {
		return fmt.Errorf("rollback check target: %w", err)
	}
	if !dstExists {
		return errors.New("целевая категория удалена: откат невозможен")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO budget_category (id, name, type, icon, sort_order) VALUES ($1, $2, $3, $4, $5)`,
		snap.src, snap.name, snap.typ, snap.icon, snap.sort); err != nil {
		return fmt.Errorf("rollback restore category (имя уже занято?): %w", err)
	}
	// Алиас слияния убираем, чтобы имя снова принадлежало самой категории.
	if _, err := tx.Exec(ctx, `DELETE FROM budget_category_alias WHERE alias_lower = lower($1) AND type = $2 AND category_id = $3`,
		snap.name, snap.typ, snap.dst); err != nil {
		return fmt.Errorf("rollback drop alias: %w", err)
	}
	for _, r := range log {
		switch r.table {
		case logTx, logPlanned, logRecurring:
			if _, err := tx.Exec(ctx, `UPDATE `+r.table+` SET category_id = $1 WHERE id = $2::uuid AND category_id = $3`, r.src, r.rowID, r.dst); err != nil {
				return fmt.Errorf("rollback %s: %w", r.table, err)
			}
		case logShare:
			if _, err := tx.Exec(ctx, `UPDATE budget_envelope_share_category SET category_id = $1, category_name = $2
				WHERE share_id = $3::uuid AND category_id = $4 AND category_name = lower($5)`, r.src, r.shareName, r.rowID, r.dst, r.dstName); err != nil {
				return fmt.Errorf("rollback shares: %w", err)
			}
		case logShareDrop:
			if _, err := tx.Exec(ctx, `INSERT INTO budget_envelope_share_category (share_id, category_id, category_name)
				VALUES ($1::uuid, $2, $3) ON CONFLICT DO NOTHING`, r.rowID, r.src, r.shareName); err != nil {
				return fmt.Errorf("rollback dropped share: %w", err)
			}
		case logAlias:
			if _, err := tx.Exec(ctx, `UPDATE budget_category_alias SET category_id = $1 WHERE id = $2::uuid AND category_id = $3`, r.src, r.rowID, r.dst); err != nil {
				return fmt.Errorf("rollback alias: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE category_merge_log SET rolled_back_at = now() WHERE merge_id = $1`, mergeID); err != nil {
		return fmt.Errorf("rollback mark: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO category_decision_log
		(decider, action, source_category_id, target_category_id, source_name, target_name, reason, merge_id)
		VALUES ('manual', 'rollback', $1, $2, $3, $4, 'откат слияния', $5)`, snap.src, snap.dst, snap.name, snap.dstName, mergeID); err != nil {
		return fmt.Errorf("rollback decision log: %w", err)
	}
	return tx.Commit(ctx)
}
