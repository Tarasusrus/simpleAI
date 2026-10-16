package plan

import "errors"

// Ошибки домена. Вызывающий код различает их через errors.Is.
var (
	ErrUnsupportedCurrency = errors.New("plan: валюта не поддерживается")
	ErrBadRate             = errors.New("plan: некорректный курс")
	ErrNoRate              = errors.New("plan: нет курса для перевода между валютами")
	ErrOverflow            = errors.New("plan: переполнение при пересчёте денег")
	ErrBadRule             = errors.New("plan: некорректное правило повтора")
	ErrTooMany             = errors.New("plan: слишком много событий повтора")
	ErrAmountRequired      = errors.New("plan: нужна сумма")
	ErrAmountNonPositive   = errors.New("plan: сумма должна быть больше нуля")
	ErrIncomeNoDate        = errors.New("plan: у поступления должна быть дата")
	ErrInvalidEvent        = errors.New("plan: некорректное событие")
	ErrDuplicateTerm       = errors.New("plan: у одного срока правила и план, и факт")
	ErrBadReservePct       = errors.New("plan: процент резерва вне 0..100")
)
