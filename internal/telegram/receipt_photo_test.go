package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt"
	"simpleAI/internal/receipt/filecheck"
)

var photoBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("receipt-pixels"), 20)...)

type memFetcher struct {
	data   []byte
	err    error
	closed int
}

type trackedBody struct {
	io.Reader
	f *memFetcher
}

func (b trackedBody) Close() error { b.f.closed++; return nil }

func (f *memFetcher) FetchAttachment(context.Context, core.Attachment) (io.ReadCloser, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return trackedBody{Reader: bytes.NewReader(f.data), f: f}, "photos/file_1.jpg", nil
}

type recordingReceipts struct {
	got   []byte
	draft receipt.Draft
	err   error
	calls int
}

func (r *recordingReceipts) Process(_ context.Context, _ int64, file io.Reader) (receipt.Draft, error) {
	r.calls++
	b, err := io.ReadAll(file)
	if err != nil {
		return receipt.Draft{}, err
	}
	r.got = b
	return r.draft, r.err
}

func (r *recordingReceipts) Confirm(context.Context, int64, string) (string, error) { return "", nil }
func (r *recordingReceipts) Cancel(int64, string) error                             { return nil }

type photoRig struct {
	bot      *buttonBot
	fetcher  *memFetcher
	svc      *recordingReceipts
	logs     *bytes.Buffer
	nextHits int
	tctx     *Context
}

func newPhotoRig(t *testing.T, update core.Update) *photoRig {
	t.Helper()
	r := &photoRig{
		bot:     &buttonBot{},
		fetcher: &memFetcher{data: photoBytes},
		svc:     &recordingReceipts{draft: receipt.Draft{ID: "d1", Receipt: sampleReceipt}},
		logs:    &bytes.Buffer{},
	}
	update.ChatID = ownerChat
	r.tctx = &Context{
		Bot: r.bot, Update: update, Fetcher: r.fetcher, MediaDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(r.logs, nil)),
	}
	return r
}

func (r *photoRig) run(t *testing.T, svc ReceiptService) {
	t.Helper()
	next := func(context.Context, *Context) error { r.nextHits++; return nil }
	if err := WithReceiptPhotos(svc, next)(context.Background(), r.tctx); err != nil {
		t.Fatalf("handler: %v", err)
	}
}

func (r *photoRig) mediaDirEmpty(t *testing.T) bool {
	t.Helper()
	entries, err := os.ReadDir(r.tctx.MediaDir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries) == 0
}

func photoUpdate(caption string) core.Update {
	return core.Update{Text: caption, Attachments: []core.Attachment{{Kind: "photo", ID: "f1", Size: 300}}}
}

func TestWithReceiptPhotos_PhotoGoesToReceiptFlowNotAgent(t *testing.T) {
	for _, caption := range []string{"", "сколько я потратил за неделю?"} {
		t.Run(fmt.Sprintf("caption=%q", caption), func(t *testing.T) {
			r := newPhotoRig(t, photoUpdate(caption))
			r.run(t, r.svc)

			if r.nextHits != 0 {
				t.Fatal("photo reached the default handler (agent / disk)")
			}
			if !bytes.Equal(r.svc.got, photoBytes) {
				t.Fatal("receipt flow got different bytes")
			}
			if r.fetcher.closed != 1 {
				t.Fatalf("attachment body closed %d times, want 1", r.fetcher.closed)
			}
			if len(r.bot.sent) != 1 || !strings.Contains(r.bot.sent[0], "7-Eleven") || len(r.bot.rows) != 1 {
				t.Fatalf("draft not shown with buttons: sent %v", r.bot.sent)
			}
			if !r.mediaDirEmpty(t) {
				t.Fatal("photo saved to MediaDir")
			}
		})
	}
}

func TestWithReceiptPhotos_Routing(t *testing.T) {
	tests := []struct {
		name      string
		update    core.Update
		toReceipt bool
	}{
		{"text", core.Update{Text: "кофе 120"}, false},
		{"voice", core.Update{Attachments: []core.Attachment{{Kind: "voice", MimeType: "audio/ogg"}}}, false},
		{"zip document", core.Update{Attachments: []core.Attachment{{Kind: "document", MimeType: "application/zip"}}}, false},
		{"jpeg document", core.Update{Attachments: []core.Attachment{{Kind: "document", MimeType: "image/jpeg"}}}, true},
		{"pdf document", core.Update{Attachments: []core.Attachment{{Kind: "document", MimeType: "application/pdf"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newPhotoRig(t, tt.update)
			r.run(t, r.svc)
			if got := r.svc.calls == 1; got != tt.toReceipt {
				t.Fatalf("receipt flow called = %v, want %v", got, tt.toReceipt)
			}
			if got := r.nextHits == 1; got == tt.toReceipt {
				t.Fatalf("default handler called = %v, want %v", got, !tt.toReceipt)
			}
		})
	}
}

func TestWithReceiptPhotos_WhenNotConfigured_RepliesAndKeepsPhotoOffDisk(t *testing.T) {
	r := newPhotoRig(t, photoUpdate(""))
	r.run(t, nil)
	if r.nextHits != 0 || r.bot.lastSent() != msgReceiptNotConfigured || !r.mediaDirEmpty(t) {
		t.Fatalf("next %d, sent %q", r.nextHits, r.bot.lastSent())
	}
}

func TestWithReceiptPhotos_ErrorMessages(t *testing.T) {
	tests := []struct {
		err    error
		want   string
		logged bool
	}{
		{fmt.Errorf("%w: модели нет", receipt.ErrNotConfigured), msgReceiptNotConfigured, true},
		{filecheck.ErrTooLarge, msgReceiptTooLarge, false},
		{filecheck.ErrUnknown, msgReceiptUnknownFile, false},
		{fmt.Errorf("%w: pdf", filecheck.ErrNotSupported), msgReceiptUnsupported, false},
		{receipt.ErrNotReceipt, msgReceiptNotReceipt, false},
		{receipt.ErrUnreadable, msgReceiptUnreadable, false},
		{receipt.ErrNoCurrency, msgReceiptNoCurrency, false},
		{receipt.ErrNoTotal, msgReceiptNoTotal, false},
		{fmt.Errorf("%w: 503", receipt.ErrCatalogUnavailable), msgReceiptProxyDown, true},
		{errors.New("pq: connection refused"), msgReceiptFailed, true},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			r := newPhotoRig(t, photoUpdate(""))
			r.svc.err = tt.err
			r.run(t, r.svc)
			if got := r.bot.lastSent(); got != tt.want {
				t.Fatalf("reply = %q, want %q", got, tt.want)
			}
			if logged := r.logs.Len() > 0; logged != tt.logged {
				t.Fatalf("logged = %v, want %v: %s", logged, tt.logged, r.logs)
			}
		})
	}
}

// Ошибка провайдера может процитировать тело запроса с картинкой: в лог не
// должны попасть ни base64, ни байты файла.
func TestWithReceiptPhotos_LogsNoImageData(t *testing.T) {
	r := newPhotoRig(t, photoUpdate(""))
	encoded := base64.StdEncoding.EncodeToString(photoBytes)
	r.svc.err = fmt.Errorf("upstream 400: invalid image data:image/jpeg;base64,%s", encoded)
	r.run(t, r.svc)

	logs := r.logs.String()
	if logs == "" {
		t.Fatal("infrastructure error was not logged")
	}
	if strings.Contains(logs, encoded[:64]) || strings.Contains(logs, "receipt-pixels") {
		t.Fatalf("image data leaked into logs: %s", logs)
	}
	if r.bot.lastSent() != msgReceiptFailed {
		t.Fatalf("reply = %q", r.bot.lastSent())
	}
}

// Ошибка скачивания содержит URL с токеном бота.
func TestWithReceiptPhotos_FetchErrorLogHasNoBotToken(t *testing.T) {
	r := newPhotoRig(t, photoUpdate(""))
	const token = "123456789:AAEhBP0av28dfHdqYfXHkTrW1PbwXz0abcd"
	r.fetcher.err = fmt.Errorf(`Get "https://api.telegram.org/file/bot%s/photos/file_3.jpg": dial tcp: i/o timeout`, token)
	r.run(t, r.svc)
	if strings.Contains(r.logs.String(), "AAEhBP0av28dfHdqYfXHkTrW1PbwXz0abcd") {
		t.Fatalf("bot token leaked: %s", r.logs)
	}
	if r.logs.Len() == 0 {
		t.Fatal("fetch error not logged")
	}
}

func TestWithReceiptPhotos_ShowsTypingWhileProcessing(t *testing.T) {
	r := newPhotoRig(t, photoUpdate(""))
	r.run(t, r.svc)
	if r.bot.typing.Load() == 0 {
		t.Fatal("no typing indicator during receipt processing")
	}
}

func TestWithReceiptPhotos_WhenFetchFails(t *testing.T) {
	r := newPhotoRig(t, photoUpdate(""))
	r.fetcher.err = errors.New("telegram getFile: 502")
	r.run(t, r.svc)
	if r.svc.calls != 0 || r.bot.lastSent() != msgReceiptFailed {
		t.Fatalf("calls %d, reply %q", r.svc.calls, r.bot.lastSent())
	}
}

func TestScrubError_TruncatesLongTextOnRuneBoundary(t *testing.T) {
	got := scrubError(errors.New(strings.Repeat("ж", errTextLimit)))
	if !strings.HasSuffix(got, "…") || len(got) > errTextLimit+len("…") {
		t.Fatalf("len %d", len(got))
	}
	if !strings.HasPrefix(got, "жж") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("truncation broke UTF-8")
	}
}

type stubModels struct {
	models []core.ModelInfo
	err    error
}

func (s stubModels) Models(context.Context) ([]core.ModelInfo, error) { return s.models, s.err }

func runModels(t *testing.T, catalog core.ModelCatalog) string {
	t.Helper()
	bot := &buttonBot{}
	tctx := &Context{Bot: bot, Update: core.Update{ChatID: ownerChat, Text: "/models"}}
	if err := NewHandleModels(catalog, "vision")(context.Background(), tctx); err != nil {
		t.Fatal(err)
	}
	return bot.lastSent()
}

func TestHandleModels(t *testing.T) {
	if got := runModels(t, nil); !strings.HasPrefix(got, msgReceiptNotConfigured) {
		t.Errorf("nil catalog: %q", got)
	}
	if got := runModels(t, stubModels{err: errors.New("down")}); !strings.Contains(got, "недоступен") {
		t.Errorf("catalog error: %q", got)
	}
	got := runModels(t, stubModels{models: []core.ModelInfo{
		{Name: "standard"},
		{Name: "vision", Capabilities: core.ModelCapabilities{Vision: true}},
		{Name: "gemini", Capabilities: core.ModelCapabilities{Vision: true, PDFInput: true}},
	}})
	for _, want := range []string{"standard — только текст", "vision — фото ← для чеков", "gemini — фото и PDF"} {
		if !strings.Contains(got, want) {
			t.Errorf("list lacks %q:\n%s", want, got)
		}
	}
	if missing := runModels(t, stubModels{models: []core.ModelInfo{{Name: "standard"}}}); !strings.Contains(missing, `"vision"`) {
		t.Errorf("missing receipt model not reported:\n%s", missing)
	}
}

func TestReceiptCallback_WhenNotConfigured(t *testing.T) {
	bot := &buttonBot{}
	if err := pressErr(nil, bot, ownerChat, receiptCallbackData(receiptActionConfirm, "x")); err != nil {
		t.Fatal(err)
	}
	if bot.lastEdit() != msgReceiptNotConfigured {
		t.Fatalf("edit = %q", bot.lastEdit())
	}
}
