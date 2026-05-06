package evals

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	tests := []struct {
		name     string
		results  []Result
		wantAcc  float64
		wantPass int
		wantFail int
		wantSkill map[string]float64
		wantTag  map[string]float64
		wantFailed []string
	}{
		{
			name:     "empty",
			results:  nil,
			wantAcc:  0,
			wantSkill: map[string]float64{},
			wantTag:  map[string]float64{},
			wantFailed: []string{},
		},
		{
			name: "all pass",
			results: []Result{
				{CaseID: "r1", ExpectedSkill: "advisor", Pass: true, Tags: []string{"future"}},
				{CaseID: "r2", ExpectedSkill: "budget", Pass: true, Tags: []string{"past"}},
			},
			wantAcc:    1.0,
			wantPass:   2,
			wantSkill:  map[string]float64{"advisor": 1, "budget": 1},
			wantTag:    map[string]float64{"future": 1, "past": 1},
			wantFailed: []string{},
		},
		{
			name: "mixed",
			results: []Result{
				{CaseID: "r1", ExpectedSkill: "advisor", Pass: true, Tags: []string{"future"}},
				{CaseID: "r2", ExpectedSkill: "advisor", Pass: false, Tags: []string{"future", "no_amount"}},
				{CaseID: "r3", ExpectedSkill: "budget", Pass: true, Tags: []string{"past"}},
			},
			wantAcc:    2.0 / 3.0,
			wantPass:   2,
			wantFail:   1,
			wantSkill:  map[string]float64{"advisor": 0.5, "budget": 1.0},
			wantTag:    map[string]float64{"future": 0.5, "no_amount": 0, "past": 1.0},
			wantFailed: []string{"r2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize(tt.results)
			if got.Accuracy != tt.wantAcc {
				t.Errorf("accuracy = %v, want %v", got.Accuracy, tt.wantAcc)
			}
			if got.Pass != tt.wantPass {
				t.Errorf("pass = %d, want %d", got.Pass, tt.wantPass)
			}
			if got.Fail != tt.wantFail {
				t.Errorf("fail = %d, want %d", got.Fail, tt.wantFail)
			}
			if !reflect.DeepEqual(got.PerSkill, tt.wantSkill) {
				t.Errorf("per_skill = %v, want %v", got.PerSkill, tt.wantSkill)
			}
			if !reflect.DeepEqual(got.PerTag, tt.wantTag) {
				t.Errorf("per_tag = %v, want %v", got.PerTag, tt.wantTag)
			}
			if !reflect.DeepEqual(got.FailedIDs, tt.wantFailed) {
				t.Errorf("failed_ids = %v, want %v", got.FailedIDs, tt.wantFailed)
			}
		})
	}
}

func TestDiffRuns(t *testing.T) {
	prev := Run{Results: []Result{
		{CaseID: "r1", Pass: true},
		{CaseID: "r2", Pass: false},
		{CaseID: "r3", Pass: true},
		{CaseID: "r4", Pass: false},
	}}
	curr := Run{Results: []Result{
		{CaseID: "r1", Pass: false}, // regressed
		{CaseID: "r2", Pass: true},  // improved
		{CaseID: "r3", Pass: true},  // unchanged
		{CaseID: "r4", Pass: false}, // unchanged
		{CaseID: "r5", Pass: true},  // new — пропускается
	}}
	got := DiffRuns(prev, curr)
	want := Diff{Regressed: []string{"r1"}, Improved: []string{"r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %+v, want %+v", got, want)
	}
}

func TestLoadCases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "golden.jsonl")
	content := `# comment line
{"id":"r1","input":"купил кофе","expected":{"skill":"budget","action":"add_expense"},"tags":["past"]}

{"id":"r2","input":"хочу купить блендер","expected":{"skill":"advisor","action":"advice"},"tags":["future"],"notes":"real case"}
{"id":"r3","input":"old","expected":{"skill":"x","action":"y"},"deprecated":true}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2 (deprecated должен быть исключён)", len(cases))
	}
	if cases[0].ID != "r1" || cases[1].ID != "r2" {
		t.Errorf("ids = %s,%s", cases[0].ID, cases[1].ID)
	}
	if cases[1].Notes != "real case" {
		t.Errorf("notes lost: %q", cases[1].Notes)
	}
}

func TestLoadCases_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(path, []byte(`{not json}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCases(path); err == nil {
		t.Error("expected error on invalid jsonl")
	}
}

func TestFilterByTag(t *testing.T) {
	cases := []Case{
		{ID: "a", Tags: []string{"future", "no_amount"}},
		{ID: "b", Tags: []string{"past"}},
		{ID: "c", Tags: []string{"future"}},
		{ID: "d"},
	}
	got := FilterByTag(cases, "future")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("filter result wrong: %+v", got)
	}
	if all := FilterByTag(cases, ""); len(all) != 4 {
		t.Errorf("empty tag should not filter, got %d", len(all))
	}
}

func TestWriteReadRun_RoundTrip(t *testing.T) {
	r := Run{
		Timestamp:  time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
		Commit:     "abc1234",
		PromptHash: "deadbeefcafe",
		Results: []Result{
			{CaseID: "r1", ExpectedSkill: "advisor", ExpectedAction: "advice", ActualSkill: "advisor", ActualAction: "advice", Pass: true, Tags: []string{"future"}},
			{CaseID: "r2", ExpectedSkill: "budget", ExpectedAction: "add_expense", ActualSkill: "advisor", Pass: false, Tags: []string{"past"}, Error: ""},
		},
		Summary: Summarize([]Result{
			{CaseID: "r1", ExpectedSkill: "advisor", Pass: true, Tags: []string{"future"}},
			{CaseID: "r2", ExpectedSkill: "budget", Pass: false, Tags: []string{"past"}},
		}),
	}
	var buf bytes.Buffer
	if err := WriteRun(&buf, r); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRun(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != r.Commit || got.PromptHash != r.PromptHash {
		t.Errorf("meta lost: %+v", got)
	}
	if len(got.Results) != 2 || got.Results[0].CaseID != "r1" {
		t.Errorf("results lost: %+v", got.Results)
	}
	if got.Summary.Total != 2 || got.Summary.Pass != 1 {
		t.Errorf("summary lost: %+v", got.Summary)
	}
}

func TestWriteRun_NoResults(t *testing.T) {
	r := Run{
		Timestamp:  time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
		Commit:     "deadbee",
		PromptHash: "0123456789ab",
		Summary:    Summarize(nil),
	}
	var buf bytes.Buffer
	if err := WriteRun(&buf, r); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRun(path)
	if err != nil {
		t.Fatalf("read empty-results run: %v", err)
	}
	if len(got.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(got.Results))
	}
	if got.Commit != "deadbee" || got.PromptHash != "0123456789ab" {
		t.Errorf("meta lost: %+v", got)
	}
}

func TestDiffRuns_EmptyInputs(t *testing.T) {
	d := DiffRuns(Run{}, Run{})
	if len(d.Regressed) != 0 || len(d.Improved) != 0 {
		t.Errorf("empty diff should be empty: %+v", d)
	}
}

func TestReadRun_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRun(path); err == nil {
		t.Error("expected error on empty run file")
	}
}
