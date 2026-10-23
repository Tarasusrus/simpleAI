package categories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store — реализация Source, Merger, Usage и Resolver над Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore создаёт Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const catCols = `id, name, type, icon, sort_order`

func scanCat(row pgx.Row) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Icon, &c.SortOrder)
	return c, err
}

// List — Source: все категории.
func (s *Store) List(ctx context.Context) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+catCols+` FROM budget_category ORDER BY type, sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		c, err := scanCat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// find ищет живую категорию по имени, затем по алиасу. Регистр сравнивает БД.
func find(ctx context.Context, q querier, name, typ string) (Category, bool, error) {
	c, err := scanCat(q.QueryRow(ctx,
		`SELECT `+catCols+` FROM budget_category WHERE lower(name) = lower($1) AND type = $2`, name, typ))
	if err == nil {
		return c, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Category{}, false, fmt.Errorf("find category %q: %w", name, err)
	}
	c, err = scanCat(q.QueryRow(ctx, `
		SELECT c.id, c.name, c.type, c.icon, c.sort_order
		FROM budget_category_alias a JOIN budget_category c ON c.id = a.category_id
		WHERE a.alias_lower = lower($1) AND a.type = $2`, name, typ))
	if err == nil {
		return c, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Category{}, false, fmt.Errorf("find alias %q: %w", name, err)
	}
	return Category{}, false, nil
}

// checkInput чистит имя и проверяет тип: общий вход Resolve и GetOrCreate.
func checkInput(name, typ string) (string, error) {
	name = Clean(name)
	if name == "" {
		return "", errors.New("пустое имя категории")
	}
	if typ != "income" && typ != "expense" {
		return "", fmt.Errorf("неизвестный тип категории %q", typ)
	}
	return name, nil
}

// Resolve находит категорию по имени или алиасу и ничего не создаёт (ADR-012 §1).
// found=false без ошибки означает «такой категории нет»: вызывающий сам решает, что делать
// (веб-план отвечает ошибкой вместо тихого NULL).
func (s *Store) Resolve(ctx context.Context, name, typ string) (Category, bool, error) {
	name, err := checkInput(name, typ)
	if err != nil {
		return Category{}, false, err
	}
	return find(ctx, s.pool, name, typ)
}

// GetOrCreate — единая точка создания категории (ADR-010 §2). Имя-алиас ведёт на целевую
// категорию, «Кофе» и «кофе» — одна категория; параллельные вызовы создают ровно одну.
func (s *Store) GetOrCreate(ctx context.Context, name, typ string) (Category, bool, error) {
	name, err := checkInput(name, typ)
	if err != nil {
		return Category{}, false, err
	}
	if c, ok, err := find(ctx, s.pool, name, typ); err != nil || ok {
		return c, false, err
	}
	c, err := scanCat(s.pool.QueryRow(ctx, `
		INSERT INTO budget_category (id, name, type, icon, sort_order)
		VALUES ($1, $2, $3, '', 100)
		ON CONFLICT DO NOTHING
		RETURNING `+catCols, uuid.New(), name, typ))
	if err == nil {
		return c, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Category{}, false, fmt.Errorf("add category: %w", err)
	}
	// Проиграли гонку: категорию уже создал параллельный вызов.
	c, ok, err := find(ctx, s.pool, name, typ)
	if err != nil {
		return Category{}, false, err
	}
	if !ok {
		return Category{}, false, fmt.Errorf("категория %q не найдена после конфликта вставки", name)
	}
	return c, false, nil
}

// SplitTarget — одна из целей разделения легаси-категории (ADR-013 §2).
type SplitTarget struct {
	Category
	IsDefault bool
}

// SplitTargets возвращает цели, на которые разделена категория; пусто — это не split-источник.
// Цель по умолчанию идёт первой.
func (s *Store) SplitTargets(ctx context.Context, source uuid.UUID) ([]SplitTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.type, c.icon, c.sort_order, t.is_default
		FROM category_split_target t JOIN budget_category c ON c.id = t.target_id
		WHERE t.source_id = $1
		ORDER BY t.is_default DESC, c.sort_order, c.name`, source)
	if err != nil {
		return nil, fmt.Errorf("split targets: %w", err)
	}
	defer rows.Close()
	var out []SplitTarget
	for rows.Next() {
		var x SplitTarget
		if err := rows.Scan(&x.ID, &x.Name, &x.Type, &x.Icon, &x.SortOrder, &x.IsDefault); err != nil {
			return nil, fmt.Errorf("scan split target: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ListAssignable — категории типа typ, в которые можно записать новую трату: без split-источников
// (легаси «Еда» не назначается напрямую, ADR-013 §3).
func (s *Store) ListAssignable(ctx context.Context, typ string) ([]Category, error) {
	if typ != "income" && typ != "expense" {
		return nil, fmt.Errorf("неизвестный тип категории %q", typ)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+catCols+` FROM budget_category c
		WHERE c.type = $1 AND NOT EXISTS (SELECT 1 FROM category_split_target t WHERE t.source_id = c.id)
		ORDER BY sort_order, name`, typ)
	if err != nil {
		return nil, fmt.Errorf("list assignable: %w", err)
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		c, err := scanCat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MostUsedCategory — самая частая категория среди прошлых трат с таким же описанием (без учёта
// регистра и краёв). Не отдаёт split-источник («Еда» смешивает цели) и «Прочее» (результат
// деградации не должен закрепляться за описанием навсегда).
func (s *Store) MostUsedCategory(ctx context.Context, typ, description string) (Category, bool, error) {
	key := strings.ToLower(Clean(description))
	if key == "" {
		return Category{}, false, nil
	}
	c, err := scanCat(s.pool.QueryRow(ctx, `
		SELECT c.id, c.name, c.type, c.icon, c.sort_order
		FROM budget_transaction t JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = $1 AND lower(btrim(t.description)) = $2
		  AND lower(c.name) <> 'прочее'
		  AND NOT EXISTS (SELECT 1 FROM category_split_target s WHERE s.source_id = c.id)
		GROUP BY c.id ORDER BY count(*) DESC, c.name LIMIT 1`, typ, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, false, nil
	}
	if err != nil {
		return Category{}, false, fmt.Errorf("most used category: %w", err)
	}
	return c, true, nil
}

// AddProposal кладёт в очередь разбора то, что бот не смог определить (kind='proposal').
// transactionID — запись, из-за которой появилось предложение.
func (s *Store) AddProposal(ctx context.Context, p Proposal, transactionID uuid.UUID) error {
	name := Clean(p.Name)
	if name == "" {
		return errors.New("пустое имя предложения")
	}
	payload := map[string]any{"description": p.Description, "reason": p.Reason, "transaction_id": transactionID.String()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("proposal payload: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO category_review_queue (type, name, name_lower, kind, payload)
		VALUES ($1, $2, lower($2), 'proposal', $3)`, p.Type, name, raw); err != nil {
		return fmt.Errorf("add proposal: %w", err)
	}
	return nil
}

// Usage считает ссылки на категорию.
func (s *Store) Usage(ctx context.Context, id uuid.UUID) (Counts, error) {
	var c Counts
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM budget_transaction WHERE category_id = $1),
		(SELECT count(*) FROM budget_planned_expense WHERE category_id = $1),
		(SELECT count(*) FROM budget_recurring WHERE category_id = $1),
		(SELECT count(*) FROM budget_envelope_share_category WHERE category_id = $1),
		(SELECT count(*) FROM plan_category_limit WHERE category_id = $1)`, id).
		Scan(&c.Transactions, &c.Planned, &c.Recurring, &c.Shares, &c.Limits)
	if err != nil {
		return c, fmt.Errorf("usage: %w", err)
	}
	return c, nil
}

// ReconcileShareNames чинит устаревшие category_name долей (ADR-010 §4): после ручного слияния
// category_id указывает на целевую категорию, а имя осталось старым. Идемпотентно.
func (s *Store) ReconcileShareNames(ctx context.Context) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("reconcile begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // после commit откат — no-op
	// Сначала убираем строки, которые после переименования столкнулись бы по PK с уже существующими.
	if _, err := tx.Exec(ctx, `
		DELETE FROM budget_envelope_share_category s USING budget_category c
		WHERE c.id = s.category_id AND s.category_name <> lower(c.name)
		  AND EXISTS (SELECT 1 FROM budget_envelope_share_category o
		              WHERE o.share_id = s.share_id AND o.category_name = lower(c.name))`); err != nil {
		return 0, fmt.Errorf("reconcile drop: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE budget_envelope_share_category s SET category_name = lower(c.name)
		FROM budget_category c WHERE c.id = s.category_id AND s.category_name <> lower(c.name)`)
	if err != nil {
		return 0, fmt.Errorf("reconcile update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("reconcile commit: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
