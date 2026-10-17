package filecheck

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"simpleAI/internal/core"
)

var (
	jpegData = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("JFIF body")...)
	pngData  = []byte("\x89PNG\r\n\x1a\n rest")
	gifData  = []byte("GIF89a rest")
	webpData = []byte("RIFF\x10\x00\x00\x00WEBPVP8 ")
	pdfData  = []byte("%PDF-1.7\n rest")
	zipData  = []byte("PK\x03\x04 not an image")

	visionOnly = core.ModelCapabilities{Vision: true}
	pdfOnly    = core.ModelCapabilities{PDFInput: true}
	both       = core.ModelCapabilities{Vision: true, PDFInput: true}
)

func mustChecker(t *testing.T, caps core.ModelCapabilities, limit int64) *Checker {
	t.Helper()
	c, err := NewChecker(caps, limit)
	if err != nil {
		t.Fatalf("NewChecker(%+v): %v", caps, err)
	}
	return c
}

func TestCheck_FormatComesFromSignature(t *testing.T) {
	c := mustChecker(t, both, 1<<20)
	tests := []struct {
		name string
		data []byte
		want Format
	}{
		{"jpeg", jpegData, JPEG},
		{"png", pngData, PNG},
		{"gif", gifData, GIF},
		{"webp", webpData, WEBP},
		{"pdf", pdfData, PDF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Check(bytes.NewReader(tt.data))
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got.Format != tt.want {
				t.Fatalf("format = %q, want %q", got.Format, tt.want)
			}
			if !bytes.Equal(got.Data, tt.data) {
				t.Fatalf("data changed")
			}
		})
	}
}

// Клиент может назвать ZIP картинкой: Check не принимает заявленный тип,
// поэтому решает только содержимое.
func TestCheck_WhenContentIsNotAKnownFormat_ShouldRejectAsUnknown(t *testing.T) {
	c := mustChecker(t, both, 1<<20)
	tests := []struct {
		name string
		data []byte
	}{
		{"zip", zipData},
		{"empty", nil},
		{"riff without webp marker", []byte("RIFF\x10\x00\x00\x00AVI LIST")},
		{"truncated riff", []byte("RIFF\x10\x00")},
		{"two jpeg bytes", []byte{0xFF, 0xD8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Check(bytes.NewReader(tt.data))
			if !errors.Is(err, ErrUnknown) {
				t.Fatalf("err = %v, want ErrUnknown", err)
			}
		})
	}
}

func TestCheck_WhenModelLacksCapability_ShouldRejectFormat(t *testing.T) {
	tests := []struct {
		name    string
		caps    core.ModelCapabilities
		data    []byte
		wantErr error
	}{
		{"vision model, pdf", visionOnly, pdfData, ErrNotSupported},
		{"vision model, jpeg", visionOnly, jpegData, nil},
		{"pdf model, jpeg", pdfOnly, jpegData, ErrNotSupported},
		{"pdf model, webp", pdfOnly, webpData, ErrNotSupported},
		{"pdf model, pdf", pdfOnly, pdfData, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustChecker(t, tt.caps, 1<<20)
			_, err := c.Check(bytes.NewReader(tt.data))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewChecker_WhenModelAcceptsNothing_ShouldFail(t *testing.T) {
	_, err := NewChecker(core.ModelCapabilities{}, 1<<20)
	if !errors.Is(err, ErrNoFormats) {
		t.Fatalf("err = %v, want ErrNoFormats", err)
	}
}

// Модель может уметь формат, которого бот не распознаёт: такой формат
// в допустимые не попадает.
func TestIntersect_KeepsOnlyFormatsKnownToBothSides(t *testing.T) {
	got := intersect([]Format{JPEG, "heic", PDF}, []Format{JPEG, PNG, PDF})
	want := map[Format]bool{JPEG: true, PDF: true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for f := range want {
		if !got[f] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCheck_SizeLimit(t *testing.T) {
	const limit = 64
	exact := append(append([]byte{}, jpegData...), make([]byte, limit-len(jpegData))...)
	c := mustChecker(t, visionOnly, limit)

	if _, err := c.Check(bytes.NewReader(exact)); err != nil {
		t.Fatalf("file of exactly MaxBytes: %v", err)
	}
	over := append(append([]byte{}, exact...), 0)
	if _, err := c.Check(bytes.NewReader(over)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("file of MaxBytes+1: err = %v, want ErrTooLarge", err)
	}
}

// endless отдаёт JPEG-заголовок, а затем нули без конца и считает отданное.
type endless struct{ n int64 }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		if e.n < int64(len(jpegData)) {
			p[i] = jpegData[e.n]
		} else {
			p[i] = 0
		}
		e.n++
	}
	return len(p), nil
}

// Размер, заявленный клиентом, может врать: Check обязан остановиться на
// MaxBytes+1 байте, а не дочитать поток до конца.
func TestCheck_WhenStreamIsEndless_ShouldStopAfterLimit(t *testing.T) {
	const limit = 4096
	r := &endless{}
	_, err := mustChecker(t, visionOnly, limit).Check(r)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if r.n > limit+1 {
		t.Fatalf("read %d bytes, want at most %d", r.n, limit+1)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCheck_WhenReadFails_ShouldReturnReadError(t *testing.T) {
	_, err := mustChecker(t, visionOnly, 1<<20).Check(failingReader{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestFormat_MimeType(t *testing.T) {
	for _, f := range recognized {
		if f.MimeType() == "" {
			t.Fatalf("format %q has no MIME type", f)
		}
	}
}
