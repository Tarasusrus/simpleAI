package categories

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fakeDir struct {
	cats       map[string]Category // ключ: lower(name)
	split      map[uuid.UUID][]SplitTarget
	assignable []Category
	created    []string
	resolved   []string
}

func (f *fakeDir) Resolve(_ context.Context, name, _ string) (Category, bool, error) {
	f.resolved = append(f.resolved, name)
	c, ok := f.cats[lowerKey(name)]
	return c, ok, nil
}

func (f *fakeDir) GetOrCreate(_ context.Context, name, _ string) (Category, bool, error) {
	f.created = append(f.created, name)
	if c, ok := f.cats[lowerKey(name)]; ok {
		return c, false, nil
	}
	c := Category{ID: uuid.New(), Name: name, Type: "expense"}
	f.cats[lowerKey(name)] = c
	return c, true, nil
}

func (f *fakeDir) SplitTargets(_ context.Context, id uuid.UUID) ([]SplitTarget, error) {
	return f.split[id], nil
}

func (f *fakeDir) ListAssignable(context.Context, string) ([]Category, error) {
	return f.assignable, nil
}

func lowerKey(s string) string { return strings.ToLower(Clean(s)) }

type fakeMemory struct {
	cat   Category
	ok    bool
	calls int
	got   string
}

func (m *fakeMemory) MostUsedCategory(_ context.Context, _ string, d string) (Category, bool, error) {
	m.calls++
	m.got = d
	return m.cat, m.ok, nil
}

type spyClassifier struct {
	res        Classification
	err        error
	calls      int
	desc       string
	candidates []Candidate
}

func (s *spyClassifier) Classify(_ context.Context, d string, c []Candidate) (Classification, error) {
	s.calls++
	s.desc, s.candidates = d, c
	return s.res, s.err
}

type world struct {
	dir                    *fakeDir
	mem                    *fakeMemory
	cls                    *spyClassifier
	food, rest, other, eda Category
}

func newWorld() *world {
	w := &world{
		food:  Category{ID: uuid.New(), Name: "Продукты", Type: "expense"},
		rest:  Category{ID: uuid.New(), Name: "Рестораны", Type: "expense"},
		other: Category{ID: uuid.New(), Name: "Прочее", Type: "expense"},
		eda:   Category{ID: uuid.New(), Name: "Еда", Type: "expense"},
		mem:   &fakeMemory{},
		cls:   &spyClassifier{},
	}
	w.dir = &fakeDir{
		cats:       map[string]Category{"продукты": w.food, "рестораны": w.rest, "прочее": w.other, "еда": w.eda},
		split:      map[uuid.UUID][]SplitTarget{},
		assignable: []Category{w.food, w.rest, w.other},
	}
	return w
}

func (w *world) chooser(mode string) *Chooser {
	return &Chooser{Dir: w.dir, Memory: w.mem, Classifier: w.cls, Mode: mode, Hints: DefaultHints}
}

func (w *world) enableSplit() {
	w.dir.split[w.eda.ID] = []SplitTarget{{Category: w.food, IsDefault: true}, {Category: w.rest}}
	w.dir.assignable = []Category{w.food, w.rest, w.other} // без «Еды»
}

func expense(name, desc string) ChooseInput {
	return ChooseInput{Name: name, Description: desc, Type: "expense"}
}

// Названная пользователем категория принимается как есть: ни память, ни классификатор не вызываются.
// Неизвестное имя создаётся с точно этим именем (спай на аргумент GetOrCreate).
//
// Мутация: вызывать классификатор или память до fromName — тест краснеет.
func TestChooser_ExplicitNameSkipsMemoryAndClassifier(t *testing.T) {
	w := newWorld()
	ch := w.chooser(ModeOn)
	got, err := ch.Choose(context.Background(), expense("рестораны", "поход в кафе"))
	if err != nil || got.Category.ID != w.rest.ID || got.Source != SourceExplicit || got.Note != "" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got, err = ch.Choose(context.Background(), expense("Хобби", "краски"))
	if err != nil || got.Category.Name != "Хобби" || got.Source != SourceExplicit {
		t.Fatalf("новое имя: got=%+v err=%v", got, err)
	}
	if len(w.dir.created) != 1 || w.dir.created[0] != "Хобби" {
		t.Errorf("GetOrCreate вызван с %v, ждали [Хобби]", w.dir.created)
	}
	if w.cls.calls != 0 || w.mem.calls != 0 {
		t.Errorf("классификатор=%d память=%d вызовов, ждали 0 и 0", w.cls.calls, w.mem.calls)
	}
}

// Порядок: память раньше классификатора; классификатор получает описание и назначаемые категории
// (без «Еды») с подсказками; уверенный ответ принимается.
//
// Мутации: убрать шаг памяти, убрать подсказки, передать в классификатор все категории — краснеет.
func TestChooser_MemoryThenClassifier(t *testing.T) {
	w := newWorld()
	w.mem.cat, w.mem.ok = w.food, true
	got, err := w.chooser(ModeOn).Choose(context.Background(), expense("", "  Пятёрочка "))
	if err != nil || got.Category.ID != w.food.ID || got.Source != SourceMemory {
		t.Fatalf("память: got=%+v err=%v", got, err)
	}
	if w.mem.got != "Пятёрочка" || w.cls.calls != 0 {
		t.Errorf("память получила %q, классификатор вызван %d раз", w.mem.got, w.cls.calls)
	}

	w = newWorld()
	w.cls.res = Classification{Action: ActionExisting, CategoryID: w.rest.ID, Confidence: 0.9}
	got, err = w.chooser(ModeOn).Choose(context.Background(), expense("", "поход в кафе"))
	if err != nil || got.Category.ID != w.rest.ID || got.Source != SourceClassifier || got.Note == "" {
		t.Fatalf("классификатор: got=%+v err=%v", got, err)
	}
	if w.cls.desc != "поход в кафе" || len(w.cls.candidates) != 3 || w.cls.candidates[1].Name != "Рестораны" || w.cls.candidates[1].Hint == "" {
		t.Errorf("в классификатор ушло desc=%q candidates=%+v", w.cls.desc, w.cls.candidates)
	}
}

// Деградация: ошибка, низкая уверенность, none, create и чужой id дают «Прочее» + предложение,
// категория из ответа модели не создаётся.
//
// Мутации: убрать деградацию (вернуть пустой Choice) или создавать категорию из create — краснеет.
func TestChooser_DegradesToOtherWithProposal(t *testing.T) {
	cases := map[string]*spyClassifier{
		"ошибка":      {err: errors.New("503")},
		"уверенность": {res: Classification{Action: ActionExisting, CategoryID: uuid.Nil, Confidence: 0.2}},
		"none":        {res: Classification{Action: ActionNone}},
		"create":      {res: Classification{Action: ActionCreate, Name: "Хобби", Confidence: 0.9, Reason: "новое"}},
	}
	for name, cls := range cases {
		w := newWorld()
		w.cls = cls
		got, err := w.chooser(ModeOn).Choose(context.Background(), expense("", "краски для рисования"))
		if err != nil || got.Category.ID != w.other.ID || got.Source != SourceDegraded || got.Note == "" || got.Proposal == nil {
			t.Fatalf("%s: got=%+v err=%v", name, got, err)
		}
		if _, made := w.dir.cats["хобби"]; made {
			t.Errorf("%s: категория из ответа модели создана", name)
		}
		if name == "create" && got.Proposal.Name != "Хобби" {
			t.Errorf("предложение = %q, ждали Хобби", got.Proposal.Name)
		}
	}
}

// Режимы: off — как раньше (нет категории, ничего не вызвано); shadow — классификатор вызван,
// но запись не меняется; пустое описание — без категории и без вызовов.
//
// Мутация: применять результат в shadow или звать классификатор в off — краснеет.
func TestChooser_ModesAndEmptyDescription(t *testing.T) {
	w := newWorld()
	w.cls.res = Classification{Action: ActionExisting, CategoryID: w.rest.ID, Confidence: 1}
	if got, err := w.chooser(ModeOff).Choose(context.Background(), expense("", "кафе")); err != nil || got.Category.ID != uuid.Nil || w.cls.calls != 0 || w.mem.calls != 0 {
		t.Fatalf("off: got=%+v calls=%d/%d err=%v", got, w.cls.calls, w.mem.calls, err)
	}
	w.mem.cat, w.mem.ok = w.food, true // память в shadow не применяется: запись не меняется
	if got, err := w.chooser(ModeShadow).Choose(context.Background(), expense("", "кафе")); err != nil || got.Category.ID != uuid.Nil || w.cls.calls != 1 || w.mem.calls != 0 {
		t.Fatalf("shadow: got=%+v calls=%d memory=%d err=%v", got, w.cls.calls, w.mem.calls, err)
	}
	w.mem.cat, w.mem.ok, w.mem.calls = Category{}, false, 0
	w = newWorld()
	if got, err := w.chooser(ModeOn).Choose(context.Background(), expense("", "  ")); err != nil || got.Category.ID != uuid.Nil || w.cls.calls != 0 {
		t.Fatalf("пустое описание: got=%+v calls=%d err=%v", got, w.cls.calls, err)
	}
	if got, err := w.chooser(ModeOn).Choose(context.Background(), ChooseInput{Description: "зарплата", Type: "income"}); err != nil || got.Category.ID != uuid.Nil || w.cls.calls != 0 {
		t.Fatalf("доход: got=%+v calls=%d err=%v", got, w.cls.calls, err)
	}
}

// «Еда» (split-источник) не назначается: память (если цель из списка) → классификатор среди целей
// (спай на аргумент) → цель по умолчанию «Продукты».
//
// Мутации: отдать «Еду» как есть, передать в классификатор не только цели, убрать default — краснеет.
func TestChooser_SplitSourceNeverAssigned(t *testing.T) {
	w := newWorld()
	w.enableSplit()
	w.cls.res = Classification{Action: ActionExisting, CategoryID: w.rest.ID, Confidence: 0.9}
	got, err := w.chooser(ModeOn).Choose(context.Background(), expense("еда", "поход в кафе"))
	if err != nil || got.Category.ID != w.rest.ID || got.Source != SourceClassifier {
		t.Fatalf("классификатор: got=%+v err=%v", got, err)
	}
	if len(w.cls.candidates) != 2 || w.cls.candidates[0].ID != w.food.ID || w.cls.candidates[1].ID != w.rest.ID {
		t.Errorf("кандидаты = %+v, ждали только цели разделения", w.cls.candidates)
	}

	w = newWorld()
	w.enableSplit()
	got, err = w.chooser(ModeOn).Choose(context.Background(), expense("Еда", ""))
	if err != nil || got.Category.ID != w.food.ID || got.Source != SourceDefault || w.cls.calls != 0 {
		t.Fatalf("без описания: got=%+v calls=%d err=%v", got, w.cls.calls, err)
	}

	w = newWorld()
	w.enableSplit()
	w.cls.err = errors.New("503")
	got, err = w.chooser(ModeOn).Choose(context.Background(), expense("еда", "что-то"))
	if err != nil || got.Category.ID != w.food.ID || got.Source != SourceDefault {
		t.Fatalf("сбой классификатора: got=%+v err=%v", got, err)
	}

	w = newWorld()
	w.enableSplit()
	w.mem.cat, w.mem.ok = w.rest, true
	got, err = w.chooser(ModeOff).Choose(context.Background(), expense("еда", "макдональдс"))
	if err != nil || got.Category.ID != w.rest.ID || got.Source != SourceMemory {
		t.Fatalf("память: got=%+v err=%v", got, err)
	}
}
