// Package txedit — правка и удаление любой транзакции со страницы «Транзакции» (ADR-018).
//
// Один путь для всех транзакций, независимо от того, кто их создал. Пакет проверяет входные значения и выбирает
// категорию; атомарность, версия строки и журнал tx_edit_log живут в хранилище (budget.Store).
package txedit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/categories"
	"simpleAI/internal/plan"
)

var (
	// ErrUnknownCategory — категории с таким именем нет: страница выбирает из существующих, новых не создаёт.
	ErrUnknownCategory = errors.New("txedit: такой категории нет")
	// ErrCategoryRetired — категория вытеснена разделением («Еда», ADR-013): выбирать её нельзя.
	ErrCategoryRetired = errors.New("txedit: категория разделена на другие, выберите одну из них")
)

// Store — то, что сервис берёт у хранилища транзакций (реализует *budget.Store).
type Store interface {
	GetTx(ctx context.Context, id uuid.UUID) (budget.TxRow, error)
	EditTx(ctx context.Context, id uuid.UUID, version time.Time, source string, c budget.TxChange) (budget.TxRow, error)
	DeleteTx(ctx context.Context, id uuid.UUID, version time.Time, source string) error
}

// Directory — то, что сервис берёт у каталога категорий (реализует *categories.Store).
type Directory interface {
	Resolve(ctx context.Context, name, typ string) (categories.Category, bool, error)
	SplitTargets(ctx context.Context, source uuid.UUID) ([]categories.SplitTarget, error)
}

// Service правит и удаляет транзакции.
type Service struct {
	store Store
	dir   Directory
}

func NewService(store Store, dir Directory) *Service { return &Service{store: store, dir: dir} }

// Input — правка. Version — updated_at, который страница получила вместе со строкой. Category пустая — категория
// остаётся как есть. Валюту, сумму, дату и название можно менять любые, но сумма и валюта идут вместе.
type Input struct {
	ID          uuid.UUID
	Version     time.Time
	Description string
	Amount      plan.Money
	Date        plan.Date
	Category    string
}

// Edit проверяет значения, выбирает категорию и записывает правку.
func (s *Service) Edit(ctx context.Context, in Input) (budget.TxRow, error) {
	if err := (plan.FactEdit{Name: in.Description, Amount: in.Amount, Date: in.Date}).Validate(); err != nil {
		return budget.TxRow{}, err
	}
	cur, err := s.store.GetTx(ctx, in.ID)
	if err != nil {
		return budget.TxRow{}, err
	}
	var catID *uuid.UUID
	if name := strings.TrimSpace(in.Category); name != "" {
		id, err := s.pickCategory(ctx, name, cur.Type)
		if err != nil {
			return budget.TxRow{}, err
		}
		catID = &id
	}
	return s.store.EditTx(ctx, in.ID, in.Version, budget.TxSourceWeb, budget.TxChange{
		Description: strings.TrimSpace(in.Description),
		AmountMinor: in.Amount.Minor,
		Currency:    string(in.Amount.Cur),
		Date:        time.Date(in.Date.Year, time.Month(in.Date.Month), in.Date.Day, 0, 0, 0, 0, time.UTC),
		CategoryID:  catID,
	})
}

// Delete удаляет транзакцию; версия защищает от удаления строки, которую успели изменить.
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version time.Time) error {
	return s.store.DeleteTx(ctx, id, version, budget.TxSourceWeb)
}

func (s *Service) pickCategory(ctx context.Context, name, typ string) (uuid.UUID, error) {
	cat, found, err := s.dir.Resolve(ctx, name, typ)
	if err != nil {
		return uuid.Nil, fmt.Errorf("txedit: категория: %w", err)
	}
	if !found {
		return uuid.Nil, ErrUnknownCategory
	}
	targets, err := s.dir.SplitTargets(ctx, cat.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("txedit: категория: %w", err)
	}
	if len(targets) > 0 {
		return uuid.Nil, ErrCategoryRetired
	}
	return cat.ID, nil
}
