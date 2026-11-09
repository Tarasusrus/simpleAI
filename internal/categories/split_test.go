package categories

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type splitSpy struct {
	calls []string
	cands [][]Candidate
	reply func(desc string, c []Candidate) (Classification, error)
}

func (s *splitSpy) Classify(_ context.Context, desc string, c []Candidate) (Classification, error) {
	s.calls = append(s.calls, desc)
	s.cands = append(s.cands, c)
	return s.reply(desc, c)
}

func splitFixture() (Category, []SplitTarget, Category, Category) {
	src := Category{ID: uuid.New(), Name: "Еда", Type: "expense"}
	prod := Category{ID: uuid.New(), Name: "Продукты", Type: "expense"}
	rest := Category{ID: uuid.New(), Name: "Рестораны", Type: "expense"}
	return src, []SplitTarget{{Category: prod, IsDefault: true}, {Category: rest}}, prod, rest
}

func rowsOf(descs ...string) []SplitRow {
	out := make([]SplitRow, 0, len(descs))
	for _, d := range descs {
		out = append(out, SplitRow{Table: logTx, ID: uuid.New(), Description: d})
	}
	return out
}

func pick(c Category, conf float64) Classification {
	return Classification{Action: ActionExisting, CategoryID: c.ID, Name: c.Name, Confidence: conf, Reason: "тест"}
}

// Классификатор зовётся один раз на уникальное описание (регистр и пробелы не различают), видит только цели
// split-таблицы с подсказками; без описания — цель по умолчанию, классификатор не зовётся.
//
// Мутации: звать классификатор на каждую строку; отдавать ему все категории вместо целей; не склеивать описания
// по регистру; звать для пустых описаний — тест краснеет.
func TestPlanSplit_UniqueDescriptionsAndCandidates(t *testing.T) {
	src, targets, prod, rest := splitFixture()
	spy := &splitSpy{reply: func(d string, _ []Candidate) (Classification, error) {
		if d == "Cafe Amazon" {
			return pick(rest, 0.9), nil
		}
		return pick(prod, 0.8), nil
	}}
	hints := map[string]string{"продукты": "магазины", "рестораны": "кафе"}
	p, err := PlanSplit(context.Background(), src, targets, rowsOf("Cafe Amazon", "cafe amazon ", "Makro", "", "  "), spy, hints)
	if err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 2 {
		t.Fatalf("вызовов классификатора %d (%v), ждали 2 уникальных описания", len(spy.calls), spy.calls)
	}
	for _, cands := range spy.cands {
		if len(cands) != 2 || cands[0].ID != prod.ID || cands[1].ID != rest.ID || cands[0].Hint != "магазины" || cands[1].Hint != "кафе" {
			t.Fatalf("кандидаты %+v, ждали ровно цели split-таблицы с подсказками", cands)
		}
	}
	got := map[string]string{}
	for _, r := range p.Rows {
		got[r.Row.Description] = r.Target.Name + "/" + r.Basis
	}
	want := map[string]string{
		"Cafe Amazon": "Рестораны/classified", "cafe amazon ": "Рестораны/classified", "Makro": "Продукты/classified",
		"": "Продукты/default:no-desc", "  ": "Продукты/default:no-desc",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q → %q, ждали %q", k, got[k], v)
		}
	}
	// таблица владельцу: уникальные описания по убыванию строк, «без описания» в конце
	if len(p.Choices) != 3 || p.Choices[0].Rows != 2 || p.Choices[len(p.Choices)-1].Basis != BasisNoDesc || p.Choices[len(p.Choices)-1].Rows != 2 {
		t.Fatalf("choices %+v", p.Choices)
	}
}

// Неуверенный, «не определил», чужая категория, ошибка и отсутствие классификатора → цель по умолчанию «на просмотр».
//
// Мутации: убрать порог уверенности; принимать категорию вне целей; при ошибке падать — тест краснеет.
func TestPlanSplit_AmbiguousFallsBackToDefault(t *testing.T) {
	src, targets, prod, rest := splitFixture()
	other := Category{ID: uuid.New(), Name: "Транспорт"}
	replies := map[string]func() (Classification, error){
		"low":     func() (Classification, error) { return pick(rest, 0.3), nil },
		"none":    func() (Classification, error) { return Classification{Action: ActionNone, Confidence: 0.9}, nil },
		"foreign": func() (Classification, error) { return pick(other, 0.99), nil },
		"create": func() (Classification, error) {
			return Classification{Action: ActionCreate, Name: "Х", Confidence: 0.99}, nil
		},
		"error": func() (Classification, error) { return Classification{}, errors.New("боом") },
		"edge":  func() (Classification, error) { return pick(rest, 0.5), nil }, // ровно порог — уверен
	}
	spy := &splitSpy{reply: func(d string, _ []Candidate) (Classification, error) { return replies[d]() }}
	p, err := PlanSplit(context.Background(), src, targets, rowsOf("low", "none", "foreign", "create", "error", "edge"), spy, nil)
	if err != nil {
		t.Fatal(err)
	}
	basis := map[string]string{}
	tgt := map[string]string{}
	for _, r := range p.Rows {
		basis[r.Row.Description], tgt[r.Row.Description] = r.Basis, r.Target.Name
	}
	for _, k := range []string{"low", "none", "foreign", "create"} {
		if basis[k] != BasisAmbiguous || tgt[k] != prod.Name {
			t.Errorf("%s: %s/%s, ждали default:ambiguous/Продукты", k, basis[k], tgt[k])
		}
	}
	if basis["error"] != BasisError || tgt["error"] != prod.Name {
		t.Errorf("error: %s/%s", basis["error"], tgt["error"])
	}
	if basis["edge"] != BasisClassified || tgt["edge"] != rest.Name {
		t.Errorf("edge: %s/%s, порог 0,5 включительно", basis["edge"], tgt["edge"])
	}
	// без классификатора всё уходит в default:error, ничего не падает
	p2, err := PlanSplit(context.Background(), src, targets, rowsOf("x"), nil, nil)
	if err != nil || p2.Rows[0].Basis != BasisError || p2.Rows[0].Target.ID != prod.ID {
		t.Fatalf("без классификатора: %+v %v", p2.Rows, err)
	}
}

func TestPlanSplit_NoDefaultTarget(t *testing.T) {
	src, targets, _, _ := splitFixture()
	targets[0].IsDefault = false
	if _, err := PlanSplit(context.Background(), src, targets, nil, nil, nil); err == nil {
		t.Fatal("план без цели по умолчанию принят")
	}
}
