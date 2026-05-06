// Package evals — типы и метрики для routing-eval.
//
// Cхема golden_set.jsonl и формат run-файлов фиксированы в docs/adr/003-eval-suite-convention.md.
// Любое изменение полей — через обновление ADR.
package evals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// Expected — ожидаемое routing-решение для кейса.
type Expected struct {
	Skill  string `json:"skill"`
	Action string `json:"action"`
}

// Case — один кейс из golden_set.jsonl.
type Case struct {
	ID         string   `json:"id"`
	Input      string   `json:"input"`
	Expected   Expected `json:"expected"`
	Tags       []string `json:"tags,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}

// Result — результат прогона одного кейса.
type Result struct {
	CaseID         string   `json:"case_id"`
	ExpectedSkill  string   `json:"expected_skill"`
	ExpectedAction string   `json:"expected_action"`
	ActualSkill    string   `json:"actual_skill"`
	ActualAction   string   `json:"actual_action"`
	Pass           bool     `json:"pass"`
	Tags           []string `json:"tags,omitempty"`
	Error          string   `json:"error,omitempty"`
	RawResponse    string   `json:"raw_response,omitempty"`
}

// Summary — агрегированные метрики прогона.
type Summary struct {
	Total      int                `json:"total"`
	Pass       int                `json:"pass"`
	Fail       int                `json:"fail"`
	Accuracy   float64            `json:"accuracy"`
	PerSkill   map[string]float64 `json:"per_skill"`
	PerTag     map[string]float64 `json:"per_tag"`
	FailedIDs  []string           `json:"failed_ids"`
}

// Run — метаданные + результаты + summary одного прогона.
type Run struct {
	Timestamp   time.Time `json:"timestamp"`
	Commit      string    `json:"commit"`
	PromptHash  string    `json:"prompt_hash"`
	Results     []Result  `json:"results"`
	Summary     Summary   `json:"summary"`
}

// Diff — сравнение двух прогонов: что упало, что починилось.
type Diff struct {
	Regressed []string `json:"regressed"`
	Improved  []string `json:"improved"`
}

// LoadCases читает golden_set.jsonl. Пустые строки и строки начинающиеся с `#` игнорируются.
// Кейсы с deprecated=true исключаются.
func LoadCases(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	cases, err := parseCases(f, path)
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return cases, nil
}

func parseCases(r io.Reader, path string) ([]Case, error) {
	var cases []Case
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scan.Scan() {
		lineNo++
		line := scan.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var c Case
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if c.Deprecated {
			continue
		}
		cases = append(cases, c)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

// FilterByTag оставляет только кейсы, у которых в Tags есть tag.
// Пустой tag — без фильтрации.
func FilterByTag(cases []Case, tag string) []Case {
	if tag == "" {
		return cases
	}
	out := make([]Case, 0, len(cases))
	for _, c := range cases {
		for _, t := range c.Tags {
			if t == tag {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Summarize строит метрики из списка результатов.
func Summarize(results []Result) Summary {
	s := Summary{
		Total:     len(results),
		PerSkill:  map[string]float64{},
		PerTag:    map[string]float64{},
		FailedIDs: []string{},
	}
	skillTotal := map[string]int{}
	skillPass := map[string]int{}
	tagTotal := map[string]int{}
	tagPass := map[string]int{}
	for _, r := range results {
		if r.Pass {
			s.Pass++
		} else {
			s.FailedIDs = append(s.FailedIDs, r.CaseID)
		}
		skillTotal[r.ExpectedSkill]++
		if r.Pass {
			skillPass[r.ExpectedSkill]++
		}
		for _, t := range r.Tags {
			tagTotal[t]++
			if r.Pass {
				tagPass[t]++
			}
		}
	}
	s.Fail = s.Total - s.Pass
	if s.Total > 0 {
		s.Accuracy = float64(s.Pass) / float64(s.Total)
	}
	for k, n := range skillTotal {
		s.PerSkill[k] = float64(skillPass[k]) / float64(n)
	}
	for k, n := range tagTotal {
		s.PerTag[k] = float64(tagPass[k]) / float64(n)
	}
	sort.Strings(s.FailedIDs)
	return s
}

// DiffRuns сравнивает два прогона по case_id.
// Regressed: было pass в prev → стало fail в curr.
// Improved: было fail в prev → стало pass в curr.
// Кейсы которых нет в одном из прогонов — пропускаются.
func DiffRuns(prev, curr Run) Diff {
	prevByID := map[string]bool{}
	for _, r := range prev.Results {
		prevByID[r.CaseID] = r.Pass
	}
	d := Diff{Regressed: []string{}, Improved: []string{}}
	for _, r := range curr.Results {
		prevPass, ok := prevByID[r.CaseID]
		if !ok {
			continue
		}
		switch {
		case prevPass && !r.Pass:
			d.Regressed = append(d.Regressed, r.CaseID)
		case !prevPass && r.Pass:
			d.Improved = append(d.Improved, r.CaseID)
		}
	}
	sort.Strings(d.Regressed)
	sort.Strings(d.Improved)
	return d
}

// runTail — последняя строка run-файла: meta-поля + summary.
// Формат фиксирован ADR-003.
type runTail struct {
	Summary    Summary   `json:"summary"`
	Timestamp  time.Time `json:"timestamp"`
	Commit     string    `json:"commit"`
	PromptHash string    `json:"prompt_hash"`
}

// WriteRun пишет run в jsonl: одна строка на Result, последняя строка — runTail.
// Формат соответствует ADR-003.
func WriteRun(w io.Writer, r Run) error {
	enc := json.NewEncoder(w)
	for _, res := range r.Results {
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return enc.Encode(runTail{
		Summary:    r.Summary,
		Timestamp:  r.Timestamp,
		Commit:     r.Commit,
		PromptHash: r.PromptHash,
	})
}

// ReadRun читает jsonl-файл прогона: все строки кроме последней — Result, последняя — meta+summary.
func ReadRun(path string) (Run, error) {
	f, err := os.Open(path)
	if err != nil {
		return Run{}, err
	}
	r, err := readRunFromReader(f, path)
	closeErr := f.Close()
	if err != nil {
		return Run{}, err
	}
	if closeErr != nil {
		return Run{}, closeErr
	}
	return r, nil
}

func readRunFromReader(rd io.Reader, path string) (Run, error) {
	var lines [][]byte
	scan := bufio.NewScanner(rd)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		b := append([]byte(nil), scan.Bytes()...)
		if len(b) == 0 {
			continue
		}
		lines = append(lines, b)
	}
	if err := scan.Err(); err != nil {
		return Run{}, err
	}
	if len(lines) == 0 {
		return Run{}, fmt.Errorf("empty run file: %s", path)
	}
	r := Run{}
	for _, line := range lines[:len(lines)-1] {
		var res Result
		if err := json.Unmarshal(line, &res); err != nil {
			return Run{}, fmt.Errorf("parse result: %w", err)
		}
		r.Results = append(r.Results, res)
	}
	var tail runTail
	if err := json.Unmarshal(lines[len(lines)-1], &tail); err != nil {
		return Run{}, fmt.Errorf("parse tail: %w", err)
	}
	r.Summary = tail.Summary
	r.Timestamp = tail.Timestamp
	r.Commit = tail.Commit
	r.PromptHash = tail.PromptHash
	return r, nil
}
