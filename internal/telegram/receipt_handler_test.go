package telegram

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt"
	"simpleAI/internal/receipt/memdraft"
)

// buttonBot — core.Bot с кнопками: запоминает правки сообщения черновика.
type buttonBot struct {
	captureBot
	mu    sync.Mutex
	edits []string
	rows  [][][]core.Button
}

func (b *buttonBot) SendWithButtons(_ context.Context, _ int64, text string, rows [][]core.Button) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, text)
	b.rows = append(b.rows, rows)
	return nil
}

func (b *buttonBot) EditWithButtons(_ context.Context, _ int64, _ int, text string, rows [][]core.Button) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.edits = append(b.edits, text)
	b.rows = append(b.rows, rows)
	return nil
}

func (b *buttonBot) AnswerCallback(context.Context, string) error { return nil }

func (b *buttonBot) lastEdit() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.edits) == 0 {
		return ""
	}
	return b.edits[len(b.edits)-1]
}

type countingWriter struct {
	calls atomic.Int32
	reply string
	err   error
}

func (w *countingWriter) WriteExpense(context.Context, int64, receipt.Receipt) (string, error) {
	w.calls.Add(1)
	return w.reply, w.err
}

const ownerChat = int64(100)

var sampleReceipt = receipt.Receipt{Merchant: "7-Eleven", Date: "2026-10-16", Currency: "THB", Total: 245.5, Category: "Еда"}

// receiptRig — настоящий receipt.Service с настоящими черновиками; подменена
// только запись расхода.
func receiptRig(t *testing.T) (*receipt.Service, *memdraft.Store, *countingWriter) {
	t.Helper()
	drafts := memdraft.New(time.Minute)
	w := &countingWriter{reply: "✅ Расход 245.5 THB\n⚠️ Конверт «Еда» пробит"}
	return receipt.NewService(receipt.Deps{Drafts: drafts, Writer: w}), drafts, w
}

func putDraft(t *testing.T, drafts *memdraft.Store) string {
	t.Helper()
	id, err := drafts.Put(ownerChat, sampleReceipt)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func pressErr(svc ReceiptService, bot *buttonBot, chatID int64, data string) error {
	tctx := &Context{Bot: bot, Logger: slog.New(slog.DiscardHandler), Update: core.Update{
		ChatID: chatID, IsCallback: true, CallbackData: data, CallbackID: "cb", MessageID: 7,
	}}
	return NewHandleReceiptCallback(svc)(context.Background(), tctx)
}

func press(t *testing.T, svc ReceiptService, bot *buttonBot, chatID int64, data string) {
	t.Helper()
	if err := pressErr(svc, bot, chatID, data); err != nil {
		t.Fatalf("handler: %v", err)
	}
}

func TestReceiptCallback_ConfirmWritesOnceAndShowsSkillReply(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	bot := &buttonBot{}
	id := putDraft(t, drafts)

	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id))
	if got := bot.lastEdit(); got != w.reply {
		t.Fatalf("edit = %q, want skill reply verbatim (ADR-008 warning)", got)
	}
	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id))
	if got := bot.lastEdit(); got != msgReceiptDraftGone {
		t.Fatalf("second press edit = %q, want draft-gone message", got)
	}
	if n := w.calls.Load(); n != 1 {
		t.Fatalf("expenses written = %d, want 1", n)
	}
}

func TestReceiptCallback_ConcurrentDoublePressWritesOnce(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	bot := &buttonBot{}
	id := putDraft(t, drafts)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pressErr(svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := w.calls.Load(); n != 1 {
		t.Fatalf("expenses written = %d, want 1", n)
	}
}

func TestReceiptCallback_ForeignChatCannotConfirm(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	bot := &buttonBot{}
	id := putDraft(t, drafts)

	press(t, svc, bot, ownerChat+1, receiptCallbackData(receiptActionConfirm, id))
	if got := bot.lastEdit(); got != msgReceiptDraftGone || w.calls.Load() != 0 {
		t.Fatalf("foreign press: edit %q, writes %d", got, w.calls.Load())
	}
	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id))
	if w.calls.Load() != 1 {
		t.Fatal("owner could not confirm after a foreign press")
	}
}

func TestReceiptCallback_CancelDropsDraft(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	bot := &buttonBot{}
	id := putDraft(t, drafts)

	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionCancel, id))
	if got := bot.lastEdit(); got != msgReceiptCancelled {
		t.Fatalf("edit = %q, want cancelled", got)
	}
	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id))
	if w.calls.Load() != 0 {
		t.Fatal("expense written after cancel")
	}
}

// Сырая ошибка записи не уходит пользователю.
func TestReceiptCallback_WhenWriteFails_ShowsGenericMessage(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	w.err = errors.New("pq: connection refused at 10.0.0.5")
	bot := &buttonBot{}
	id := putDraft(t, drafts)

	press(t, svc, bot, ownerChat, receiptCallbackData(receiptActionConfirm, id))
	if got := bot.lastEdit(); got != msgReceiptWriteFailed {
		t.Fatalf("edit = %q, want generic failure", got)
	}
}

func TestReceiptCallback_IgnoresMalformedData(t *testing.T) {
	svc, drafts, w := receiptRig(t)
	bot := &buttonBot{}
	id := putDraft(t, drafts)
	for _, data := range []string{"rcpt:", "rcpt:ok", "rcpt:ok:", "rcpt:zz:" + id} {
		press(t, svc, bot, ownerChat, data)
	}
	if len(bot.edits) != 0 || w.calls.Load() != 0 {
		t.Fatalf("malformed data changed state: edits %v, writes %d", bot.edits, w.calls.Load())
	}
}

func TestReceiptDraftMessage(t *testing.T) {
	items := make([]receipt.Item, 12)
	for i := range items {
		items[i] = receipt.Item{Name: "item", Amount: 1}
	}
	r := sampleReceipt
	r.Items = items
	text, rows := ReceiptDraftMessage(receipt.Draft{ID: "0123456789abcdef", Receipt: r})

	for _, want := range []string{"7-Eleven", "2026-10-16", "245.5 THB", "Еда", "… ещё 2", msgReceiptAsk} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "• item"); n != receiptItemsShown {
		t.Errorf("items shown = %d, want %d", n, receiptItemsShown)
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("buttons = %v", rows)
	}
	for _, b := range rows[0] {
		if !strings.HasPrefix(b.Data, ReceiptCallbackPrefix) || !strings.HasSuffix(b.Data, "0123456789abcdef") || len(b.Data) > 64 {
			t.Errorf("button data %q", b.Data)
		}
	}

	noCat, _ := ReceiptDraftMessage(receipt.Draft{ID: "x", Receipt: receipt.Receipt{Currency: "RUB", Total: 10}})
	if !strings.Contains(noCat, "без категории") || !strings.Contains(noCat, "Чек") {
		t.Errorf("draft without merchant/category:\n%s", noCat)
	}
	if bytes.Contains([]byte(noCat), []byte("·")) {
		t.Errorf("date separator shown without date:\n%s", noCat)
	}
}
