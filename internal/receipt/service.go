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

// ErrCatalogUnavailable — прокси моделей сейчас не отвечает; временно, в
// отличие от ErrNotConfigured.
var ErrCatalogUnavailable = errors.New("receipt: прокси моделей недоступен")

// Draft — разобранный чек, ждущий подтверждения.
type Draft struct {
	ID      string
	Receipt Receipt
}

// Service выполняет шаги 1–4 ADR-014 строго по порядку: проверка файла →
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
	writer     ExpenseWriter
	seen       SeenStore
}

type Deps struct {
	Catalog    core.ModelCatalog
	Model      string
	MaxBytes   int64
	Classifier Classifier
	Parser     Parser
	Categories CategoryLister
	Drafts     DraftStore
	Writer     ExpenseWriter
	// Seen необязателен: nil — защиты от повторной записи по номеру транзакции нет.
	Seen SeenStore
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
		writer:     d.Writer,
		seen:       d.Seen,
	}
}

// Process ведёт файл до черновика. Ошибки шагов — ErrNotConfigured,
// ErrCatalogUnavailable,
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

	// Уже записанный чек не доходит до черновика: пользователь сразу видит причину, а не после «Записать».
	if r.Reference != "" && s.seen != nil {
		dup, err := s.seen.Seen(ctx, chatID, r.Reference)
		if err != nil {
			return Draft{}, fmt.Errorf("receipt: проверка дубля: %w", err)
		}
		if dup {
			return Draft{}, ErrDuplicate
		}
	}

	id, err := s.drafts.Put(chatID, r)
	if err != nil {
		return Draft{}, err
	}
	return Draft{ID: id, Receipt: r}, nil
}

// Confirm — шаг 5: забирает черновик и записывает расход. Черновик забран
// до записи: при сбое записи пользователь присылает фото ещё раз, зато
// повторное нажатие никогда не даёт второй расход.
func (s *Service) Confirm(ctx context.Context, chatID int64, draftID string) (string, error) {
	r, err := s.drafts.Take(chatID, draftID)
	if err != nil {
		return "", err
	}
	if r.Reference == "" || s.seen == nil {
		return s.writer.WriteExpense(ctx, chatID, r)
	}
	// Номер занимается до записи, атомарно: два подтверждения одного чека не дадут два расхода. Не записалось —
	// отметка снимается, чтобы чек можно было прислать ещё раз.
	claimed, err := s.seen.Claim(ctx, chatID, r.Reference)
	if err != nil {
		return "", fmt.Errorf("receipt: отметка чека: %w", err)
	}
	if !claimed {
		return "", ErrDuplicate
	}
	reply, err := s.writer.WriteExpense(ctx, chatID, r)
	if err != nil {
		if rerr := s.seen.Release(ctx, chatID, r.Reference); rerr != nil {
			return "", fmt.Errorf("%w (снять отметку чека не удалось: %v)", err, rerr)
		}
		return "", err
	}
	return reply, nil
}

// Cancel удаляет черновик без записи.
func (s *Service) Cancel(chatID int64, draftID string) error {
	_, err := s.drafts.Take(chatID, draftID)
	return err
}

// checker строит проверку файла под возможности модели из каталога.
func (s *Service) checker(ctx context.Context) (*filecheck.Checker, error) {
	models, err := s.catalog.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCatalogUnavailable, err)
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
