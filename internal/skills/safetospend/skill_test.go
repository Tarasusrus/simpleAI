package safetospend

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

type fakeStore struct{}

func (fakeStore) GetExchangeRates(context.Context) (map[string]float64, error) {
	return map[string]float64{"RUB": 1.0, "THB": 2.6}, nil
}
func (fakeStore) GetPeriodSnapshot(context.Context, int64, time.Time, time.Time, map[string]float64) (*budget.AdvisorSnapshot, error) {
	// recurring 20000 RUB → /2.6 в THB (snapshot хранит THB). Здесь сразу THB.
	return &budget.AdvisorSnapshot{
		UpcomingRecurring: 20000 / 2.6,
		ActiveDebtDue:     0,
		SpentByCategory:   map[string]float64{"Еда": 100},
	}, nil
}
func (fakeStore) GetForecastData(context.Context, int, map[string]float64) ([]budget.CategoryForecast, error) {
	return []budget.CategoryForecast{{CategoryName: "Еда", Currency: "RUB", ForecastAmount: 30000}}, nil
}
func (fakeStore) PlannedExpensesTHB(context.Context, int64, map[string]float64) (float64, int, error) {
	return 0, 0, nil
}
func (fakeStore) ListPlannedExpenses(context.Context, int64) ([]budget.PlannedExpense, error) {
	return nil, nil
}
func (fakeStore) GetActiveEnvelope(context.Context, int64) (*budget.Envelope, bool, error) {
	return nil, false, nil
}
func (fakeStore) ListShares(context.Context, int64, uuid.UUID) ([]budget.EnvelopeShare, error) {
	return nil, nil
}
func (fakeStore) SpentByCategoryExcludingRecurring(context.Context, time.Time, time.Time) ([]budget.CategorySpentRow, error) {
	return nil, nil
}

// fakeLLM возвращает заданную строку (для проверки, что числа от неё не зависят).
type fakeLLM struct{ resp string }

func (f fakeLLM) Ask(context.Context, string) (string, error)                   { return f.resp, nil }
func (f fakeLLM) AskWithSystem(context.Context, string, string) (string, error) { return f.resp, nil }

// numericLines извлекает строки с числовыми суммами (₽) — детерминированный вывод.
func numericLines(s string) []string {
	re := regexp.MustCompile(`(?m)^.*[-\d]+ ₽.*$`)
	return re.FindAllString(s, -1)
}

// TestNumbersIndependentOfLLM (ADR-007 §3): мутация ответа LLM НЕ меняет числа.
func TestNumbersIndependentOfLLM(t *testing.T) {
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
	payload := `{"amount":127000,"currency":"RUB","question":"сколько свободно?"}`

	s1 := NewSafeToSpendSkill(fakeStore{}, fakeLLM{resp: "Совет A"}, nil)
	out1, err := s1.Run(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	// Другой ответ LLM с фейковыми числами.
	s2 := NewSafeToSpendSkill(fakeStore{}, fakeLLM{resp: "Потратьте 999999 на ерунду"}, nil)
	out2, err := s2.Run(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}

	n1, n2 := numericLines(out1), numericLines(out2)
	if len(n1) == 0 {
		t.Fatal("нет числовых строк в ответе")
	}
	if len(n1) != len(n2) {
		t.Fatalf("число числовых строк изменилось от LLM: %d vs %d", len(n1), len(n2))
	}
	for i := range n1 {
		if n1[i] != n2[i] {
			t.Errorf("числовая строка зависит от LLM:\n  A: %q\n  B: %q", n1[i], n2[i])
		}
	}

	// И проверим само число: приход 127000 − recurring 20000 = 107000.
	// Разряды группируются пробелом — суммы печатаются через Display (simpleAI-302i).
	if !regexpContains(out1, `Остаётся до повседневных трат: 107 000 ₽`) {
		t.Errorf("ожидалось 'Остаётся до повседневных трат: 107 000 ₽', got:\n%s", out1)
	}
}

// Без суммы и без активного конверта — приглашение назвать приход/завести конверт.
func TestNoAmountNoEnvelope(t *testing.T) {
	s := NewSafeToSpendSkill(fakeStore{}, fakeLLM{}, nil)
	out, err := s.Run(context.Background(), `{"amount":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if !regexpContains(out, `Активного конверта нет`) {
		t.Errorf("ожидалось сообщение об отсутствии конверта, got: %q", out)
	}
}

func regexpContains(s, pat string) bool {
	return regexp.MustCompile(pat).MatchString(s)
}

// shareStore — конверт с раскладкой. Факт трат отдаётся ТОЛЬКО через
// SpentByCategoryExcludingRecurring; GetPeriodSnapshot подсовывает «грязный»
// факт с recurring-тратой, которого в остатке доли быть не должно.
type shareStore struct {
	fakeStore
	spentCalled bool
}

func (s *shareStore) GetActiveEnvelope(context.Context, int64) (*budget.Envelope, bool, error) {
	return &budget.Envelope{
		ID:             uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		IncomeAmount:   127000,
		IncomeCurrency: "RUB",
		PeriodStart:    time.Now().AddDate(0, 0, -7),
		PeriodEnd:      time.Now().AddDate(0, 0, 7),
	}, true, nil
}

func (s *shareStore) ListShares(context.Context, int64, uuid.UUID) ([]budget.EnvelopeShare, error) {
	return []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 10000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		{Name: "прочее", Kind: budget.ShareKindSpend, Allocated: 2600, Position: 1},
	}, nil
}

func (s *shareStore) SpentByCategoryExcludingRecurring(context.Context, time.Time, time.Time) ([]budget.CategorySpentRow, error) {
	s.spentCalled = true
	return []budget.CategorySpentRow{{CategoryName: "Еда", Currency: "THB", Amount: 1000}}, nil
}

// Режим конвертов берёт факт из источника, который отсекает recurring, а не из
// SpentByCategory снапшота (тот recurring не отделяет — был бы двойной учёт,
// ADR-008 §5). Мутация «взять факт из снапшота» роняет обе проверки.
func TestRunShares_UsesRecurringFreeSource(t *testing.T) {
	st := &shareStore{}
	s := NewSafeToSpendSkill(st, fakeLLM{}, nil)
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))

	out, err := s.Run(ctx, `{"question":"сколько осталось в конвертах?"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !st.spentCalled {
		t.Fatal("режим конвертов не спросил факт у SpentByCategoryExcludingRecurring")
	}
	// 10000 − 1000 = 9000 ฿ (валюту не просили — печатаем батами). Снапшотный
	// факт (100 THB по «Еда») в остаток попасть не должен.
	if !regexpContains(out, `Еда\s+1 000 ฿\s+9 000 ฿`) {
		t.Errorf("ожидался остаток «Еда» 9000 ฿ при потраченных 1000 ฿, got:\n%s", out)
	}
}

// Валюта конвертов по умолчанию — баты: ни одного рублёвого знака в ответе.
// Мутация «форматтер всегда печатает рубли» роняет этот тест.
func TestRunShares_DefaultCurrencyIsTHB(t *testing.T) {
	s := NewSafeToSpendSkill(&shareStore{}, fakeLLM{}, nil)
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))

	out, err := s.Run(ctx, `{"question":"сколько осталось в конвертах?"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(amountsOnly(out), "₽") {
		t.Errorf("без просьбы о рублях ответ обязан быть в батах, got:\n%s", out)
	}
}

// «Покажи конверты в рублях» — те же доли рублями по курсу конверта:
// 9000 ฿ × 2.6 = 23400 ₽ из 26000 ₽. Проверяются оба канала валюты — явное
// поле от LLM и разбор самой фразы (поле модель заполняет не всегда).
func TestRunShares_DisplayRUB(t *testing.T) {
	cases := map[string]string{
		"поле от LLM": `{"question":"покажи конверты","display_currency":"RUB"}`,
		"фраза":       `{"question":"покажи конверты в рублях"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewSafeToSpendSkill(&shareStore{}, fakeLLM{}, nil)
			ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
			out, err := s.Run(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if !regexpContains(out, `Еда\s+2 600 ₽\s+23 400 ₽`) {
				t.Errorf("ожидался остаток «Еда» 23400 ₽ при потраченных 2600 ₽, got:\n%s", out)
			}
			if strings.Contains(amountsOnly(out), "฿") {
				t.Errorf("просили рубли, а в ответе баты:\n%s", out)
			}
		})
	}
}

// «В батах» словами — тот же дефолт, но названный явно.
func TestRunShares_DisplayTHBByWords(t *testing.T) {
	s := NewSafeToSpendSkill(&shareStore{}, fakeLLM{}, nil)
	ctx := context.WithValue(context.Background(), agent.ChatIDKey{}, int64(1))
	out, err := s.Run(ctx, `{"question":"покажи конверты в батах"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !regexpContains(out, `Еда\s+1 000 ฿\s+9 000 ฿`) {
		t.Errorf("ожидался остаток «Еда» 9000 ฿ при потраченных 1000 ฿, got:\n%s", out)
	}
}

// amountsOnly отбрасывает строку курса: она печатает ОБА знака валют («3,1 ₽/฿»)
// по определению, и проверка «в ответе нет чужого знака» должна смотреть на
// суммы, а не на курс.
func amountsOnly(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Курс ") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// Вопрос без слова «конверт» ведёт в общий остаток, а не в раскладку.
func TestRunRemaining_NotHijackedBySharesMode(t *testing.T) {
	s := NewSafeToSpendSkill(fakeStore{}, fakeLLM{}, nil)
	out, err := s.Run(context.Background(), `{"question":"сколько свободно осталось?"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !regexpContains(out, `Активного конверта нет`) {
		t.Errorf("ожидался общий режим остатка, got: %q", out)
	}
}
