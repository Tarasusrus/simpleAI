// Package classifier — eval классификатора категорий (ADR-003, ADR-012).
//
// Прогоняет описания трат из evals/classifier_golden_set.jsonl через categories.Classifier и
// считает три доли: верно, неверно, деградация (ошибка, none, низкая уверенность, create). Доля
// деградаций важна отдельно: бот в этом случае пишет в «Прочее», и это не то же, что ошибка выбора.
package classifier

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
)

// Case — один кейс golden-набора.
type Case struct {
	ID         string   `json:"id"`
	Input      string   `json:"input"`
	Expected   string   `json:"expected"` // имя категории
	Tags       []string `json:"tags,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}

// Outcome — исход одного кейса.
type Outcome string

const (
	OK       Outcome = "ok"       // выбрана ожидаемая категория
	Wrong    Outcome = "wrong"    // выбрана другая существующая категория
	Degraded Outcome = "degraded" // none, create или уверенность ниже порога: модель ответила, но не определила
	Errored  Outcome = "errored"  // вызов не дошёл: лимит провайдера, таймаут, обрезанный или битый ответ
)

// MinConfidence — как у Chooser: ниже ответ считается «не определил».
const MinConfidence = 0.5

// Result — результат кейса.
type Result struct {
	CaseID   string  `json:"case_id"`
	Input    string  `json:"input"`
	Expected string  `json:"expected"`
	Actual   string  `json:"actual,omitempty"`
	Action   string  `json:"action,omitempty"`
	Conf     float64 `json:"confidence,omitempty"`
	Outcome  Outcome `json:"outcome"`
	Err      string  `json:"error,omitempty"`
}

// Report — сводка прогона.
type Report struct {
	Total, OK, Wrong, Degraded, Errored int
	Results                             []Result
}

// Answered — кейсы, на которые модель ответила (без инфраструктурных сбоев).
func (r Report) Answered() int { return r.Total - r.Errored }

// Accuracy — доля верных среди ответивших: качество выбора отдельно от лимитов бесплатных провайдеров.
func (r Report) Accuracy() float64 { return ratio(r.OK, r.Answered()) }

// WrongRate — доля неверных среди ответивших (худший исход: трата ушла не туда без пометки).
func (r Report) WrongRate() float64 { return ratio(r.Wrong, r.Answered()) }

// DegradedRate — доля «не определил» среди ответивших.
func (r Report) DegradedRate() float64 { return ratio(r.Degraded, r.Answered()) }

// ErrorRate — доля вызовов, которые не дошли до ответа. Высокая доля делает прогон недействительным.
func (r Report) ErrorRate() float64 { return ratio(r.Errored, r.Total) }

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Load читает golden-набор (jsonl), пропуская deprecated.
func Load(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // чтение
	return Read(f)
}

// Read разбирает jsonl.
func Read(r io.Reader) ([]Case, error) {
	var out []Case
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var c Case
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("строка %d: %w", line, err)
		}
		if c.ID == "" || c.Input == "" || c.Expected == "" {
			return nil, fmt.Errorf("строка %d: нужны id, input и expected", line)
		}
		if !c.Deprecated {
			out = append(out, c)
		}
	}
	return out, sc.Err()
}

// Run прогоняет кейсы через классификатор. Кандидаты одни на все кейсы (как в проде для расхода).
func Run(ctx context.Context, cls categories.Classifier, cases []Case, cands []categories.Candidate) Report {
	byID := make(map[uuid.UUID]string, len(cands))
	for _, c := range cands {
		byID[c.ID] = c.Name
	}
	rep := Report{Total: len(cases)}
	for _, c := range cases {
		res := Result{CaseID: c.ID, Input: c.Input, Expected: c.Expected}
		got, err := cls.Classify(ctx, c.Input, cands)
		switch {
		case errors.Is(err, categories.ErrBadClassification):
			// Ответ нарушил контракт (чужой id, плохое имя): в проде это деградация в «Прочее»,
			// а не сбой инфраструктуры.
			res.Outcome, res.Err = Degraded, err.Error()
		case err != nil:
			res.Outcome, res.Err = Errored, err.Error()
		case got.Action != categories.ActionExisting || got.Confidence < MinConfidence:
			res.Outcome, res.Action, res.Conf = Degraded, got.Action, got.Confidence
		default:
			res.Action, res.Conf, res.Actual = got.Action, got.Confidence, byID[got.CategoryID]
			if res.Actual == c.Expected {
				res.Outcome = OK
			} else {
				res.Outcome = Wrong
			}
		}
		switch res.Outcome {
		case OK:
			rep.OK++
		case Wrong:
			rep.Wrong++
		case Errored:
			rep.Errored++
		default:
			rep.Degraded++
		}
		rep.Results = append(rep.Results, res)
	}
	return rep
}
