package receipt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"simpleAI/internal/core"
)

type fakeSeen struct {
	set      map[string]bool
	seenCall int
	released int
}

func newFakeSeen() *fakeSeen { return &fakeSeen{set: map[string]bool{}} }

func key(chat int64, ref string) string { return string(rune(chat)) + "|" + ref }

func (f *fakeSeen) Seen(_ context.Context, chat int64, ref string) (bool, error) {
	f.seenCall++
	return f.set[key(chat, ref)], nil
}
func (f *fakeSeen) Claim(_ context.Context, chat int64, ref string) (bool, error) {
	if f.set[key(chat, ref)] {
		return false, nil
	}
	f.set[key(chat, ref)] = true
	return true, nil
}
func (f *fakeSeen) Release(_ context.Context, chat int64, ref string) error {
	f.released++
	delete(f.set, key(chat, ref))
	return nil
}

type takeDrafts struct{ r Receipt }

func (d takeDrafts) Put(int64, Receipt) (string, error)  { return "d1", nil }
func (d takeDrafts) Take(int64, string) (Receipt, error) { return d.r, nil }

type countWriter struct {
	calls int
	err   error
}

func (w *countWriter) WriteExpense(context.Context, int64, Receipt) (string, error) {
	w.calls++
	return "записано", w.err
}

func dedupService(seen SeenStore, r Receipt, w ExpenseWriter) *Service {
	j := &journal{}
	return NewService(Deps{
		Catalog: stubCatalog{j: j, models: []core.ModelInfo{{Name: "vision", Capabilities: core.ModelCapabilities{Vision: true}}}},
		Model:   "vision", MaxBytes: 1 << 20,
		Classifier: stubClassifier{j: j, ok: true}, Parser: stubParser{j: j, r: r}, Categories: stubCategories{j: j, cats: []string{"Еда"}},
		Drafts: takeDrafts{r: r}, Writer: w, Seen: seen,
	})
}

var wallet = Receipt{Merchant: "TANTRAPHAN", Currency: "THB", Total: 357, Reference: "261026165917754NYGOI"}

// Один и тот же чек во второй раз не доходит до черновика; чек без номера и чек с новым номером проходят.
//
// Мутации: убрать проверку Seen в Process — краснеет «уже записан»; проверять и без номера — краснеет «без номера».
func TestProcess_DuplicateReference(t *testing.T) {
	seen := newFakeSeen()
	seen.set[key(42, wallet.Reference)] = true
	svc := dedupService(seen, wallet, &countWriter{})
	if _, err := svc.Process(context.Background(), 42, bytes.NewReader(jpegBytes)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("уже записанный чек: %v, ждали ErrDuplicate", err)
	}
	if _, err := svc.Process(context.Background(), 7, bytes.NewReader(jpegBytes)); err != nil {
		t.Errorf("тот же номер в другом чате: %v", err)
	}
	noRef := wallet
	noRef.Reference = ""
	seen.seenCall = 0
	if _, err := dedupService(seen, noRef, &countWriter{}).Process(context.Background(), 42, bytes.NewReader(jpegBytes)); err != nil || seen.seenCall != 0 {
		t.Errorf("чек без номера: err=%v, обращений к базе %d", err, seen.seenCall)
	}
}

// Подтверждение занимает номер до записи: второе подтверждение того же чека не пишет расход; сбой записи снимает
// отметку, и чек можно записать снова; без хранилища отметок запись идёт как раньше.
//
// Мутации: писать расход до Claim — краснеет «записан один раз»; не снимать отметку при сбое — краснеет «после сбоя».
func TestConfirm_ClaimsReferenceBeforeWriting(t *testing.T) {
	ctx := context.Background()
	seen := newFakeSeen()
	w := &countWriter{}
	svc := dedupService(seen, wallet, w)
	if _, err := svc.Confirm(ctx, 42, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, 42, "d1"); !errors.Is(err, ErrDuplicate) || w.calls != 1 {
		t.Errorf("второе подтверждение: err=%v, записей расхода %d, ждали ErrDuplicate и 1", err, w.calls)
	}

	seen = newFakeSeen()
	w = &countWriter{err: errors.New("база упала")}
	svc = dedupService(seen, wallet, w)
	if _, err := svc.Confirm(ctx, 42, "d1"); err == nil {
		t.Fatal("сбой записи проглочен")
	}
	if seen.released != 1 || seen.set[key(42, wallet.Reference)] {
		t.Errorf("после сбоя отметка не снята: released=%d", seen.released)
	}
	w.err = nil
	if _, err := svc.Confirm(ctx, 42, "d1"); err != nil {
		t.Errorf("повторная запись после сбоя: %v", err)
	}

	w = &countWriter{}
	svc = dedupService(nil, wallet, w)
	for i := 0; i < 2; i++ {
		if _, err := svc.Confirm(ctx, 42, "d1"); err != nil {
			t.Fatal(err)
		}
	}
	if w.calls != 2 {
		t.Errorf("без хранилища отметок записей %d, ждали 2", w.calls)
	}
}

// Номер с разных скринов одного чека приводится к одному виду; мусор длиннее предела отбрасывается.
func TestNormalizeReference(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"261026165917754NYGOI", "261026165917754NYGOI"},
		{" 2610 2616 5917754nygoi\n", "261026165917754NYGOI"},
		{"", ""},
		{strings.Repeat("A", 65), ""},
	} {
		if got := NormalizeReference(c.in); got != c.want {
			t.Errorf("%q → %q, ждали %q", c.in, got, c.want)
		}
	}
}

// Разбор достаёт номер транзакции из ответа модели и нормализует; классификатор и разбор называют подтверждения
// оплаты из кошелька, а не только бумажные чеки.
//
// Мутации: не заполнять Reference в decodeReceipt; вернуть старый текст классификатора — краснеют проверки.
func TestDecodeReceipt_ReferenceAndWalletPrompts(t *testing.T) {
	r, err := decodeReceipt(`{"is_receipt":true,"merchant":"TANTRAPHAN","reference":" 261026165917754nygoi ","currency":"THB","total":357,"category":""}`, nil)
	if err != nil || r.Reference != "261026165917754NYGOI" {
		t.Fatalf("разбор: %+v %v", r, err)
	}
	for name, text := range map[string]string{"классификатор": classifyPrompt, "разбор": parsePromptTemplate} {
		if !strings.Contains(text, "payment confirmation") && !strings.Contains(text, "Transaction ID") {
			t.Errorf("%s не упоминает подтверждение оплаты кошельком", name)
		}
	}
	if !strings.Contains(classifyPrompt, "wallet") {
		t.Error("классификатор не принимает скрины кошелька")
	}
}
