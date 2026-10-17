package categories

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func cat(name, typ string) Category { return Category{ID: uuid.New(), Name: name, Type: typ} }

type fakeSource []Category

func (f fakeSource) List(context.Context) ([]Category, error) { return f, nil }

type fakeMerger struct{ calls []MergeRequest }

func (f *fakeMerger) Merge(_ context.Context, r MergeRequest) (MergeResult, error) {
	f.calls = append(f.calls, r)
	return MergeResult{MergeID: uuid.New()}, nil
}

func TestSynonymFinder_PairsChainsAndGuards(t *testing.T) {
	food, products, cafe := cat("Еда", "expense"), cat("Продукты", "expense"), cat("кафе / ресторан", "expense")
	clothes := cat("clothes", "expense") // цели «Одежда» нет → пары нет
	incomeFood := cat("продукты", "income")
	f := SynonymFinder{Synonyms: map[string]string{"продукты": "Еда", "кафе / ресторан": "продукты", "clothes": "Одежда"}}
	pairs, err := f.Find(context.Background(), []Category{food, products, cafe, clothes, incomeFood})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range pairs {
		got[p.Source.Name] = p.Target.Name
	}
	if len(got) != 2 || got["Продукты"] != "Еда" || got["кафе / ресторан"] != "Еда" {
		t.Fatalf("пары: %v; ждали Продукты→Еда и кафе→Еда (цепочка схлопнута), без clothes и без доходов", got)
	}
}

func TestDeduplicator_DryRunWritesNothing_ApplyMerges(t *testing.T) {
	food, products := cat("Еда", "expense"), cat("продукты", "expense")
	m := &fakeMerger{}
	d := &Deduplicator{
		Source: fakeSource{food, products}, Finder: SynonymFinder{Synonyms: map[string]string{"продукты": "Еда"}},
		Decider: RuleDecider{}, Merger: m,
	}
	rep, err := d.Run(context.Background(), false)
	if err != nil || len(rep.Items) != 1 || len(m.calls) != 0 {
		t.Fatalf("dry-run: items=%d merges=%d err=%v", len(rep.Items), len(m.calls), err)
	}
	if _, err := d.Run(context.Background(), true); err != nil || len(m.calls) != 1 {
		t.Fatalf("apply: merges=%d err=%v", len(m.calls), err)
	}
	if m.calls[0].Source != products.ID || m.calls[0].Target != food.ID || m.calls[0].Decider != "rule" {
		t.Errorf("запрос слияния: %+v", m.calls[0])
	}
}
