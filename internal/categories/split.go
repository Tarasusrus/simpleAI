package categories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Разнесение истории легаси-категории («Еда» → «Продукты» | «Рестораны», ADR-013 §4, этап 2).
//
// Три шага, каждый отдельно:
//  1. PlanSplit — ничего не пишет: по строке («транзакция», «плановый», «повтор») выбирает цель; классификатору
//     уходят только УНИКАЛЬНЫЕ описания и цели из split-таблицы (без сумм, дат и chat_id).
//  2. ApplySplit — одна транзакция БД: строки переезжают на цели, каждая в журнале category_merge_log с
//     split_id (merge_id) и прежней категорией. Идемпотентно: переносятся только строки, всё ещё лежащие в источнике.
//  3. RollbackSplit — возвращает строки по журналу, если после разнесения их категорию не меняли.
// Сама категория-источник не удаляется: её убирает RetireSource после проверки владельцем.

// Таблицы, строки которых разносятся, и колонка с текстом для классификатора.
var splitTables = []struct{ table, descCol string }{
	{logTx, "description"},
	{logPlanned, "description"},
	{logRecurring, "name"},
}

// Основание выбора цели строки.
const (
	BasisClassified = "classified"        // классификатор выбрал цель с достаточной уверенностью
	BasisNoDesc     = "default:no-desc"   // описания нет — цель по умолчанию
	BasisAmbiguous  = "default:ambiguous" // классификатор не определил или не уверен — цель по умолчанию, на просмотр
	BasisError      = "default:error"     // классификатор недоступен — цель по умолчанию, на просмотр
)

// SplitRow — строка источника.
type SplitRow struct {
	Table       string
	ID          uuid.UUID
	Description string
	Date        time.Time // дата транзакции / срок планового платежа; нулевая, если её нет
	Amount      float64
	Currency    string
}

// RowChoice — решение по строке.
type RowChoice struct {
	Row        SplitRow
	Target     Category
	Basis      string
	Confidence float64
}

// DescriptionChoice — решение по уникальному описанию (для таблицы владельцу).
type DescriptionChoice struct {
	Description string
	Rows        int
	Target      Category
	Basis       string
	Confidence  float64
	Reason      string
}

// SplitPlan — результат PlanSplit.
type SplitPlan struct {
	Source  Category
	Targets []SplitTarget
	Default SplitTarget
	Rows    []RowChoice
	Choices []DescriptionChoice
}

// ErrNotSplitSource — у категории нет строк в split-таблице.
var ErrNotSplitSource = errors.New("categories: категория не является split-источником")

// minSplitConfidence — как у Chooser: ниже — «не определил».
const minSplitConfidence = minConfidence

// SplitSource находит источник по имени и его цели из split-таблицы.
func (s *Store) SplitSource(ctx context.Context, name string) (Category, []SplitTarget, error) {
	src, found, err := s.Resolve(ctx, name, "expense")
	if err != nil {
		return Category{}, nil, err
	}
	if !found {
		return Category{}, nil, fmt.Errorf("категория %q не найдена", name)
	}
	targets, err := s.SplitTargets(ctx, src.ID)
	if err != nil {
		return Category{}, nil, err
	}
	if len(targets) == 0 {
		return Category{}, nil, fmt.Errorf("%w: %s", ErrNotSplitSource, src.Name)
	}
	return src, targets, nil
}

// SplitRows читает строки источника во всех трёх таблицах.
func (s *Store) SplitRows(ctx context.Context, src uuid.UUID) ([]SplitRow, error) {
	var out []SplitRow
	for _, t := range splitTables {
		var q string
		switch t.table {
		case logTx:
			q = `SELECT id, COALESCE(description, ''), transaction_date, amount::float8, currency FROM budget_transaction WHERE category_id = $1 ORDER BY transaction_date, id`
		case logPlanned:
			q = `SELECT id, COALESCE(description, ''), due_date, COALESCE(amount, 0)::float8, currency FROM budget_planned_expense WHERE category_id = $1 ORDER BY id`
		default:
			q = `SELECT id, COALESCE(name, ''), next_date, amount::float8, currency FROM budget_recurring WHERE category_id = $1 ORDER BY id`
		}
		rows, err := s.pool.Query(ctx, q, src)
		if err != nil {
			return nil, fmt.Errorf("split rows %s: %w", t.table, err)
		}
		for rows.Next() {
			r := SplitRow{Table: t.table}
			var d *time.Time
			if err := rows.Scan(&r.ID, &r.Description, &d, &r.Amount, &r.Currency); err != nil {
				rows.Close()
				return nil, fmt.Errorf("split rows %s scan: %w", t.table, err)
			}
			if d != nil {
				r.Date = *d
			}
			out = append(out, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("split rows %s: %w", t.table, err)
		}
	}
	return out, nil
}

// descKey — ключ уникального описания: регистр и лишние пробелы не различают описания.
func descKey(s string) string { return strings.ToLower(Clean(s)) }

// PlanSplit выбирает цель для каждой строки. Классификатор зовётся один раз на уникальное описание и видит ТОЛЬКО цели
// из split-таблицы (создавать новые категории нельзя). Нет описания → цель по умолчанию; нет уверенности, ошибка,
// «не определил» → цель по умолчанию с пометкой на просмотр.
func PlanSplit(ctx context.Context, src Category, targets []SplitTarget, rows []SplitRow, clf Classifier, hints map[string]string) (SplitPlan, error) {
	p := SplitPlan{Source: src, Targets: targets}
	for _, t := range targets {
		if t.IsDefault {
			p.Default = t
		}
	}
	if p.Default.ID == uuid.Nil {
		return SplitPlan{}, errors.New("у split-источника нет цели по умолчанию")
	}
	allowed := make(map[uuid.UUID]Category, len(targets))
	cands := make([]Candidate, 0, len(targets))
	for _, t := range targets {
		allowed[t.ID] = t.Category
		cands = append(cands, Candidate{ID: t.ID, Name: t.Name, Hint: hints[strings.ToLower(t.Name)]})
	}

	byDesc := map[string]*DescriptionChoice{}
	var order []string
	classify := func(desc string) *DescriptionChoice {
		key := descKey(desc)
		if c, ok := byDesc[key]; ok {
			return c
		}
		c := &DescriptionChoice{Description: Clean(desc), Target: p.Default.Category, Basis: BasisAmbiguous}
		byDesc[key] = c
		order = append(order, key)
		if clf == nil {
			c.Basis, c.Reason = BasisError, "классификатор не подключён"
			return c
		}
		res, err := clf.Classify(ctx, c.Description, cands)
		switch {
		case err != nil:
			c.Basis, c.Reason = BasisError, err.Error()
		case res.Action == ActionExisting && res.Confidence >= minSplitConfidence && allowed[res.CategoryID].ID != uuid.Nil:
			c.Target, c.Basis, c.Confidence, c.Reason = allowed[res.CategoryID], BasisClassified, res.Confidence, res.Reason
		default:
			c.Confidence, c.Reason = res.Confidence, res.Reason
		}
		return c
	}

	noDesc := &DescriptionChoice{Description: "", Target: p.Default.Category, Basis: BasisNoDesc}
	for _, r := range rows {
		var c *DescriptionChoice
		if Clean(r.Description) == "" {
			c = noDesc
		} else {
			c = classify(r.Description)
		}
		c.Rows++
		p.Rows = append(p.Rows, RowChoice{Row: r, Target: c.Target, Basis: c.Basis, Confidence: c.Confidence})
	}
	for _, k := range order {
		p.Choices = append(p.Choices, *byDesc[k])
	}
	sort.SliceStable(p.Choices, func(i, j int) bool { return p.Choices[i].Rows > p.Choices[j].Rows })
	if noDesc.Rows > 0 {
		p.Choices = append(p.Choices, *noDesc)
	}
	return p, nil
}

// SplitResult — итог ApplySplit.
type SplitResult struct {
	SplitID uuid.UUID
	Moved   map[string]int // таблица → сколько строк переехало
}

// ApplySplit переносит строки одной транзакцией БД и пишет каждую в журнал с split_id. Переносятся только строки, которые
// всё ещё лежат в источнике (повторный запуск и строки, изменённые после плана, не трогаются).
func (s *Store) ApplySplit(ctx context.Context, p SplitPlan) (SplitResult, error) {
	res := SplitResult{SplitID: uuid.New(), Moved: map[string]int{}}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("split begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // после commit откат — no-op

	for _, rc := range p.Rows {
		tag, err := tx.Exec(ctx, `UPDATE `+rc.Row.Table+` SET category_id = $1 WHERE id = $2 AND category_id = $3`,
			rc.Target.ID, rc.Row.ID, p.Source.ID)
		if err != nil {
			return SplitResult{}, fmt.Errorf("split move %s %s: %w", rc.Row.Table, rc.Row.ID, err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO category_merge_log `+mergeLogCols+`
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL)`,
			rc.Row.Table, rc.Row.ID.String(), p.Source.ID, rc.Target.ID, p.Source.Name, res.SplitID, rc.Target.Name,
			p.Source.Type, p.Source.Icon, p.Source.SortOrder); err != nil {
			return SplitResult{}, fmt.Errorf("split log %s %s: %w", rc.Row.Table, rc.Row.ID, err)
		}
		res.Moved[rc.Row.Table]++
	}
	if err := tx.Commit(ctx); err != nil {
		return SplitResult{}, fmt.Errorf("split commit: %w", err)
	}
	return res, nil
}

// RollbackSplit возвращает строки разнесения в источник по журналу. Строка, чью категорию изменили после разнесения,
// не трогается. Откатить можно только записи с таблицами строк (не слияние: у того есть снимок категории).
func (s *Store) RollbackSplit(ctx context.Context, splitID uuid.UUID) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("split rollback begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // после commit откат — no-op

	rows, err := tx.Query(ctx, `SELECT table_name, row_id, old_category_id, new_category_id FROM category_merge_log
		WHERE merge_id = $1 AND rolled_back_at IS NULL`, splitID)
	if err != nil {
		return 0, fmt.Errorf("split rollback read: %w", err)
	}
	type logRow struct {
		table, id string
		old, new  uuid.UUID
	}
	var log []logRow
	for rows.Next() {
		var r logRow
		if err := rows.Scan(&r.table, &r.id, &r.old, &r.new); err != nil {
			rows.Close()
			return 0, fmt.Errorf("split rollback scan: %w", err)
		}
		log = append(log, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(log) == 0 {
		return 0, errors.New("разнесение не найдено в журнале или уже откатано")
	}
	back := 0
	for _, r := range log {
		switch r.table {
		case logTx, logPlanned, logRecurring:
		default:
			return 0, fmt.Errorf("в журнале %q: это не разнесение, а слияние категорий (откат — categories --rollback)", r.table)
		}
		tag, err := tx.Exec(ctx, `UPDATE `+r.table+` SET category_id = $1 WHERE id = $2::uuid AND category_id = $3`, r.old, r.id, r.new)
		if err != nil {
			return 0, fmt.Errorf("split rollback %s: %w", r.table, err)
		}
		back += int(tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `UPDATE category_merge_log SET rolled_back_at = now() WHERE merge_id = $1`, splitID); err != nil {
		return 0, fmt.Errorf("split rollback mark: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("split rollback commit: %w", err)
	}
	return back, nil
}

// RetireSource убирает опустевший источник: сливает его в цель по умолчанию обычным Merge (доли, алиасы, лимиты,
// алиас «еда» → «Продукты», журнал и откат — как у любого слияния). Отказывает, пока в источнике есть строки.
func (s *Store) RetireSource(ctx context.Context, src Category, target Category) (MergeResult, error) {
	c, err := s.Usage(ctx, src.ID)
	if err != nil {
		return MergeResult{}, err
	}
	if c.Transactions+c.Planned+c.Recurring > 0 {
		return MergeResult{}, fmt.Errorf("в %q ещё есть строки (транзакций %d, плановых %d, повторов %d): сначала разнести",
			src.Name, c.Transactions, c.Planned, c.Recurring)
	}
	return s.Merge(ctx, MergeRequest{Source: src.ID, Target: target.ID, Decider: "manual", Reason: "этап 2 ADR-013: история разнесена, «Еда» убрана"})
}

// planFile — то, что dry-run сохраняет на диск и принимает --apply: только id строк и цели (без описаний). Владелец
// утверждает таблицу, а применяется именно сохранённый план: повторный вызов классификатора мог бы ответить иначе.
type planFile struct {
	SourceID   uuid.UUID `json:"source_id"`
	SourceName string    `json:"source_name"`
	CreatedAt  time.Time `json:"created_at"`
	Rows       []struct {
		Table      string    `json:"table"`
		ID         uuid.UUID `json:"id"`
		TargetID   uuid.UUID `json:"target_id"`
		TargetName string    `json:"target_name"`
	} `json:"rows"`
}

// Marshal сохраняет план для --apply.
func (p SplitPlan) Marshal(now time.Time) ([]byte, error) {
	f := planFile{SourceID: p.Source.ID, SourceName: p.Source.Name, CreatedAt: now.UTC()}
	for _, r := range p.Rows {
		f.Rows = append(f.Rows, struct {
			Table      string    `json:"table"`
			ID         uuid.UUID `json:"id"`
			TargetID   uuid.UUID `json:"target_id"`
			TargetName string    `json:"target_name"`
		}{r.Row.Table, r.Row.ID, r.Target.ID, r.Target.Name})
	}
	return json.MarshalIndent(f, "", "  ")
}

// LoadSplitPlan читает сохранённый план и проверяет его по базе: источник — тот же split-источник, каждая цель — из
// его split-таблицы, таблицы строк известны. Чужой или устаревший файл отвергается.
func (s *Store) LoadSplitPlan(ctx context.Context, data []byte, sourceName string) (SplitPlan, error) {
	var f planFile
	if err := json.Unmarshal(data, &f); err != nil {
		return SplitPlan{}, fmt.Errorf("план разнесения: %w", err)
	}
	src, targets, err := s.SplitSource(ctx, sourceName)
	if err != nil {
		return SplitPlan{}, err
	}
	if f.SourceID != src.ID {
		return SplitPlan{}, fmt.Errorf("план составлен для другой категории (%s), а не для %q", f.SourceName, src.Name)
	}
	byID := map[uuid.UUID]SplitTarget{}
	p := SplitPlan{Source: src, Targets: targets}
	for _, t := range targets {
		byID[t.ID] = t
		if t.IsDefault {
			p.Default = t
		}
	}
	for _, r := range f.Rows {
		t, ok := byID[r.TargetID]
		if !ok {
			return SplitPlan{}, fmt.Errorf("в плане цель %q (%s) не из split-таблицы источника", r.TargetName, r.TargetID)
		}
		switch r.Table {
		case logTx, logPlanned, logRecurring:
		default:
			return SplitPlan{}, fmt.Errorf("в плане неизвестная таблица %q", r.Table)
		}
		p.Rows = append(p.Rows, RowChoice{Row: SplitRow{Table: r.Table, ID: r.ID}, Target: t.Category, Basis: "saved"})
	}
	return p, nil
}
