// Command categories — eval классификатора категорий на реальном маршруте LiteLLM (ADR-003, ADR-012).
//
// Usage:
//
//	CATEGORY_LLM_BASE_URL=http://127.0.0.1:4444/v1 CATEGORY_LLM_API_KEY=... go run ./evals/cmd/categories
//	go run ./evals/cmd/categories -min-accuracy 0.8 -max-wrong 0.1
//
// Это не мок: описания уходят на маршрут standard. Код возврата 1, если точность ниже порога или
// доля неверных выше.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/evals/classifier"
	"simpleAI/internal/adapters/llm/openai"
	"simpleAI/internal/categories"
)

// canonical — категории расхода, как они лежат в проде после миграции 00026 (без «Еды»).
var canonical = []string{"Продукты", "Рестораны", "Транспорт", "Жильё", "Здоровье", "Красота", "Развлечения", "Подписки", "Одежда", "Переводы", "Прочее"}

func main() {
	input := flag.String("input", "evals/classifier_golden_set.jsonl", "golden-набор")
	out := flag.String("out", "evals/runs", "каталог для прогона")
	minAcc := flag.Float64("min-accuracy", 0.8, "порог точности")
	maxWrong := flag.Float64("max-wrong", 0.1, "порог доли неверных")
	timeout := flag.Duration("timeout", 25*time.Second, "таймаут одного вызова")
	pause := flag.Duration("pause", 3*time.Second, "пауза между вызовами (бесплатный маршрут режет по лимитам)")
	maxErrors := flag.Float64("max-errors", 0.3, "доля сбоев вызова, выше которой прогон недействителен")
	flag.Parse()

	cases, err := classifier.Load(*input)
	if err != nil {
		fatal(err)
	}
	client, err := openai.NewClassifierClient(os.Getenv("CATEGORY_LLM_BASE_URL"), os.Getenv("CATEGORY_LLM_API_KEY"), envOr("CATEGORY_LLM_MODEL", "standard"), *timeout)
	if err != nil {
		fatal(err)
	}
	cands := make([]categories.Candidate, 0, len(canonical))
	for _, n := range canonical {
		cands = append(cands, categories.Candidate{ID: uuid.New(), Name: n, Hint: categories.DefaultHints[strings.ToLower(n)]})
	}

	// Тот же GuardedClassifier, что в проде: чужой id и плохие имена отсекаются так же, как у бота.
	guarded := categories.NewGuardedClassifier(&paced{inner: client, pause: *pause}, categories.GuardConfig{MaxCallsPerDay: len(cases) + 10, MaxCreatesPerDay: len(cases)})
	rep := classifier.Run(context.Background(), guarded, cases, cands)
	for _, r := range rep.Results {
		if r.Outcome != classifier.OK {
			fmt.Printf("%-9s %s %-40.40q ждали %-10s получили %-10s conf=%.2f %s\n", r.Outcome, r.CaseID, r.Input, r.Expected, r.Actual, r.Conf, r.Err)
		}
	}
	fmt.Printf("\nкейсов %d, ответили %d, сбоев вызова %d (%.0f%%)\nсреди ответивших: верно %d (%.0f%%), неверно %d (%.0f%%), не определил %d (%.0f%%)\n",
		rep.Total, rep.Answered(), rep.Errored, 100*rep.ErrorRate(), rep.OK, 100*rep.Accuracy(), rep.Wrong, 100*rep.WrongRate(), rep.Degraded, 100*rep.DegradedRate())

	if err := os.MkdirAll(*out, 0o755); err == nil {
		path := filepath.Join(*out, time.Now().UTC().Format("20060102T150405Z")+"_classifier.jsonl")
		if f, err := os.Create(path); err == nil {
			enc := json.NewEncoder(f)
			for _, r := range rep.Results {
				_ = enc.Encode(r) //nolint:errcheck // отчёт best-effort
			}
			_ = f.Close() //nolint:errcheck // отчёт best-effort
			fmt.Println("прогон:", path)
		}
	}
	if rep.ErrorRate() > *maxErrors {
		fmt.Printf("прогон недействителен: сбоев вызова %.0f%% (макс %.0f%%); повтори позже или проверь лимиты маршрута\n", 100*rep.ErrorRate(), 100**maxErrors)
		os.Exit(3)
	}
	if rep.Accuracy() < *minAcc || rep.WrongRate() > *maxWrong {
		fmt.Printf("ниже порога: точность %.2f (мин %.2f), неверных %.2f (макс %.2f)\n", rep.Accuracy(), *minAcc, rep.WrongRate(), *maxWrong)
		os.Exit(1)
	}
}

// paced выдерживает паузу перед каждым вызовом: бесплатные провайдеры ограничивают частоту.
type paced struct {
	inner categories.Classifier
	pause time.Duration
	n     int
}

func (p *paced) Classify(ctx context.Context, d string, c []categories.Candidate) (categories.Classification, error) {
	if p.n > 0 {
		time.Sleep(p.pause)
	}
	p.n++
	return p.inner.Classify(ctx, d, c)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(2)
}
