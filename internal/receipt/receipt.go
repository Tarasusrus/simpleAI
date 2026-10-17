// Package receipt — сценарий «фото чека → черновик расхода» (ADR-010).
// Зависит только от core и стандартной библиотеки: Telegram, budget и
// провайдеры LLM подключаются адаптерами через порты этого пакета.
package receipt

import (
	"context"
	"errors"

	"simpleAI/internal/receipt/filecheck"
)

// Receipt — разобранный чек. Только поля, без байтов картинки.
type Receipt struct {
	Merchant string
	// Date — YYYY-MM-DD; пусто, если на чеке нет даты.
	Date     string
	Currency string
	Total    float64
	// Category — одна из существующих категорий расхода или пусто.
	Category string
	Items    []Item
}

type Item struct {
	Name   string
	Amount float64
}

var (
	// ErrNotReceipt — на фото не чек.
	ErrNotReceipt = errors.New("receipt: на фото не чек")
	// ErrUnreadable — модель ответила не по формату; что на фото, неизвестно.
	ErrUnreadable = errors.New("receipt: не удалось разобрать ответ модели")
	// ErrNoCurrency — валюта не определена: без неё запись молча станет RUB.
	ErrNoCurrency = errors.New("receipt: на чеке не определена валюта")
	// ErrNoTotal — нет положительной итоговой суммы.
	ErrNoTotal = errors.New("receipt: на чеке не найдена итоговая сумма")
)

// Classifier решает, чек ли на фото. Отдельный порт, чтобы дешёвую проверку
// можно было заменить (например, локальной моделью), не трогая Service.
type Classifier interface {
	IsReceipt(ctx context.Context, f filecheck.File) (bool, error)
}

// Parser разбирает чек. categories — существующие категории расхода:
// Parser выбирает только из них, иначе запись создала бы новую категорию.
type Parser interface {
	Parse(ctx context.Context, f filecheck.File, categories []string) (Receipt, error)
}
