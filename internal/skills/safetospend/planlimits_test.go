package safetospend

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/botlimits"
	"simpleAI/internal/budget"
)

type fakeLimits struct {
	snap  botlimits.Snapshot
	ok    bool
	err   error
	calls []string
}

func (f *fakeLimits) Snapshot(_ context.Context, chatID int64, cur string) (botlimits.Snapshot, bool, error) {
	f.calls = append(f.calls, cur)
	return f.snap, f.ok, f.err
}

func perDay(n int64) *int64 { return &n }

func planSnap(cur string) botlimits.Snapshot {
	return botlimits.Snapshot{
		Cur: cur, WindowStart: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), WindowEnd: time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
		DaysLeft: 9, CanSpend: 80000, PerDay: perDay(8888), LimitHold: 13000,
		Limits: []botlimits.Limit{
			{Category: "Еда", Amount: 20000, Spent: 5000, Planned: 2000, Left: 13000},
			{Category: "Такси", Amount: 1000, Spent: 1300, Over: 300},
		},
	}
}

// oldEnvelope — конверт с одной долей «Еда» лимитом 5000 ฿ и расходом 0: старый путь отвечает из долей.
func oldEnvelope() futureEnvelopeStore {
	start := time.Now().AddDate(0, 0, -1)
	return futureEnvelopeStore{
		env: &budget.Envelope{ID: uuid.New(), IncomeAmount: 100000, IncomeCurrency: "RUB", PeriodStart: start, PeriodEnd: start.AddDate(0, 0, 14)},
		shares: []budget.EnvelopeShare{
			{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5000, Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		},
	}
}

func skillWith(st interface {
	store
}, l *fakeLimits, mode LimitsMode, logs *bytes.Buffer) *SafeToSpendSkill {
	lg := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := NewSafeToSpendSkill(st, fakeLLM{}, lg)
	if l != nil {
		s.WithLimits(l, mode)
	}
	return s
}

func chatCtx() context.Context {
	return context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
}

const sharesQuestion = `{"question":"сколько осталось в конвертах","display_currency":"THB"}`

// FromPlan: ответ строится из чисел плана; старый путь (конверт) не нужен и не читается.
//
// Мутации: не вызывать planFirst в runShares; печатать Left вместо Left−Over; не печатать «На день» из PerDay;
// игнорировать флаг FromPlan — проверки краснеют.
func TestRunShares_FromPlanUsesPlanNumbers(t *testing.T) {
	var logs bytes.Buffer
	l := &fakeLimits{snap: planSnap("THB"), ok: true}
	out, err := skillWith(fakeStore{}, l, LimitsMode{FromPlan: true}, &logs).Run(chatCtx(), sharesQuestion)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"05.03 — 18.03 · осталось 9 дней", "Еда", "5 000 ฿", "13 000 ฿", "Такси", "-300 ฿", "**На день: 8 888 ฿**",
		"Свободно до конца окна: 80 000 ฿ (ещё 13 000 ฿ отложено на лимиты категорий)", "Пробито: Такси на 300 ฿"} {
		if !strings.Contains(out, want) {
			t.Errorf("в ответе нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Активного конверта нет") {
		t.Errorf("ответ из старого пути:\n%s", out)
	}
	if len(l.calls) != 1 || l.calls[0] != "THB" {
		t.Fatalf("валюта запроса к плану: %v, ждали [THB]", l.calls)
	}
	// RUB-показ просит план в рублях
	l2 := &fakeLimits{snap: planSnap("RUB"), ok: true}
	out2, err := skillWith(fakeStore{}, l2, LimitsMode{FromPlan: true}, &logs).Run(chatCtx(), `{"question":"сколько осталось в конвертах в рублях"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(l2.calls) != 1 || l2.calls[0] != "RUB" || !strings.Contains(out2, "₽") {
		t.Fatalf("RUB: calls=%v\n%s", l2.calls, out2)
	}
}

// План не отвечает (вне области, нет окна, ошибка) — старый путь, а не «лимитов нет» и не ошибка.
func TestRunShares_PlanUnavailableFallsBackToOldPath(t *testing.T) {
	for name, l := range map[string]*fakeLimits{
		"вне области": {ok: false},
		"ошибка":      {snap: planSnap("THB"), ok: true, err: errors.New("boom")},
	} {
		var logs bytes.Buffer
		out, err := skillWith(oldEnvelope(), l, LimitsMode{FromPlan: true}, &logs).Run(chatCtx(), sharesQuestion)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Что осталось") || strings.Contains(out, "Свободно до конца окна") {
			t.Errorf("%s: ответ не из старого пути:\n%s", name, out)
		}
	}
}

// Флаг выключен: ответ тот же, что без плана; shadow пишет расхождение в лог и не меняет ответ.
//
// Мутации: отдавать ответ плана при выключенном флаге; не вызывать shadowShares; логировать при выключенном shadow.
func TestRunShares_ShadowDoesNotChangeReply(t *testing.T) {
	var base bytes.Buffer
	want, err := skillWith(oldEnvelope(), nil, LimitsMode{}, &base).Run(chatCtx(), sharesQuestion)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	l := &fakeLimits{snap: planSnap("THB"), ok: true}
	st := oldEnvelope()
	got, err := skillWith(st, l, LimitsMode{Shadow: true}, &logs).Run(chatCtx(), sharesQuestion)
	if err != nil {
		t.Fatal(err)
	}
	// «Курс … на dd.mm» и период зависят от времени — сравниваем числа и строки таблицы, они у обоих запусков одни.
	if !strings.Contains(got, "Что осталось") || strings.Contains(got, "Свободно до конца окна") {
		t.Fatalf("shadow изменил ответ:\n%s", got)
	}
	if strings.Split(got, "\n")[2] != strings.Split(want, "\n")[2] {
		t.Errorf("шапка ответа отличается:\n%s\nvs\n%s", got, want)
	}
	if len(l.calls) != 1 {
		t.Fatalf("shadow не обратился к плану: %v", l.calls)
	}
	if !strings.Contains(logs.String(), "bot_limits_shadow") || !strings.Contains(logs.String(), "category=Еда") {
		t.Fatalf("расхождение не в логе:\n%s", logs.String())
	}
	// shadow и FromPlan выключены — план не трогаем
	l3 := &fakeLimits{snap: planSnap("THB"), ok: true}
	if _, err := skillWith(oldEnvelope(), l3, LimitsMode{}, &logs).Run(chatCtx(), sharesQuestion); err != nil || len(l3.calls) != 0 {
		t.Fatalf("план вызван при выключенных флагах: %v err=%v", l3.calls, err)
	}
}

// Ошибка плана в shadow не ломает ответ.
func TestRunShares_ShadowPlanErrorIsHarmless(t *testing.T) {
	var logs bytes.Buffer
	l := &fakeLimits{snap: planSnap("THB"), ok: true, err: errors.New("boom")}
	out, err := skillWith(oldEnvelope(), l, LimitsMode{Shadow: true}, &logs).Run(chatCtx(), sharesQuestion)
	if err != nil || !strings.Contains(out, "Что осталось") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// Режим общего остатка: FromPlan отвечает «Свободно» плана; без конверта это не «активного конверта нет».
func TestRunRemaining_FromPlan(t *testing.T) {
	var logs bytes.Buffer
	l := &fakeLimits{snap: planSnap("RUB"), ok: true}
	out, err := skillWith(fakeStore{}, l, LimitsMode{FromPlan: true}, &logs).Run(chatCtx(), `{"question":"сколько свободно осталось"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Свободно до конца окна: 80 000 ₽") || !strings.Contains(out, "На день: 8 888 ₽") || strings.Contains(out, "Активного конверта нет") {
		t.Fatalf("ответ:\n%s", out)
	}
	if len(l.calls) != 1 || l.calls[0] != "RUB" {
		t.Fatalf("calls=%v", l.calls)
	}
}

func TestShadowDiff(t *testing.T) {
	items := []ShareRemaining{
		{Name: "Еда", Kind: budget.ShareKindSpend, Remaining: 13000},    // совпадает с планом
		{Name: "Такси", Kind: budget.ShareKindSpend, Remaining: -250},   // план: −300
		{Name: "Подарки", Kind: budget.ShareKindSpend, Remaining: 1000}, // в плане лимита нет — не сравниваем
	}
	d := shadowDiff(items, 8000, planSnap("THB"))
	if len(d) != 2 || d[0].Category != "Такси" || d[0].Old != -250 || d[0].New != -300 || d[1].Category != "" || d[1].Old != 8000 || d[1].New != 8888 {
		t.Fatalf("diff=%+v", d)
	}
	if d := shadowDiff(items[:1], 8888, planSnap("THB")); len(d) != 0 {
		t.Fatalf("совпало, а diff=%+v", d)
	}
}

func TestFormatPlanLimits_NoLimitsAndNoPerDay(t *testing.T) {
	snap := planSnap("THB")
	snap.Limits, snap.LimitHold, snap.PerDay = nil, 0, nil
	out := formatPlanLimits(snap, NewDisplay("THB", 2.5), time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC))
	for _, want := range []string{"Лимитов на категории в этом окне нет", "На день: —", "Свободно до конца окна: 80 000 ฿"} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "отложено на лимиты") {
		t.Errorf("при нулевых лимитах не пишем про отложенное:\n%s", out)
	}
}

// Shadow общего остатка: старый «Свободно осталось» против CanSpend плана, ответ не меняется.
func TestRunRemaining_ShadowLogsAndKeepsOldReply(t *testing.T) {
	var logs bytes.Buffer
	l := &fakeLimits{snap: planSnap("RUB"), ok: true}
	out, err := skillWith(oldEnvelope(), l, LimitsMode{Shadow: true}, &logs).Run(chatCtx(), `{"question":"сколько свободно осталось"}`)
	if err != nil || !strings.Contains(out, "Свободно осталось") || strings.Contains(out, "Свободно до конца окна") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if !strings.Contains(logs.String(), "mode=remaining") || !strings.Contains(logs.String(), "plan_can_spend_rub=80000") || !strings.Contains(logs.String(), "old_rub=") {
		t.Fatalf("shadow-лог:\n%s", logs.String())
	}
}

// Старый путь без конверта не зовёт к снятому start_envelope («разложи по конвертам», «запомни приход»).
//
// Мутация: вернуть подсказки про раскладку в remaining.go — тест краснеет.
func TestOldPath_NoEnvelopeReplyDoesNotAdvertiseStartEnvelope(t *testing.T) {
	var logs bytes.Buffer
	for _, q := range []string{`{"question":"сколько осталось в конвертах"}`, `{"question":"сколько свободно осталось"}`} {
		out, err := skillWith(fakeStore{}, nil, LimitsMode{}, &logs).Run(chatCtx(), q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Активного конверта нет") || strings.Contains(out, "разложи по конвертам") || strings.Contains(out, "запомни приход") {
			t.Errorf("%s: %q", q, out)
		}
	}
}

func TestFormatPlanDigest(t *testing.T) {
	snap := planSnap("THB")
	out := FormatPlanDigest(snap)
	for _, want := range []string{"05.03 — 18.03 · осталось 9 дней", "**На день: 8 888 ฿**", "Свободно до конца окна: 80 000 ฿",
		"Лимиты:", "• Еда — осталось 13 000 ฿ из 20 000 ฿", "• Такси — пробито на 300 ฿ (лимит 1 000 ฿)"} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q:\n%s", want, out)
		}
	}
	snap.Limits = nil
	if out := FormatPlanDigest(snap); strings.Contains(out, "Лимиты:") {
		t.Errorf("пустой блок лимитов:\n%s", out)
	}
	snap.Cur = "RUB"
	if out := FormatPlanDigest(snap); !strings.Contains(out, "80 000 ₽") {
		t.Errorf("валюта сводки не из снимка:\n%s", out)
	}
}
