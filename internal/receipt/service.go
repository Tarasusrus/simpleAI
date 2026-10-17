package receipt

import (
	"context"
	"errors"
	"fmt"
	"io"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt/filecheck"
)

// ErrNotConfigured — нет модели, которая принимает хотя бы один формат,
// который бот умеет проверить (или каталог моделей недоступен).
var ErrNotConfigured = errors.New("receipt: обработка фото не настроена")

// Draft — разобранный чек, ждущий подтверждения.
type Draft struct {
	ID      string
	Receipt Receipt
}

// Service выполняет шаги 1–4 ADR-010 строго по порядку: проверка файла →
// классификатор → разбор → черновик. Каждый шаг запускается только после
// успеха предыдущего, поэтому модель не получает файл, который не прошёл
// проверку, а разбор не тратится на фото без чека.
type Service struct {
	catalog    core.ModelCatalog
	model      string
	maxBytes   int64
	classifier Classifier
	parser     Parser
	categories CategoryLister
	drafts     DraftStore
}

type Deps struct {
	Catalog    core.ModelCatalog
	Model      string
	MaxBytes   int64
	Classifier Classifier
	Parser     Parser
	Categories CategoryLister
	Drafts     DraftStore
}

func NewService(d Deps) *Service {
	return &Service{
		catalog:    d.Catalog,
		model:      d.Model,
		maxBytes:   d.MaxBytes,
		classifier: d.Classifier,
		parser:     d.Parser,
		categories: d.Categories,
		drafts:     d.Drafts,
	}
}

// Process ведёт файл до черновика. Ошибки шагов — ErrNotConfigured,
// filecheck.Err*, ErrNotReceipt, ErrUnreadable, ErrNoCurrency, ErrNoTotal;
// остальные — сбой инфраструктуры.
func (s *Service) Process(ctx context.Context, chatID int64, file io.Reader) (Draft, error) {
	checker, err := s.checker(ctx)
	if err != nil {
		return Draft{}, err
	}
	f, err := checker.Check(file)
	if err != nil {
		return Draft{}, err
	}

	ok, err := s.classifier.IsReceipt(ctx, f)
	if err != nil {
		return Draft{}, err
	}
	if !ok {
		return Draft{}, ErrNotReceipt
	}

	cats, err := s.categories.ExpenseCategories(ctx)
	if err != nil {
		return Draft{}, fmt.Errorf("receipt: категории: %w", err)
	}
	r, err := s.parser.Parse(ctx, f, cats)
	if err != nil {
		return Draft{}, err
	}

	id, err := s.drafts.Put(chatID, r)
	if err != nil {
		return Draft{}, err
	}
	return Draft{ID: id, Receipt: r}, nil
}

// checker строит проверку файла под возможности модели из каталога.
func (s *Service) checker(ctx context.Context) (*filecheck.Checker, error) {
	models, err := s.catalog.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotConfigured, err)
	}
	for _, m := range models {
		if m.Name != s.model {
			continue
		}
		c, err := filecheck.NewChecker(m.Capabilities, s.maxBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotConfigured, err)
		}
		return c, nil
	}
	return nil, fmt.Errorf("%w: модели %q нет в каталоге", ErrNotConfigured, s.model)
}
