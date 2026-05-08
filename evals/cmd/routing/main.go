// Command routing — eval-харнес для проверки routing-решений LLM.
//
// Прогон: для каждого кейса в golden_set.jsonl строит system prompt
// (тот же что в проде), делает один вызов LLM, парсит первый tool call,
// сравнивает с expected. Skills НЕ выполняются — оцениваем только routing.
//
// Usage:
//
//	go run ./evals/cmd/routing -input evals/golden_set.jsonl -out evals/runs
//	go run ./evals/cmd/routing -tag future_purchase -limit 5
//	go run ./evals/cmd/routing -prev evals/runs/baseline-2026-05-06.jsonl
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	"simpleAI/internal/agent"
	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
	advisorskill "simpleAI/internal/skills/advisor"
	budgetskill "simpleAI/internal/skills/budget"

	"simpleAI/evals"
)

const (
	caseTimeout    = 60 * time.Second
	promptHashLen  = 12
)

func main() {
	var (
		inputPath  = flag.String("input", "evals/golden_set.jsonl", "путь к golden_set.jsonl")
		outDir     = flag.String("out", "evals/runs", "каталог куда писать run-файлы")
		tagFilter  = flag.String("tag", "", "фильтр по тегу (пусто = без фильтра)")
		limit      = flag.Int("limit", 0, "smoke: ограничить N кейсами (0 = все)")
		prevPath   = flag.String("prev", "", "путь к предыдущему run для diff (пусто = без diff)")
		dryRun     = flag.Bool("dry-run", false, "не вызывать LLM, проверить только загрузку и схему")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cases, err := evals.LoadCases(*inputPath)
	if err != nil {
		fatal(logger, "load cases", err)
	}
	cases = evals.FilterByTag(cases, *tagFilter)
	if *limit > 0 && *limit < len(cases) {
		cases = cases[:*limit]
	}

	if len(cases) == 0 {
		fmt.Println("accuracy=N/A (0 кейсов)")
		return
	}

	manifests := buildManifests()
	systemPrompt := agent.BuildToolsSystemPrompt(manifests)
	promptHash := hashPrompt(systemPrompt)

	if *dryRun {
		fmt.Printf("dry-run: %d кейсов, prompt_hash=%s\n", len(cases), promptHash)
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		fatal(logger, "load config", err)
	}
	llm, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		fatal(logger, "init llm", err)
	}

	results := runCases(llm, systemPrompt, cases)

	summary := evals.Summarize(results)
	run := evals.Run{
		Timestamp:  time.Now().UTC(),
		Commit:     gitCommitShort(),
		PromptHash: promptHash,
		Results:    results,
		Summary:    summary,
	}

	outPath, err := writeRun(*outDir, run)
	if err != nil {
		fatal(logger, "write run", err)
	}

	printSummary(summary)
	fmt.Printf("\nrun: %s\n", outPath)

	if *prevPath != "" {
		prev, err := evals.ReadRun(*prevPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: cannot read prev run %s: %v\n", *prevPath, err)
		} else {
			diff := evals.DiffRuns(prev, run)
			printDiff(diff, *prevPath)
		}
	}

	if summary.Fail > 0 {
		os.Exit(1)
	}
}

// buildManifests возвращает Manifest всех skills как в проде.
// Skill-конструкторы вызываются с nil зависимостями — Manifest не дереференсит их.
func buildManifests() []plugin.Manifest {
	return []plugin.Manifest{
		budgetskill.NewBudgetSkill(nil).Manifest(),
		advisorskill.NewAdvisorSkill(nil, nil, nil).Manifest(),
	}
}

func runCases(llm core.LLMClient, systemPrompt string, cases []evals.Case) []evals.Result {
	results := make([]evals.Result, 0, len(cases))
	for i, c := range cases {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s ... ", i+1, len(cases), c.ID)
		res := evals.Result{
			CaseID:         c.ID,
			ExpectedSkill:  c.Expected.Skill,
			ExpectedAction: c.Expected.Action,
			Tags:           c.Tags,
		}
		ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
		resp, err := llm.AskWithSystem(ctx, systemPrompt, c.Input)
		cancel()
		if err != nil {
			res.Error = err.Error()
			fmt.Fprintln(os.Stderr, "ERR:", err)
			results = append(results, res)
			continue
		}
		res.RawResponse = resp
		calls := agent.ParseCalls(resp)
		if len(calls) > 0 {
			res.ActualSkill = calls[0].Skill
			res.ActualAction = calls[0].Action
		}
		res.Pass = res.ActualSkill == res.ExpectedSkill && res.ActualAction == res.ExpectedAction
		if res.Pass {
			fmt.Fprintln(os.Stderr, "PASS")
		} else {
			fmt.Fprintf(os.Stderr, "FAIL (got %s/%s, want %s/%s)\n",
				res.ActualSkill, res.ActualAction, res.ExpectedSkill, res.ExpectedAction)
		}
		results = append(results, res)
	}
	return results
}

func writeRun(dir string, r evals.Run) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	hashFragment := r.PromptHash
	if len(hashFragment) > promptHashLen {
		hashFragment = hashFragment[:promptHashLen]
	}
	name := fmt.Sprintf("%s_%s.jsonl", r.Timestamp.Format("20060102T150405Z"), hashFragment)
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	writeErr := evals.WriteRun(f, r)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}

func hashPrompt(p string) string {
	h := sha256.Sum256([]byte(p))
	return hex.EncodeToString(h[:])
}

func gitCommitShort() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func printSummary(s evals.Summary) {
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("total: %d  pass: %d  fail: %d  accuracy: %.2f\n", s.Total, s.Pass, s.Fail, s.Accuracy)
	if len(s.PerSkill) > 0 {
		fmt.Println("per_skill:")
		for k, v := range s.PerSkill {
			fmt.Printf("  %s: %.2f\n", k, v)
		}
	}
	if len(s.PerTag) > 0 {
		fmt.Println("per_tag:")
		for k, v := range s.PerTag {
			fmt.Printf("  %s: %.2f\n", k, v)
		}
	}
	if len(s.FailedIDs) > 0 {
		b, err := json.Marshal(s.FailedIDs)
		if err != nil {
			b = []byte("[]")
		}
		fmt.Printf("failed_ids: %s\n", b)
	}
}

func printDiff(d evals.Diff, prev string) {
	fmt.Printf("\n=== Diff vs %s ===\n", prev)
	fmt.Printf("regressed: %v\n", d.Regressed)
	fmt.Printf("improved:  %v\n", d.Improved)
}

func fatal(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "err", err)
	os.Exit(2)
}
