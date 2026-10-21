// Package filecheck решает, можно ли отдать файл модели: определяет тип по
// сигнатуре содержимого и сверяет его с тем, что умеет модель (ADR-014 §2).
package filecheck

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"simpleAI/internal/core"
)

// Format — тип файла, определённый по содержимому.
type Format string

const (
	JPEG Format = "jpeg"
	PNG  Format = "png"
	WEBP Format = "webp"
	GIF  Format = "gif"
	PDF  Format = "pdf"
)

var mimeTypes = map[Format]string{
	JPEG: "image/jpeg",
	PNG:  "image/png",
	WEBP: "image/webp",
	GIF:  "image/gif",
	PDF:  "application/pdf",
}

// MimeType — MIME для запроса к модели.
func (f Format) MimeType() string { return mimeTypes[f] }

var (
	ErrTooLarge     = errors.New("filecheck: файл больше лимита")
	ErrUnknown      = errors.New("filecheck: тип файла не распознан")
	ErrNotSupported = errors.New("filecheck: модель не принимает этот формат")
	ErrNoFormats    = errors.New("filecheck: модель не принимает ни одного формата, который бот умеет проверить")
)

// sniff определяет формат по содержимому алгоритмом WHATWG MIME Sniffing
// (net/http.DetectContentType). Распознанным считается только формат из
// mimeTypes: это и есть «что бот умеет проверить», даже если модель
// поддерживает больше.
func sniff(data []byte) (Format, bool) {
	detected := http.DetectContentType(data)
	for f, mime := range mimeTypes {
		if detected == mime {
			return f, true
		}
	}
	return "", false
}

// recognized — форматы, которые распознаёт sniff.
var recognized = func() []Format {
	out := make([]Format, 0, len(mimeTypes))
	for f := range mimeTypes {
		out = append(out, f)
	}
	return out
}()

// formatsFor переводит флаги модели в форматы. Флаги — да/нет, списка
// форматов провайдер не отдаёт, поэтому перевод зафиксирован в ADR-014 §2.
func formatsFor(c core.ModelCapabilities) []Format {
	var out []Format
	if c.Vision {
		out = append(out, JPEG, PNG, WEBP, GIF)
	}
	if c.PDFInput {
		out = append(out, PDF)
	}
	return out
}

func intersect(a, b []Format) map[Format]bool {
	inB := make(map[Format]bool, len(b))
	for _, f := range b {
		inB[f] = true
	}
	out := make(map[Format]bool)
	for _, f := range a {
		if inB[f] {
			out[f] = true
		}
	}
	return out
}

// Checker принимает файл, если его формат входит в пересечение возможностей
// модели и форматов, которые бот распознаёт, а размер не больше MaxBytes.
type Checker struct {
	allowed  map[Format]bool
	maxBytes int64
}

// NewChecker считает допустимые форматы для модели с возможностями caps.
// Пустое пересечение — ErrNoFormats: проверять файл бессмысленно.
func NewChecker(caps core.ModelCapabilities, maxBytes int64) (*Checker, error) {
	allowed := intersect(formatsFor(caps), recognized)
	if len(allowed) == 0 {
		return nil, ErrNoFormats
	}
	return &Checker{allowed: allowed, maxBytes: maxBytes}, nil
}

// File — проверенный файл, готовый к отправке модели.
type File struct {
	Format Format
	Data   []byte
}

// Check читает файл целиком, но не больше MaxBytes+1 байт: размер,
// заявленный клиентом, не проверяется — только реально прочитанное.
// Заявленный MIME сюда не передаётся вовсе.
func (c *Checker) Check(r io.Reader) (File, error) {
	data, err := io.ReadAll(io.LimitReader(r, c.maxBytes+1))
	if err != nil {
		return File{}, fmt.Errorf("filecheck: чтение файла: %w", err)
	}
	if int64(len(data)) > c.maxBytes {
		return File{}, ErrTooLarge
	}
	f, ok := sniff(data)
	if !ok {
		return File{}, ErrUnknown
	}
	if !c.allowed[f] {
		return File{}, fmt.Errorf("%w: %s", ErrNotSupported, f)
	}
	return File{Format: f, Data: data}, nil
}
