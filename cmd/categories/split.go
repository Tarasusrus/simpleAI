package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	"simpleAI/internal/adapters/llm/openai"
	"simpleAI/internal/budget"
	"simpleAI/internal/categories"
	"simpleAI/internal/db"
	"simpleAI/internal/plan"
	"simpleAI/internal/planrepo"
)

// runSplit — «categories split»: разнесение истории легаси-категории (ADR-013 §4, этап 2).
//
//	categories split                      — dry-run: ничего не пишет, печатает таблицу для владельца
//	categories split --apply              — перенос строк одной транзакцией + журнал (split_id)
//	categories split --rollback-split ID  — откат по журналу
//	categories split --retire             — убрать пустую «Еду» (слияние в «Продукты», «еда» → алиас)
//
// База: CATEGORIES_DATABASE_URL (например реплика для dry-run), иначе POSTGRES_* из окружения. Классификатор —
// CATEGORY_LLM_* (как у бота); в запрос уходят только описания и две цели, без сумм и дат.
func runSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	source := fs.String("source", "Еда", "категория-источник (должна быть в category_split_target)")
	apply := fs.Bool("apply", false, "перенести строки (без флага — только таблица)")
	rollback := fs.String("rollback-split", "", "split_id для отката")
	planPath := fs.String("plan-file", "split-plan.json", "файл плана: dry-run его пишет, --apply применяет именно его")
	retire := fs.Bool("retire", false, "убрать пустой источник слиянием в цель по умолчанию")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	write := *apply || *rollback != "" || *retire
	if write && os.Getenv("CATEGORIES_DATABASE_URL") == "" {
		if err := db.Migrate(ctx, cfg.DB); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	pool, err := newPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := categories.NewStore(pool)

	if *rollback != "" {
		id, err := uuid.Parse(*rollback)
		if err != nil {
			return fmt.Errorf("split_id: %w", err)
		}
		n, err := st.RollbackSplit(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("откат выполнен: %s, строк возвращено %d\n", id, n)
		return nil
	}

	src, targets, err := st.SplitSource(ctx, *source)
	if err != nil {
		return err
	}
	if *retire {
		mr, err := st.RetireSource(ctx, src, targets[0].Category)
		if err != nil {
			return err
		}
		fmt.Printf("«%s» убрана слиянием в «%s», merge_id=%s (откат: categories --rollback %s)\n", src.Name, targets[0].Name, mr.MergeID, mr.MergeID)
		return nil
	}

	if *apply {
		// Применяется ровно тот план, что владелец видел в dry-run: классификатор заново не зовётся.
		data, err := os.ReadFile(*planPath)
		if err != nil {
			return fmt.Errorf("нет файла плана %s: сначала dry-run (categories split), утверди таблицу, потом --apply: %w", *planPath, err)
		}
		p, err := st.LoadSplitPlan(ctx, data, *source)
		if err != nil {
			return err
		}
		res, err := st.ApplySplit(ctx, p)
		if err != nil {
			return err
		}
		fmt.Printf("ПРИМЕНЕНО: split_id=%s, перенесено %v (строк в плане %d)\nОткат: categories split --rollback-split %s\n", res.SplitID, res.Moved, len(p.Rows), res.SplitID)
		return nil
	}

	rows, err := st.SplitRows(ctx, src.ID)
	if err != nil {
		return err
	}
	clf, err := buildClassifier(cfg, len(rows))
	if err != nil {
		return err
	}
	p, err := categories.PlanSplit(ctx, src, targets, rows, clf, categories.DefaultHints)
	if err != nil {
		return err
	}
	var report strings.Builder
	if err := fillSplitReport(ctx, &report, st, pool, cfg, p); err != nil {
		return err
	}
	if _, err := fmt.Fprint(os.Stdout, report.String()); err != nil {
		return err
	}
	data, err := p.Marshal(time.Now())
	if err != nil {
		return err
	}
	if err := os.WriteFile(*planPath, data, 0o600); err != nil {
		return fmt.Errorf("запись плана: %w", err)
	}
	fmt.Printf("\nДРАЙ-РАН: в базу ничего не записано. План сохранён в %s. Применить: categories split --apply --plan-file %s (после ответа владельца на таблицу).\n", *planPath, *planPath)
	return nil
}

func newPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	if u := os.Getenv("CATEGORIES_DATABASE_URL"); u != "" {
		return pgxpool.New(ctx, u)
	}
	return db.NewPool(ctx, cfg.DB)
}

// buildClassifier — тот же клиент, что у бота; потолок вызовов — число строк (больше уникальных описаний не бывает).
func buildClassifier(cfg config.Config, rows int) (categories.Classifier, error) {
	c := cfg.Classifier
	if c.BaseURL == "" || c.APIKey == "" {
		return nil, fmt.Errorf("классификатор не настроен: нужны CATEGORY_LLM_BASE_URL и CATEGORY_LLM_API_KEY (без него всё уйдёт в «по умолчанию»)")
	}
	client, err := openai.NewClassifierClient(c.BaseURL, c.APIKey, c.Model, c.Timeout)
	if err != nil {
		return nil, err
	}
	return categories.NewGuardedClassifier(client, categories.GuardConfig{MaxCallsPerDay: rows + 1, BreakerFailures: rows + 1}), nil
}

func fmtCur(sums map[string]float64) string {
	if len(sums) == 0 {
		return "—"
	}
	cur := make([]string, 0, len(sums))
	for c := range sums {
		cur = append(cur, c)
	}
	sort.Strings(cur)
	parts := make([]string, 0, len(cur))
	for _, c := range cur {
		parts = append(parts, fmt.Sprintf("%.0f %s", sums[c], c))
	}
	return strings.Join(parts, " + ")
}

func fillSplitReport(ctx context.Context, w *strings.Builder, st *categories.Store, pool *pgxpool.Pool, cfg config.Config, p categories.SplitPlan) error {
	var noDesc, ambiguous, failed, classified int
	moved := map[string]int{}
	for _, r := range p.Rows {
		moved[r.Row.Table+"→"+r.Target.Name]++
		switch r.Basis {
		case categories.BasisNoDesc:
			noDesc++
		case categories.BasisAmbiguous:
			ambiguous++
		case categories.BasisError:
			failed++
		default:
			classified++
		}
	}
	fmt.Fprintf(w, "# Разнесение «%s» → %s (ДРАЙ-РАН)\n\n", p.Source.Name, joinTargets(p.Targets))
	fmt.Fprintf(w, "Строк в источнике: %d. Классификатором выбрано: %d. Без описания → «%s»: %d. Неоднозначных (→ «%s», на просмотр): %d. Сбой классификатора: %d.\n\n",
		len(p.Rows), classified, p.Default.Name, noDesc, p.Default.Name, ambiguous, failed)

	// Агрегаты до и после по категориям и месяцам (транзакции, суммы по валютам)
	type key struct{ month, cat string }
	before := map[key]map[string]float64{}
	after := map[key]map[string]float64{}
	add := func(m map[key]map[string]float64, k key, cur string, v float64) {
		if m[k] == nil {
			m[k] = map[string]float64{}
		}
		m[k][cur] += v
	}
	months := map[string]bool{}
	cats := []string{p.Source.Name}
	for _, t := range p.Targets {
		cats = append(cats, t.Name)
		existing, err := st.SplitRows(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, r := range existing {
			if r.Table != "budget_transaction" {
				continue
			}
			k := key{r.Date.Format("2006-01"), t.Name}
			months[k.month] = true
			add(before, k, r.Currency, r.Amount)
			add(after, k, r.Currency, r.Amount)
		}
	}
	for _, r := range p.Rows {
		if r.Row.Table != "budget_transaction" {
			continue
		}
		m := r.Row.Date.Format("2006-01")
		months[m] = true
		add(before, key{m, p.Source.Name}, r.Row.Currency, r.Row.Amount)
		add(after, key{m, r.Target.Name}, r.Row.Currency, r.Row.Amount)
	}
	ms := make([]string, 0, len(months))
	for m := range months {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	fmt.Fprintf(w, "## Траты по месяцам: до → после\n\n| Месяц |")
	for _, c := range cats {
		fmt.Fprintf(w, " %s до | %s после |", c, c)
	}
	fmt.Fprintf(w, "\n|---|%s\n", strings.Repeat("---|---|", len(cats)))
	for _, m := range ms {
		fmt.Fprintf(w, "| %s |", m)
		for _, c := range cats {
			fmt.Fprintf(w, " %s | %s |", fmtCur(before[key{m, c}]), fmtCur(after[key{m, c}]))
		}
		w.WriteString("\n")
	}

	fmt.Fprintf(w, "\n## Куда уходят строки\n\n| Таблица → цель | Строк |\n|---|---|\n")
	keys := make([]string, 0, len(moved))
	for k := range moved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "| %s | %d |\n", k, moved[k])
	}

	fmt.Fprintf(w, "\n## Уникальные описания и выбор\n\n| Описание | Строк | Цель | Основание | Уверенность | Комментарий модели |\n|---|---|---|---|---|---|\n")
	for _, c := range p.Choices {
		desc := c.Description
		if desc == "" {
			desc = "(без описания)"
		}
		fmt.Fprintf(w, "| %s | %d | %s | %s | %.2f | %s |\n", strings.ReplaceAll(desc, "|", "/"), c.Rows, c.Target.Name, c.Basis, c.Confidence, strings.ReplaceAll(c.Reason, "|", "/"))
	}

	return fillPlanEffect(ctx, w, pool, cfg, p)
}

func joinTargets(ts []categories.SplitTarget) string {
	n := make([]string, 0, len(ts))
	for _, t := range ts {
		n = append(n, "«"+t.Name+"»")
	}
	return strings.Join(n, " | ")
}

// overrideReader отдаёт снимок плана с переразнесёнными категориями строк: «после» без записи в базу.
type overrideReader struct{ snap plan.Snapshot }

func (o overrideReader) Read(context.Context, plan.Scope) (plan.Snapshot, error) { return o.snap, nil }

// remapped возвращает копию снимка, где события и правила строк плана получили категории целей.
func remapped(s plan.Snapshot, p categories.SplitPlan) plan.Snapshot {
	by := map[string]string{} // ключ события / id правила → имя цели
	for _, r := range p.Rows {
		switch r.Row.Table {
		case "budget_transaction":
			by[plan.TransactionKey(r.Row.ID.String())] = r.Target.Name
		case "budget_planned_expense":
			by["planned:"+r.Row.ID.String()] = r.Target.Name
		case "budget_recurring":
			by["rule:"+r.Row.ID.String()] = r.Target.Name
		}
	}
	out := s
	out.Payments = append([]plan.Event(nil), s.Payments...)
	for i, e := range out.Payments {
		if t, ok := by[e.Key]; ok && plan.CategoryKey(e.Category) == plan.CategoryKey(p.Source.Name) {
			out.Payments[i].Category = t
		}
	}
	out.Rules = append([]plan.RuleWithExceptions(nil), s.Rules...)
	for i, r := range out.Rules {
		if t, ok := by["rule:"+r.ID]; ok {
			out.Rules[i].Category = t
		}
	}
	return out
}

// printPlanEffect — «Свободно», лимиты и «Как обычно тратишь» до и после (ADR-013 §4, ADR-011 §3): тот же расчёт плана.
func fillPlanEffect(ctx context.Context, w *strings.Builder, pool *pgxpool.Pool, cfg config.Config, p categories.SplitPlan) error {
	chats := config.ParseChatIDs(os.Getenv("PLAN_CHAT_IDS"))
	if len(chats) == 0 {
		chats = cfg.Telegram.AllowedChats
	}
	if len(chats) == 0 {
		fmt.Fprintf(w, "\n## Влияние на план\n\nПропущено: не задан PLAN_CHAT_IDS / TELEGRAM_ALLOWED_CHATS.\n")
		return nil
	}
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return err
	}
	store := budget.NewStore(pool)
	snap, err := planrepo.NewReader(store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithLimits(store).Read(ctx, plan.Scope{ChatIDs: chats})
	if err != nil {
		return fmt.Errorf("чтение плана: %w", err)
	}
	today := func() plan.Date { return plan.DateOf(time.Now(), loc) }
	view := func(s plan.Snapshot) (plan.Result, error) {
		return plan.NewService(overrideReader{s}, nil, today, plan.Scope{ChatIDs: chats}).View(ctx, plan.CurRUB)
	}
	b, err := view(snap)
	if err != nil {
		return fmt.Errorf("план до: %w", err)
	}
	a, err := view(remapped(snap, p))
	if err != nil {
		return fmt.Errorf("план после: %w", err)
	}
	fmt.Fprintf(w, "\n## Влияние на план (в ₽, окно как на экране)\n\n| Показатель | До | После |\n|---|---|---|\n")
	fmt.Fprintf(w, "| Свободно (CanSpend) | %d | %d |\n| Потрачено в окне | %d | %d |\n| Потрачено вне плана | %d | %d |\n| Отложено на лимиты | %d | %d |\n",
		b.View.CanSpend, a.View.CanSpend, b.View.Spent, a.View.Spent, b.View.UnplannedSpent, a.View.UnplannedSpent, b.View.LimitHold, a.View.LimitHold)
	fmt.Fprintf(w, "\n### Лимиты\n\n| Категория | Лимит | Потрачено до → после | Остаток до → после | Перерасход до → после |\n|---|---|---|---|---|\n")
	after := map[string]plan.LimitView{}
	for _, l := range a.View.Limits {
		after[plan.CategoryKey(l.Category)] = l
	}
	for _, l := range b.View.Limits {
		x := after[plan.CategoryKey(l.Category)]
		fmt.Fprintf(w, "| %s | %d | %d → %d | %d → %d | %d → %d |\n", l.Category, l.Amount, l.Spent, x.Spent, l.Left, x.Left, l.Over, x.Over)
	}
	fmt.Fprintf(w, "\n### «Как обычно тратишь» (за %d дн., на окно)\n\n| Категория | Обычно до → после | Сейчас до → после |\n|---|---|---|\n", b.View.AvgDays)
	names := map[string]bool{}
	for _, t := range p.Targets {
		names[plan.CategoryKey(t.Name)] = true
	}
	names[plan.CategoryKey(p.Source.Name)] = true
	rowsAfter := map[string]plan.CategoryRow{}
	for _, c := range a.View.Categories {
		rowsAfter[plan.CategoryKey(c.Name)] = c
	}
	seen := map[string]bool{}
	row := func(c plan.CategoryRow, x plan.CategoryRow) {
		fmt.Fprintf(w, "| %s | %d → %d | %d → %d |\n", c.Name, c.Avg, x.Avg, c.Now, x.Now)
	}
	for _, c := range b.View.Categories {
		k := plan.CategoryKey(c.Name)
		if names[k] {
			row(c, rowsAfter[k])
			seen[k] = true
		}
	}
	for _, c := range a.View.Categories {
		if k := plan.CategoryKey(c.Name); names[k] && !seen[k] {
			row(plan.CategoryRow{Name: c.Name}, c)
		}
	}
	return nil
}
