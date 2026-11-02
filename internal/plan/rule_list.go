package plan

import (
	"context"
	"errors"
)

// RuleRecord — правило повтора из хранилища вместе с отметкой паузы. Snapshot.Rules
// отдаёт только включённые, поэтому приостановленные в список попадают отдельным портом.
type RuleRecord struct {
	RuleWithExceptions
	Paused bool
}

// RuleList — все правила области и курс для пересчёта «в месяц» (nil, если курса нет).
type RuleList struct {
	Rules []RuleRecord
	Rate  *Rate
}

// RuleLister читает все правила повтора области, включая приостановленные.
// Реализует internal/planrepo; курс отдаёт тем же чтением, чтобы список не тащил
// весь Snapshot ради одного числа.
type RuleLister interface {
	ListRules(ctx context.Context, sc Scope) (RuleList, error)
}

// WithRuleLister подключает чтение списка правил.
func WithRuleLister(l RuleLister) Option { return func(s *Service) { s.rules = l } }

// RuleRow — строка списка регулярных платежей.
type RuleRow struct {
	ID, Name, Category string
	Kind               Kind
	Rule               Rule
	Paused             bool
	// Invalid — правило не проходит проверку (битые данные или валюта не бат и не
	// рубль): строка остаётся в списке, чтобы владелец мог исправить или удалить её.
	Invalid bool
	// Next — ближайший срок не раньше сегодня с учётом пропусков; nil, если серия
	// закончилась (Until), на паузе или правило Invalid.
	Next *Date
	// Monthly — «в месяц» в целых единицах валюты показа (как Convert); nil, если
	// курса нет, а валюты разные.
	Monthly *int64
}

// MonthlyEquivalent приводит правило к сумме в месяц в валюте правила: день ×365/12,
// неделя ×52/12, месяц ÷N, год ÷12N. Округление одно, half-away-from-zero.
func MonthlyEquivalent(r Rule) (Money, error) {
	if err := r.check(); err != nil {
		return Money{}, err
	}
	num, den := int64(1), int64(r.N)
	switch r.Unit {
	case UnitDay:
		num, den = 365, 12*int64(r.N)
	case UnitWeek:
		num, den = 52, 12*int64(r.N)
	case UnitYear:
		den = 12 * int64(r.N)
	case UnitMonth:
	}
	// Сумма правила ограничена maxAmountMinor, ×365 помещается в int64.
	q := (2*r.Amount.Minor*num + den) / (2 * den)
	return Money{Minor: q, Cur: r.Amount.Cur}, nil
}

// nextTerm — ближайший срок не раньше today, пропущенные (Exceptions) не считаются.
func nextTerm(r RuleWithExceptions, today Date) (*Date, error) {
	after := today.AddDays(-1)
	for {
		t, ok, err := Next(r.Rule, after)
		if err != nil || !ok {
			return nil, err
		}
		if !isException(r.Exceptions, t) {
			return &t, nil
		}
		after = t
	}
}

// ListRules отдаёт все регулярные платежи области, включая приостановленные, с
// суммой «в месяц» в валюте показа cur.
func (s *Service) ListRules(ctx context.Context, cur Currency) ([]RuleRow, error) {
	if s.rules == nil {
		return nil, ErrRulesOff
	}
	list, err := s.rules.ListRules(ctx, s.scope)
	if err != nil {
		return nil, err
	}
	today := s.today()
	rows := make([]RuleRow, 0, len(list.Rules))
	for _, rec := range list.Rules {
		row := RuleRow{ID: rec.ID, Name: rec.Name, Category: rec.Category, Kind: rec.Kind, Rule: rec.Rule, Paused: rec.Paused}
		monthly, err := MonthlyEquivalent(rec.Rule)
		if err != nil {
			row.Invalid = true
			rows = append(rows, row)
			continue
		}
		if !rec.Paused {
			if row.Next, err = nextTerm(rec.RuleWithExceptions, today); err != nil {
				return nil, err
			}
		}
		// Нет курса — «в месяц» не показываем, остальное в списке остаётся.
		if minor, err := Convert(list.Rate, monthly, cur); err == nil {
			row.Monthly = &minor
		} else if !errors.Is(err, ErrNoRate) {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
