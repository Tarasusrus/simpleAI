// Package receipt — сценарий «фото чека → черновик расхода» (ADR-014).
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
	// Reference — номер транзакции или платежа с подтверждения оплаты (приоритет у «Transaction ID»), приведённый
	// NormalizeReference; пусто, если на фото его нет. По нему один и тот же чек в двух видах не пишется дважды.
	Reference string
	Items     []Item
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

// ErrDuplicate — чек с таким номером транзакции уже записан.
var ErrDuplicate = errors.New("receipt: этот чек уже записан")

// SeenStore помнит номера уже записанных чеков по чату. Claim атомарен: из двух одновременных подтверждений одного
// чека true получит только одно. Release снимает отметку, когда запись расхода не удалась и чек можно прислать снова.
type SeenStore interface {
	Seen(ctx context.Context, chatID int64, reference string) (bool, error)
	Claim(ctx context.Context, chatID int64, reference string) (bool, error)
	Release(ctx context.Context, chatID int64, reference string) error
}

// ErrDraftNotFound — черновика нет: истёк, уже записан или отменён, бот
// перезапускался, или черновик чужого чата.
var ErrDraftNotFound = errors.New("receipt: черновик не найден или устарел")

// DraftStore держит разобранный чек до подтверждения. Take атомарен:
// второе нажатие той же кнопки получает ErrDraftNotFound, а не второй расход.
type DraftStore interface {
	Put(chatID int64, r Receipt) (id string, err error)
	// Take отдаёт и удаляет черновик. Черновик чужого чата не отдаётся и
	// не удаляется.
	Take(chatID int64, id string) (Receipt, error)
}

// CategoryLister — существующие категории расхода, из которых Parser
// выбирает категорию чека.
type CategoryLister interface {
	ExpenseCategories(ctx context.Context) ([]string, error)
}

// ExpenseWriter записывает подтверждённый чек как расход в бюджет чата и
// возвращает ответ для пользователя (вместе с предупреждением о пробитом
// конверте, ADR-008).
type ExpenseWriter interface {
	WriteExpense(ctx context.Context, chatID int64, r Receipt) (string, error)
}
