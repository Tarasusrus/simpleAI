package categories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeClassifier struct {
	res   Classification
	err   error
	calls int
	got   []Candidate
}

func (f *fakeClassifier) Classify(_ context.Context, _ string, c []Candidate) (Classification, error) {
	f.calls++
	f.got = c
	return f.res, f.err
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var (
	idFood = uuid.MustParse("00000000-0000-0000-0000-00000000f001")
	idCafe = uuid.MustParse("00000000-0000-0000-0000-00000000f002")
	cands  = []Candidate{{ID: idFood, Name: "Продукты", Hint: "магазины"}, {ID: idCafe, Name: "Рестораны", Hint: "кафе"}}
)

func guard(inner Classifier, cl *clock, cfg GuardConfig) *GuardedClassifier {
	cfg.Now = cl.now
	return NewGuardedClassifier(inner, cfg)
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)} }

// Ответ с id, которого нет в списке, отклоняется; настоящий id возвращает имя из списка.
//
// Мутация: убрать цикл проверки id в validate — тест краснеет.
func TestGuard_ExistingMustBeFromList(t *testing.T) {
	cl := newClock()
	f := &fakeClassifier{res: Classification{Action: ActionExisting, CategoryID: uuid.New(), Name: "Выдуманная", Confidence: 0.9}}
	g := guard(f, cl, GuardConfig{})
	if _, err := g.Classify(context.Background(), "кафе", cands); !errors.Is(err, ErrBadClassification) {
		t.Fatalf("чужой id: err=%v, ждали ErrBadClassification", err)
	}
	f.res = Classification{Action: ActionExisting, CategoryID: idCafe, Name: "что-то своё", Confidence: 1.7}
	got, err := g.Classify(context.Background(), "кафе", cands)
	if err != nil || got.Name != "Рестораны" || got.CategoryID != idCafe || got.Confidence != 1 {
		t.Fatalf("got=%+v err=%v, ждали Рестораны с confidence 1", got, err)
	}
}

// Имя новой категории: пустое, длинное, с « / », защищённое и совпадающее с существующей отклоняются.
//
// Мутация: убрать любую из веток checkNewName — соответствующая строка таблицы краснеет.
func TestGuard_CreateNameRules(t *testing.T) {
	long := ""
	for i := 0; i < maxNameRunes+1; i++ {
		long += "я"
	}
	for name, bad := range map[string]string{
		"пустое": "  ", "длинное": long, "слэш": "Кафе / ресторан", "защищённое еда": "Еда",
		"защищённое прочее": "прочее", "уже есть": " продукты ",
	} {
		g := guard(&fakeClassifier{res: Classification{Action: ActionCreate, Name: bad}}, newClock(), GuardConfig{})
		if _, err := g.Classify(context.Background(), "x", cands); !errors.Is(err, ErrBadClassification) {
			t.Errorf("%s (%q): err=%v, ждали ErrBadClassification", name, bad, err)
		}
	}
	g := guard(&fakeClassifier{res: Classification{Action: ActionCreate, Name: "  Хобби  "}}, newClock(), GuardConfig{})
	got, err := g.Classify(context.Background(), "x", cands)
	if err != nil || got.Name != "Хобби" {
		t.Fatalf("нормальное имя: got=%+v err=%v", got, err)
	}
}

// Потолок новых категорий в сутки; на следующие сутки счётчик сбрасывается.
//
// Мутация: убрать reserveCreate или rollDay — тест краснеет.
func TestGuard_CreateCapPerDay(t *testing.T) {
	cl := newClock()
	f := &fakeClassifier{res: Classification{Action: ActionCreate, Name: "Хобби"}}
	g := guard(f, cl, GuardConfig{MaxCreatesPerDay: 2})
	for i := 0; i < 2; i++ {
		if _, err := g.Classify(context.Background(), "x", cands); err != nil {
			t.Fatalf("создание %d: %v", i, err)
		}
	}
	if _, err := g.Classify(context.Background(), "x", cands); !errors.Is(err, ErrBadClassification) {
		t.Fatalf("третье создание: err=%v, ждали отказ по потолку", err)
	}
	cl.t = cl.t.Add(24 * time.Hour)
	if _, err := g.Classify(context.Background(), "x", cands); err != nil {
		t.Fatalf("на следующие сутки: %v", err)
	}
}

// Суточный потолок вызовов: сверх него inner не вызывается.
//
// Мутация: убрать проверку calls >= MaxCallsPerDay — тест краснеет.
func TestGuard_CallCapPerDay(t *testing.T) {
	cl := newClock()
	f := &fakeClassifier{res: Classification{Action: ActionNone}}
	g := guard(f, cl, GuardConfig{MaxCallsPerDay: 3})
	for i := 0; i < 3; i++ {
		if _, err := g.Classify(context.Background(), "x", cands); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Classify(context.Background(), "x", cands); !errors.Is(err, ErrClassifierBudget) {
		t.Fatalf("err=%v, ждали ErrClassifierBudget", err)
	}
	if f.calls != 3 {
		t.Errorf("inner вызван %d раз, ждали 3: сверх потолка вызовов быть не должно", f.calls)
	}
}

// После N сбоев подряд вызовы приостанавливаются на паузу; успех сбрасывает счётчик сбоев;
// после паузы вызовы возобновляются.
//
// Мутация: не сбрасывать failures в recordSuccess или не ставить pausedTill — тест краснеет.
func TestGuard_BreakerOpensAndRecovers(t *testing.T) {
	cl := newClock()
	f := &fakeClassifier{err: errors.New("503")}
	g := guard(f, cl, GuardConfig{BreakerFailures: 3, BreakerPause: 15 * time.Minute})
	for i := 0; i < 2; i++ {
		g.Classify(context.Background(), "x", cands) //nolint:errcheck // сбои нужны для счётчика
	}
	f.err, f.res = nil, Classification{Action: ActionNone}
	if _, err := g.Classify(context.Background(), "x", cands); err != nil {
		t.Fatal(err)
	}
	f.err = errors.New("503")
	for i := 0; i < 2; i++ {
		g.Classify(context.Background(), "x", cands) //nolint:errcheck // сбои нужны для счётчика
	}
	if _, err := g.Classify(context.Background(), "x", cands); errors.Is(err, ErrClassifierUnavailable) {
		t.Fatal("успех не сбросил счётчик: предохранитель сработал на 2+1 сбоях")
	}
	// теперь 3 сбоя подряд набраны (2 + этот) → пауза
	calls := f.calls
	if _, err := g.Classify(context.Background(), "x", cands); !errors.Is(err, ErrClassifierUnavailable) {
		t.Fatalf("err=%v, ждали ErrClassifierUnavailable", err)
	}
	if f.calls != calls {
		t.Error("во время паузы inner вызван")
	}
	cl.t = cl.t.Add(16 * time.Minute)
	f.err, f.res = nil, Classification{Action: ActionNone}
	if _, err := g.Classify(context.Background(), "x", cands); err != nil {
		t.Fatalf("после паузы: %v", err)
	}
}

// Кандидаты уходят в inner как есть (спай на аргумент), а none возвращается без имени и id.
func TestGuard_PassesCandidatesAndNone(t *testing.T) {
	f := &fakeClassifier{res: Classification{Action: ActionNone, Name: "мусор", CategoryID: idFood, Confidence: 0.2, Reason: "не ясно"}}
	g := guard(f, newClock(), GuardConfig{})
	got, err := g.Classify(context.Background(), "x", cands)
	if err != nil || got.Action != ActionNone || got.Name != "" || got.CategoryID != uuid.Nil || got.Reason != "не ясно" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if len(f.got) != 2 || f.got[0].ID != idFood || f.got[1].Name != "Рестораны" {
		t.Errorf("в inner ушли кандидаты %+v", f.got)
	}
}
