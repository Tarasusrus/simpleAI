package web

import (
	"context"
	"errors"
	"net/http"

	"simpleAI/internal/plan"
)

// Список регулярных платежей: только чтение. Суммы «в месяц» и ближайшие сроки считает домен,
// BFF раскладывает готовые строки по полям.

// RuleViewer — то, что вебу нужно от plan.Service для списка регулярных платежей.
type RuleViewer interface {
	ListRules(ctx context.Context, cur plan.Currency) ([]plan.RuleRow, error)
}

// WithRuleViewer подключает список регулярных платежей. Без него маршрут отвечает 404.
func (s *Server) WithRuleViewer(v RuleViewer) *Server {
	s.ruleView = v
	return s
}

// RuleDTO — строка списка. Amount — сумма одного платежа в СВОЕЙ валюте (Currency), Monthly — «в месяц» в
// валюте показа; null, если курса нет. Next — null у приостановленного и у неверного правила.
type RuleDTO struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Kind     string  `json:"kind"`
	Amount   string  `json:"amount"`
	Currency string  `json:"currency"`
	Every    int     `json:"every"`
	Unit     string  `json:"unit"`
	Until    *string `json:"until"`
	Paused   bool    `json:"paused"`
	Invalid  bool    `json:"invalid"`
	Next     *string `json:"next"`
	Monthly  *int64  `json:"monthly"`
}

// RulesDTO — ответ GET /api/plan/recurring. Total — сумма «в месяц» по действующим расходам, у которых она
// посчитана; Missing — сколько действующих расходов в неё не вошли (нет курса).
type RulesDTO struct {
	Rows    []RuleDTO `json:"rows"`
	Total   int64     `json:"total"`
	Missing int       `json:"missing"`
}

func ruleDTO(r plan.RuleRow) RuleDTO {
	d := RuleDTO{
		ID: r.ID, Name: r.Name, Category: r.Category, Kind: string(r.Kind),
		Amount: decimal(r.Rule.Amount.Minor), Currency: string(r.Rule.Amount.Cur),
		Every: r.Rule.N, Unit: string(r.Rule.Unit), Paused: r.Paused, Invalid: r.Invalid, Monthly: r.Monthly,
	}
	if r.Rule.Until != nil {
		u := dateStr(*r.Rule.Until)
		d.Until = &u
	}
	if r.Next != nil {
		n := dateStr(*r.Next)
		d.Next = &n
	}
	return d
}

// handleRules: GET /api/plan/recurring?cur=RUB|THB
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	if s.ruleView == nil {
		writeError(w, http.StatusNotFound, "Список регулярных платежей не включён.")
		return
	}
	rows, err := s.ruleView.ListRules(r.Context(), s.curFrom(r))
	if errors.Is(err, plan.ErrRulesOff) {
		writeError(w, http.StatusNotFound, "Список регулярных платежей не включён.")
		return
	}
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	out := RulesDTO{Rows: make([]RuleDTO, 0, len(rows))}
	for _, row := range rows {
		out.Rows = append(out.Rows, ruleDTO(row))
		if row.Kind != plan.KindPayment || row.Paused || row.Invalid {
			continue
		}
		if row.Monthly == nil {
			out.Missing++
			continue
		}
		out.Total += *row.Monthly
	}
	writeJSON(w, out)
}

func (s *Server) registerRuleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/plan/recurring", s.auth(http.HandlerFunc(s.handleRules)))
}
