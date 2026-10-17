package categories

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// DefaultSynonyms — стартовая карта «имя (нижний регистр) → каноничная категория расходов».
// Это правила, а не код слияния: заменяются конфигом или другой стратегией SimilarityFinder.
// Слияние по карте происходит, только если целевая категория существует.
var DefaultSynonyms = map[string]string{
	"clothes": "Одежда", "health": "Здоровье", "гигиена / личный уход": "Красота",
	"продукт": "Еда", "продукты": "Еда", "ресторан": "Еда", "кафе / ресторан": "Еда",
	"корм": "Прочее", "цветы": "Прочее", "сигареты": "Прочее",
	"водоснабжение": "Жильё", "коммунальные услуги": "Жильё", "дом": "Жильё", "дом / бытовая техника": "Жильё",
	"дом / хранение": "Жильё", "товары для дома": "Жильё", "хоз расходы": "Жильё", "посуда": "Жильё",
	"инструменты": "Жильё", "ремонт": "Жильё", "стирка": "Жильё",
	"связь": "Подписки", "сотовая связь": "Подписки", "кредит": "Переводы",
}

// SynonymFinder — SimilarityFinder по карте синонимов (только расходы).
type SynonymFinder struct{ Synonyms map[string]string }

func key(s string) string { return strings.ToLower(Clean(s)) }

// Find строит пары источник→цель. Цепочки (A→B, B→C) схлопываются до конечной цели.
func (f SynonymFinder) Find(_ context.Context, cats []Category) ([]Pair, error) {
	byKey := map[string]Category{}
	for _, c := range cats {
		if c.Type == "expense" {
			byKey[key(c.Name)] = c
		}
	}
	var out []Pair
	for _, c := range cats {
		if c.Type != "expense" {
			continue
		}
		want, ok := f.Synonyms[key(c.Name)]
		if !ok {
			continue
		}
		target, ok := byKey[key(want)]
		for depth := 0; ok && depth < 5; depth++ {
			next, more := f.Synonyms[key(target.Name)]
			nt, exists := byKey[key(next)]
			if !more || !exists || nt.ID == target.ID {
				break
			}
			target = nt
		}
		if !ok || target.ID == c.ID {
			continue
		}
		out = append(out, Pair{Source: c, Target: target, Reason: fmt.Sprintf("синоним: «%s» → «%s»", c.Name, target.Name)})
	}
	return out, nil
}

// RuleDecider сливает всё, что нашёл правиловый поиск. Сомнительное он не предлагает вовсе.
type RuleDecider struct{}

func (RuleDecider) Decide(_ context.Context, p Pair) (Decision, error) {
	return Decision{Action: ActionMerge, Decider: "rule", Reason: p.Reason}, nil
}

// Reconciler чинит устаревшие имена долей перед первым слиянием.
type Reconciler interface {
	ReconcileShareNames(ctx context.Context) (int, error)
}

// Item — строка отчёта по одной паре.
type Item struct {
	Pair     Pair
	Decision Decision
	Counts   Counts
	Result   *MergeResult
	Err      error
}

// Report — итог прогона.
type Report struct {
	Applied    bool
	Items      []Item
	Reconciled int
}

// Deduplicator оркестрирует: Source → Finder → Decider → Merger → Notifier.
type Deduplicator struct {
	Source     Source
	Finder     SimilarityFinder
	Decider    Decider
	Merger     Merger
	Usage      Usage
	Reconciler Reconciler
	Notifier   Notifier
}

// Run строит план; при apply выполняет его. Повторный прогон ничего не меняет:
// источники слиты и удалены, пар больше нет.
func (d *Deduplicator) Run(ctx context.Context, apply bool) (Report, error) {
	rep := Report{Applied: apply}
	if apply && d.Reconciler != nil {
		n, err := d.Reconciler.ReconcileShareNames(ctx)
		if err != nil {
			return rep, fmt.Errorf("reconcile: %w", err)
		}
		rep.Reconciled = n
	}
	cats, err := d.Source.List(ctx)
	if err != nil {
		return rep, err
	}
	pairs, err := d.Finder.Find(ctx, cats)
	if err != nil {
		return rep, fmt.Errorf("find: %w", err)
	}
	notifier := d.Notifier
	if notifier == nil {
		notifier = NopNotifier{}
	}
	merged := map[uuid.UUID]bool{}
	for _, p := range pairs {
		it := Item{Pair: p}
		dec, err := d.Decider.Decide(ctx, p)
		if err != nil {
			it.Err = fmt.Errorf("decide: %w", err)
			rep.Items = append(rep.Items, it)
			continue
		}
		it.Decision = dec
		if d.Usage != nil {
			if it.Counts, err = d.Usage.Usage(ctx, p.Source.ID); err != nil {
				it.Err = err
				rep.Items = append(rep.Items, it)
				continue
			}
		}
		if apply && dec.Action == ActionMerge && !merged[p.Source.ID] {
			res, err := d.Merger.Merge(ctx, MergeRequest{Source: p.Source.ID, Target: p.Target.ID, Decider: dec.Decider, Reason: dec.Reason})
			if err != nil {
				it.Err = err
			} else {
				it.Result = &res
				merged[p.Source.ID] = true
				// После commit; сбой оповещения слияние не откатывает.
				_ = notifier.Notify(ctx, Event{Kind: "merged", Source: p.Source.Name, Target: p.Target.Name, Reason: dec.Reason}) //nolint:errcheck // ADR-010 §5
			}
		}
		rep.Items = append(rep.Items, it)
	}
	return rep, nil
}
