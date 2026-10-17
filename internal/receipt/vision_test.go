package receipt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt/filecheck"
)

type fakeVision struct {
	reply string
	err   error
	reqs  []core.VisionRequest
}

func (f *fakeVision) AskVision(_ context.Context, req core.VisionRequest) (string, error) {
	f.reqs = append(f.reqs, req)
	return f.reply, f.err
}

func pngFile(t *testing.T, w, h int) filecheck.File {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return filecheck.File{Format: filecheck.PNG, Data: buf.Bytes()}
}

func TestVisionClassifier_ParsesStrictAnswer(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		want    bool
		wantErr error
	}{
		{"true", `{"is_receipt":true}`, true, nil},
		{"false", `{"is_receipt":false}`, false, nil},
		{"fenced", "```json\n{\"is_receipt\": true}\n```", true, nil},
		{"missing field", `{"receipt":true}`, false, ErrUnreadable},
		{"string instead of bool", `{"is_receipt":"yes"}`, false, ErrUnreadable},
		{"prose", "Yes, this is a receipt.", false, ErrUnreadable},
		{"empty", "", false, ErrUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &fakeVision{reply: tt.reply}
			got, err := NewVisionClassifier(llm, "vision").IsReceipt(context.Background(), pngFile(t, 10, 10))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("IsReceipt = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisionClassifier_WhenModelFails_ShouldReturnItsError(t *testing.T) {
	boom := errors.New("upstream down")
	_, err := NewVisionClassifier(&fakeVision{err: boom}, "vision").IsReceipt(context.Background(), pngFile(t, 10, 10))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want upstream error", err)
	}
}

// Классификатор — дешёвый шаг: уменьшенная картинка и короткий ответ.
func TestVisionClassifier_SendsShrunkImageWithShortAnswerLimit(t *testing.T) {
	llm := &fakeVision{reply: `{"is_receipt":true}`}
	if _, err := NewVisionClassifier(llm, "vision").IsReceipt(context.Background(), pngFile(t, 1600, 800)); err != nil {
		t.Fatal(err)
	}
	req := llm.reqs[0]
	if req.Model != "vision" || req.MaxTokens != classifyMaxTokens || req.MimeType != "image/jpeg" {
		t.Fatalf("request = model %q, max_tokens %d, mime %q", req.Model, req.MaxTokens, req.MimeType)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(req.Data))
	if err != nil {
		t.Fatalf("sent data is not JPEG: %v", err)
	}
	if cfg.Width != shrinkSide || cfg.Height != shrinkSide/2 {
		t.Fatalf("sent %dx%d, want %dx%d", cfg.Width, cfg.Height, shrinkSide, shrinkSide/2)
	}
}

func TestShrink(t *testing.T) {
	t.Run("portrait keeps aspect", func(t *testing.T) {
		got := shrink(pngFile(t, 300, 1200))
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(got.Data))
		if err != nil || got.Format != filecheck.JPEG {
			t.Fatalf("format %q, err %v", got.Format, err)
		}
		if cfg.Width != 128 || cfg.Height != shrinkSide {
			t.Fatalf("got %dx%d, want 128x%d", cfg.Width, cfg.Height, shrinkSide)
		}
	})
	t.Run("small image unchanged", func(t *testing.T) {
		in := pngFile(t, shrinkSide, 100)
		if got := shrink(in); got.Format != filecheck.PNG || !bytes.Equal(got.Data, in.Data) {
			t.Fatal("small image was re-encoded")
		}
	})
	for _, in := range []filecheck.File{
		{Format: filecheck.WEBP, Data: []byte("RIFF....WEBPVP8 ")},
		{Format: filecheck.PDF, Data: []byte("%PDF-1.7")},
		{Format: filecheck.JPEG, Data: []byte{0xFF, 0xD8, 0xFF, 0x00}},
	} {
		t.Run(string(in.Format)+" passes through", func(t *testing.T) {
			if got := shrink(in); got.Format != in.Format || !bytes.Equal(got.Data, in.Data) {
				t.Fatal("undecodable file was changed")
			}
		})
	}
}

var categories = []string{"Еда", "Транспорт", "Кафе"}

func TestVisionParser_ValidReceipt(t *testing.T) {
	llm := &fakeVision{reply: "Here you go:\n```json\n" + `{"is_receipt":true,"merchant":" 7-Eleven ","date":"2026-10-16",
		"currency":"thb","total":245.5,"category":"еда",
		"items":[{"name":"Milk","amount":45.5},{"name":" ","amount":1},{"name":"Bread","amount":200}]}` + "\n```"}
	f := pngFile(t, 1600, 800)

	got, err := NewVisionParser(llm, "vision").Parse(context.Background(), f, categories)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Receipt{Merchant: "7-Eleven", Date: "2026-10-16", Currency: "THB", Total: 245.5, Category: "Еда",
		Items: []Item{{"Milk", 45.5}, {"Bread", 200}}}
	if got.Merchant != want.Merchant || got.Date != want.Date || got.Currency != want.Currency ||
		got.Total != want.Total || got.Category != want.Category || len(got.Items) != len(want.Items) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want.Items {
		if got.Items[i] != want.Items[i] {
			t.Fatalf("item %d = %+v, want %+v", i, got.Items[i], want.Items[i])
		}
	}

	req := llm.reqs[0]
	if !bytes.Equal(req.Data, f.Data) || req.MimeType != "image/png" {
		t.Error("parser must send the original image, not a shrunk one")
	}
	if req.System == "" || !strings.Contains(req.Prompt, `"Транспорт"`) {
		t.Error("prompt must carry the system part and the category list")
	}
}

func TestVisionParser_RejectsUnusableAnswers(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		wantErr error
	}{
		{"not a receipt", `{"is_receipt":false,"total":100,"currency":"THB"}`, ErrNotReceipt},
		{"no verdict", `{"total":100,"currency":"THB"}`, ErrUnreadable},
		{"prose", "I cannot read this", ErrUnreadable},
		{"no currency", `{"is_receipt":true,"total":100,"currency":""}`, ErrNoCurrency},
		{"word instead of code", `{"is_receipt":true,"total":100,"currency":"руб"}`, ErrNoCurrency},
		{"two letters", `{"is_receipt":true,"total":100,"currency":"US"}`, ErrNoCurrency},
		{"zero total", `{"is_receipt":true,"total":0,"currency":"THB"}`, ErrNoTotal},
		{"negative total", `{"is_receipt":true,"total":-5,"currency":"THB"}`, ErrNoTotal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewVisionParser(&fakeVision{reply: tt.reply}, "vision").Parse(context.Background(), pngFile(t, 4, 4), categories)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVisionParser_DropsUnknownCategoryAndBadDate(t *testing.T) {
	llm := &fakeVision{reply: `{"is_receipt":true,"total":10,"currency":"RUB","category":"Электроника","date":"16.10.2026"}`}
	got, err := NewVisionParser(llm, "vision").Parse(context.Background(), pngFile(t, 4, 4), categories)
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != "" {
		t.Errorf("category = %q, want empty: unknown name would create a new category", got.Category)
	}
	if got.Date != "" {
		t.Errorf("date = %q, want empty for non-ISO date", got.Date)
	}
}

func TestVisionParser_WhenModelFails_ShouldReturnItsError(t *testing.T) {
	boom := errors.New("upstream down")
	_, err := NewVisionParser(&fakeVision{err: boom}, "vision").Parse(context.Background(), pngFile(t, 4, 4), categories)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want upstream error", err)
	}
}
