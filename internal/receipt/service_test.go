package receipt

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt/filecheck"
)

var jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F', 0, 1, 2, 3}

// journal записывает порядок шагов, через которые прошёл файл.
type journal struct{ steps []string }

func (j *journal) add(s string) { j.steps = append(j.steps, s) }

type stubCatalog struct {
	j      *journal
	models []core.ModelInfo
	err    error
}

func (c stubCatalog) Models(context.Context) ([]core.ModelInfo, error) {
	c.j.add("catalog")
	return c.models, c.err
}

type stubClassifier struct {
	j   *journal
	ok  bool
	err error
}

func (c stubClassifier) IsReceipt(context.Context, filecheck.File) (bool, error) {
	c.j.add("classify")
	return c.ok, c.err
}

type stubParser struct {
	j       *journal
	r       Receipt
	err     error
	gotCats *[]string
}

func (p stubParser) Parse(_ context.Context, _ filecheck.File, cats []string) (Receipt, error) {
	p.j.add("parse")
	if p.gotCats != nil {
		*p.gotCats = cats
	}
	return p.r, p.err
}

type stubCategories struct {
	j    *journal
	cats []string
	err  error
}

func (c stubCategories) ExpenseCategories(context.Context) ([]string, error) {
	c.j.add("categories")
	return c.cats, c.err
}

type stubDrafts struct {
	j      *journal
	put    []Receipt
	putErr error
}

func (d *stubDrafts) Put(_ int64, r Receipt) (string, error) {
	d.j.add("draft")
	if d.putErr != nil {
		return "", d.putErr
	}
	d.put = append(d.put, r)
	return "draft-1", nil
}

func (d *stubDrafts) Take(int64, string) (Receipt, error) { return Receipt{}, ErrDraftNotFound }

var sample = Receipt{Merchant: "7-Eleven", Currency: "THB", Total: 245.5, Category: "Еда"}

type fixture struct {
	j          *journal
	catalog    stubCatalog
	classifier stubClassifier
	parser     stubParser
	categories stubCategories
	drafts     *stubDrafts
}

func newFixture() *fixture {
	j := &journal{}
	return &fixture{
		j:          j,
		catalog:    stubCatalog{j: j, models: []core.ModelInfo{{Name: "vision", Capabilities: core.ModelCapabilities{Vision: true}}}},
		classifier: stubClassifier{j: j, ok: true},
		parser:     stubParser{j: j, r: sample},
		categories: stubCategories{j: j, cats: []string{"Еда"}},
		drafts:     &stubDrafts{j: j},
	}
}

func (f *fixture) service() *Service {
	return NewService(Deps{
		Catalog: f.catalog, Model: "vision", MaxBytes: 1 << 20,
		Classifier: f.classifier, Parser: f.parser, Categories: f.categories, Drafts: f.drafts,
	})
}

func (f *fixture) process(data []byte) (Draft, error) {
	return f.service().Process(context.Background(), 42, bytes.NewReader(data))
}

func TestProcess_HappyPathRunsStepsInOrder(t *testing.T) {
	f := newFixture()
	var gotCats []string
	f.parser.gotCats = &gotCats

	d, err := f.process(jpegBytes)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if want := []string{"catalog", "classify", "categories", "parse", "draft"}; !slices.Equal(f.j.steps, want) {
		t.Fatalf("steps = %v, want %v", f.j.steps, want)
	}
	if d.ID != "draft-1" || d.Receipt.Total != sample.Total || len(f.drafts.put) != 1 {
		t.Fatalf("draft = %+v, stored %d", d, len(f.drafts.put))
	}
	if !slices.Equal(gotCats, []string{"Еда"}) {
		t.Fatalf("parser got categories %v", gotCats)
	}
}

// Каждый отказ останавливает пайплайн: дальнейшие шаги не выполняются,
// черновик не создаётся.
func TestProcess_StopsAtFirstFailedStep(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name      string
		data      []byte
		setup     func(*fixture)
		wantErr   error
		wantSteps []string
	}{
		{"catalog down", jpegBytes, func(f *fixture) { f.catalog.err = boom }, ErrCatalogUnavailable, []string{"catalog"}},
		{"model not in catalog", jpegBytes, func(f *fixture) {
			f.catalog.models = []core.ModelInfo{{Name: "other", Capabilities: core.ModelCapabilities{Vision: true}}}
		}, ErrNotConfigured, []string{"catalog"}},
		{"model without vision", jpegBytes, func(f *fixture) {
			f.catalog.models = []core.ModelInfo{{Name: "vision"}}
		}, ErrNotConfigured, []string{"catalog"}},
		{"file is not an image", []byte("PK\x03\x04zip"), nil, filecheck.ErrUnknown, []string{"catalog"}},
		{"pdf for vision-only model", []byte("%PDF-1.7"), nil, filecheck.ErrNotSupported, []string{"catalog"}},
		{"not a receipt", jpegBytes, func(f *fixture) { f.classifier.ok = false }, ErrNotReceipt, []string{"catalog", "classify"}},
		{"classifier unsure", jpegBytes, func(f *fixture) { f.classifier.err = ErrUnreadable }, ErrUnreadable, []string{"catalog", "classify"}},
		{"categories fail", jpegBytes, func(f *fixture) { f.categories.err = boom }, boom, []string{"catalog", "classify", "categories"}},
		{"parser rejects", jpegBytes, func(f *fixture) { f.parser.err = ErrNoCurrency }, ErrNoCurrency, []string{"catalog", "classify", "categories", "parse"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			if tt.setup != nil {
				tt.setup(f)
			}
			_, err := f.process(tt.data)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(f.j.steps, tt.wantSteps) {
				t.Fatalf("steps = %v, want %v", f.j.steps, tt.wantSteps)
			}
			if len(f.drafts.put) != 0 {
				t.Fatal("draft created after a failed step")
			}
		})
	}
}

func TestProcess_WhenDraftStoreFails_ShouldReturnError(t *testing.T) {
	f := newFixture()
	f.drafts.putErr = errors.New("rand failed")
	if _, err := f.process(jpegBytes); err == nil {
		t.Fatal("want error")
	}
}
