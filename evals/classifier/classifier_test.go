package classifier

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
)

type scripted struct {
	byInput map[string]categories.Classification
	errFor  map[string]error
	seen    []string
}

func (s *scripted) Classify(_ context.Context, d string, _ []categories.Candidate) (categories.Classification, error) {
	s.seen = append(s.seen, d)
	if e := s.errFor[d]; e != nil {
		return categories.Classification{}, e
	}
	return s.byInput[d], nil
}

var (
	idP = uuid.New()
	idR = uuid.New()
	cs  = []categories.Candidate{{ID: idP, Name: "Продукты"}, {ID: idR, Name: "Рестораны"}}
)

// Исходы: верно / неверно / деградация (ошибка, none, create, низкая уверенность) считаются раздельно.
//
// Мутации: считать низкую уверенность верной, смешать Wrong и Degraded, не учитывать ошибку — краснеет.
func TestRun_ClassifiesOutcomes(t *testing.T) {
	cases := []Case{
		{ID: "1", Input: "ok", Expected: "Рестораны"},
		{ID: "2", Input: "wrong", Expected: "Рестораны"},
		{ID: "3", Input: "err", Expected: "Рестораны"},
		{ID: "4", Input: "none", Expected: "Рестораны"},
		{ID: "5", Input: "create", Expected: "Рестораны"},
		{ID: "6", Input: "low", Expected: "Рестораны"},
		{ID: "7", Input: "bad", Expected: "Рестораны"},
	}
	cls := &scripted{
		byInput: map[string]categories.Classification{
			"ok":     {Action: categories.ActionExisting, CategoryID: idR, Confidence: 0.9},
			"wrong":  {Action: categories.ActionExisting, CategoryID: idP, Confidence: 0.9},
			"none":   {Action: categories.ActionNone, Confidence: 0.9},
			"create": {Action: categories.ActionCreate, Name: "Хобби", Confidence: 0.9},
			"low":    {Action: categories.ActionExisting, CategoryID: idR, Confidence: 0.3},
		},
		errFor: map[string]error{"err": errors.New("503"), "bad": fmt.Errorf("%w: чужой id", categories.ErrBadClassification)},
	}
	rep := Run(context.Background(), cls, cases, cs)
	if rep.Total != 7 || rep.OK != 1 || rep.Wrong != 1 || rep.Degraded != 4 || rep.Errored != 1 {
		t.Fatalf("ok=%d wrong=%d degraded=%d errored=%d total=%d, ждали 1/1/4/1/7", rep.OK, rep.Wrong, rep.Degraded, rep.Errored, rep.Total)
	}
	want := map[string]Outcome{"1": OK, "2": Wrong, "3": Errored, "4": Degraded, "5": Degraded, "6": Degraded, "7": Degraded}
	for _, r := range rep.Results {
		if r.Outcome != want[r.CaseID] {
			t.Errorf("кейс %s: %s, ждали %s", r.CaseID, r.Outcome, want[r.CaseID])
		}
	}
	// Доли считаются среди ответивших (6 из 7): сбой вызова не должен портить оценку качества выбора.
	if rep.Answered() != 6 || rep.Accuracy() != 1.0/6 || rep.WrongRate() != 1.0/6 || rep.DegradedRate() != 4.0/6 || rep.ErrorRate() != 1.0/7 {
		t.Errorf("доли: %v %v %v %v", rep.Accuracy(), rep.WrongRate(), rep.DegradedRate(), rep.ErrorRate())
	}
	if len(cls.seen) != 7 || cls.seen[0] != "ok" {
		t.Errorf("в классификатор ушли описания %v", cls.seen)
	}
}

func TestReport_EmptyHasZeroRates(t *testing.T) {
	var r Report
	if r.Accuracy() != 0 || r.WrongRate() != 0 || r.DegradedRate() != 0 {
		t.Error("пустой отчёт должен давать нули, а не NaN")
	}
}

func TestRead_ValidatesAndSkipsDeprecated(t *testing.T) {
	got, err := Read(strings.NewReader(`{"id":"a","input":"x","expected":"Продукты"}
{"id":"b","input":"y","expected":"Продукты","deprecated":true}

`))
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	for _, bad := range []string{`{"id":"a","input":"x"}`, `not json`} {
		if _, err := Read(strings.NewReader(bad)); err == nil {
			t.Errorf("принято %q", bad)
		}
	}
}

// Сам golden-набор: идентификаторы уникальны, ожидаемые категории существуют в списке прода,
// кейсов достаточно и оба главных класса (кафе и магазин) представлены.
//
// Мутация: испортить имя категории в наборе — тест краснеет.
func TestGoldenSet_IsConsistent(t *testing.T) {
	cases, err := Load("../classifier_golden_set.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{}
	for _, n := range []string{"Продукты", "Рестораны", "Транспорт", "Жильё", "Здоровье", "Красота", "Развлечения", "Подписки", "Одежда", "Переводы", "Прочее"} {
		valid[n] = true
	}
	seen := map[string]bool{}
	byCat := map[string]int{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Errorf("дубль id %s", c.ID)
		}
		seen[c.ID] = true
		if !valid[c.Expected] {
			t.Errorf("%s: неизвестная категория %q", c.ID, c.Expected)
		}
		byCat[c.Expected]++
	}
	if len(cases) < 30 || byCat["Рестораны"] < 10 || byCat["Продукты"] < 10 {
		t.Errorf("кейсов %d, Рестораны %d, Продукты %d: набор слишком мал", len(cases), byCat["Рестораны"], byCat["Продукты"])
	}
}
