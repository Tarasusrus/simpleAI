package web

import (
	"errors"
	"net/http"
	"strings"

	"simpleAI/internal/plan"
)

// handleWindow: GET /api/plan/window?cur=RUB|THB → WindowView.
func (s *Server) handleWindow(w http.ResponseWriter, r *http.Request) {
	cur := plan.CurRUB
	switch strings.ToUpper(r.URL.Query().Get("cur")) {
	case "", "RUB":
	case "THB":
		cur = plan.CurTHB
	default:
		writeError(w, http.StatusBadRequest, "Валюта показа: RUB или THB.")
		return
	}
	res, err := s.viewFor(r, cur)
	if err != nil {
		s.writeViewError(w, err)
		return
	}
	writeJSON(w, toWindowView(res, cur))
}

// writeViewError: понятные причины — 400 по-русски, остальное — 500 без деталей.
func (s *Server) writeViewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plan.ErrNoRate):
		writeError(w, http.StatusBadRequest, "Нет курса бата. Скажи боту «курс 2,7» — и план посчитается.")
	case errors.Is(err, plan.ErrDuplicateTerm):
		s.logger.Error("plan web: дубль срока", "err", err)
		writeError(w, http.StatusInternalServerError, "В данных есть дубль платежа по одному сроку. Нужно разобрать вручную.")
	default:
		s.logger.Error("plan web: view", "err", err)
		writeError(w, http.StatusInternalServerError, "Не удалось посчитать. Попробуй ещё раз.")
	}
}
