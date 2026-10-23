package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"simpleAI/internal/plan"
)

// Лимиты категорий (ADR-011). Отдельный интерфейс и подключение через WithLimits:
// лимиты — самостоятельная часть плана, Planner они не раздувают.

// LimitPlanner — то, что вебу нужно от plan.Service для лимитов.
type LimitPlanner interface {
	AddLimit(ctx context.Context, in plan.AddLimitInput) (string, error)
	EditLimit(ctx context.Context, id string, e plan.LimitEdit) error
	DeleteLimit(ctx context.Context, id string) error
	PreviewLimit(ctx context.Context, cur plan.Currency, in plan.PreviewLimitInput) (plan.LimitPreview, error)
}

// WithLimits подключает команды лимитов и подсказку суммы. Без него маршруты лимитов отвечают 404.
func (s *Server) WithLimits(p LimitPlanner, sg plan.LimitSuggester) *Server {
	s.limits, s.suggester = p, sg
	return s
}

// CategoryHider — то, что вебу нужно от plan.Service для «Не планировать» (ADR-015 §3).
type CategoryHider interface {
	DismissCategory(ctx context.Context, category string) error
	RestoreCategory(ctx context.Context, category string) error
}

// WithCategoryHider подключает скрытие категорий. Без него маршруты отвечают 404.
func (s *Server) WithCategoryHider(h CategoryHider) *Server {
	s.hider = h
	return s
}

// LimitDTO — лимит окна в валюте показа; все числа готовы, фронт ничего не считает.
type LimitDTO struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Repeat    bool   `json:"repeat"`
	Amount    int64  `json:"amount"`
	Spent     int64  `json:"spent"`
	Planned   int64  `json:"planned"`
	Left      int64  `json:"left"`
	Over      int64  `json:"over"`
	LeftPct   int    `json:"leftPct"`
	Attention bool   `json:"attention"`
}

func toLimitDTO(l plan.LimitView) LimitDTO {
	return LimitDTO{ID: l.ID, Category: l.Category, Repeat: l.Repeat, Amount: l.Amount, Spent: l.Spent,
		Planned: l.Planned, Left: l.Left, Over: l.Over, LeftPct: l.LeftPct, Attention: l.Attention}
}

func limitDTOs(in []plan.LimitView) []LimitDTO {
	out := make([]LimitDTO, 0, len(in)) // пустой список — [], а не null
	for _, l := range in {
		out = append(out, toLimitDTO(l))
	}
	return out
}

type limitBody struct {
	Category string      `json:"category"`
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
	// Repeat обязателен: забытое поле не должно молча сделать повторяющийся лимит разовым.
	Repeat *bool `json:"repeat"`
}

var errLimitFields = errors.New("web: у лимита нет обязательного поля repeat")

type previewBody struct {
	ID string `json:"id"`
	limitBody
}

// limitsOn отвечает 404, если лимиты не подключены.
func (s *Server) limitsOn(w http.ResponseWriter) bool {
	if s.limits == nil {
		writeError(w, http.StatusNotFound, "Лимиты не включены.")
		return false
	}
	return true
}

// handleAddLimit: POST /api/plan/limits — лимит на категорию в окне, которое на экране.
func (s *Server) handleAddLimit(w http.ResponseWriter, r *http.Request) {
	if !s.limitsOn(w) {
		return
	}
	var b limitBody
	if !decode(w, r, &b) {
		return
	}
	money, err := parseMoney(b.Amount, b.Currency)
	if err == nil && b.Repeat == nil {
		err = errLimitFields
	}
	if err != nil {
		s.writeLimitError(w, err)
		return
	}
	id, err := s.limits.AddLimit(r.Context(), plan.AddLimitInput{Category: b.Category, Amount: money, Repeat: *b.Repeat})
	if err != nil {
		s.writeLimitError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "limit_add", "id", id, "added_by", "web")
	s.respondViewWith(w, r, http.StatusCreated, func(v *WindowView) { v.LimitID = id })
}

// handleEditLimit: PUT /api/plan/limits/{id} — сумма, валюта и повтор (категория не меняется).
func (s *Server) handleEditLimit(w http.ResponseWriter, r *http.Request) {
	if !s.limitsOn(w) {
		return
	}
	var b limitBody
	if !decode(w, r, &b) {
		return
	}
	money, err := parseMoney(b.Amount, b.Currency)
	if err == nil && b.Repeat == nil {
		err = errLimitFields
	}
	if err != nil {
		s.writeLimitError(w, err)
		return
	}
	id := r.PathValue("id")
	if err := s.limits.EditLimit(r.Context(), id, plan.LimitEdit{Amount: money, Repeat: *b.Repeat}); err != nil {
		s.writeLimitError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "limit_edit", "id", id, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleDeleteLimit: DELETE /api/plan/limits/{id}.
func (s *Server) handleDeleteLimit(w http.ResponseWriter, r *http.Request) {
	if !s.limitsOn(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.limits.DeleteLimit(r.Context(), id); err != nil {
		s.writeLimitError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "limit_delete", "id", id, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// PreviewDTO — эффект лимита на «свободно», в валюте показа. Ничего не сохраняется.
type PreviewDTO struct {
	CanSpendBefore  int64     `json:"canSpendBefore"`
	CanSpendAfter   int64     `json:"canSpendAfter"`
	LimitHoldBefore int64     `json:"limitHoldBefore"`
	LimitHoldAfter  int64     `json:"limitHoldAfter"`
	ExceedsFree     bool      `json:"exceedsFree"`
	Limit           *LimitDTO `json:"limit"`
}

// handlePreviewLimit: POST /api/plan/limits/preview — «что будет, если». Тело как у
// создания; с id — правка существующего лимита.
func (s *Server) handlePreviewLimit(w http.ResponseWriter, r *http.Request) {
	if !s.limitsOn(w) {
		return
	}
	var b previewBody
	if !decode(w, r, &b) {
		return
	}
	money, err := parseMoney(b.Amount, b.Currency)
	if err == nil && b.Repeat == nil {
		err = errLimitFields
	}
	if err != nil {
		s.writeLimitError(w, err)
		return
	}
	cur := s.curFrom(r)
	pv, err := s.limits.PreviewLimit(r.Context(), cur, plan.PreviewLimitInput{ID: b.ID, Category: b.Category, Amount: money, Repeat: *b.Repeat})
	if err != nil {
		s.writeLimitError(w, err)
		return
	}
	out := PreviewDTO{CanSpendBefore: pv.CanSpendBefore, CanSpendAfter: pv.CanSpendAfter,
		LimitHoldBefore: pv.LimitHoldBefore, LimitHoldAfter: pv.LimitHoldAfter, ExceedsFree: pv.ExceedsFree}
	if pv.Limit != nil {
		l := toLimitDTO(*pv.Limit)
		out.Limit = &l
	}
	writeJSON(w, out)
}

// SuggestDTO — подсказка суммы: целые единицы валюты показа; amount null, когда данных мало.
type SuggestDTO struct {
	Enough bool   `json:"enough"`
	Months int    `json:"months"`
	Amount *int64 `json:"amount"`
	Cur    string `json:"cur"`
	Days   int    `json:"days"`
	// AvgCount — трат категории за окно в среднем; 0, если истории мало (ADR-015 §4).
	AvgCount int `json:"avgCount"`
}

// handleSuggestLimit: GET /api/plan/limits/suggest?category=…&cur=… — среднее по категории
// за всю историю, пересчитанное на длину окна. Нет данных или курса — amount null.
func (s *Server) handleSuggestLimit(w http.ResponseWriter, r *http.Request) {
	if !s.limitsOn(w) {
		return
	}
	if s.suggester == nil {
		writeError(w, http.StatusNotFound, "Подсказка суммы не включена.")
		return
	}
	cat := strings.TrimSpace(r.URL.Query().Get("category"))
	if cat == "" {
		writeError(w, http.StatusBadRequest, "Нужна категория.")
		return
	}
	cur := s.curFrom(r)
	sug, err := s.suggester.Suggest(r.Context(), cat, plan.WindowDays)
	if err != nil {
		s.logger.Error("plan web: suggest", "err", err)
		writeError(w, http.StatusInternalServerError, "Не удалось посчитать подсказку.")
		return
	}
	out := SuggestDTO{Enough: false, Months: sug.Months, Cur: string(cur), Days: plan.WindowDays, AvgCount: sug.AvgCount}
	if sug.Enough {
		amount, ok := s.suggestionIn(r, cur, sug.AmountTHBMinor)
		if ok {
			out.Enough, out.Amount = true, &amount
		}
	}
	writeJSON(w, out)
}

// suggestionIn переводит сатанги подсказки в целые единицы валюты показа. Курс берётся
// из того же расчёта, что у экрана; нет курса при показе в рублях — подсказки нет.
func (s *Server) suggestionIn(r *http.Request, cur plan.Currency, thbMinor int64) (int64, bool) {
	var rate *plan.Rate
	if cur != plan.CurTHB {
		res, err := s.planner.ViewWith(r.Context(), cur, plan.ViewOptions{})
		if err != nil || !res.Meta.HasRate {
			return 0, false
		}
		rate = &plan.Rate{RubPerThbMicro: res.Meta.RubPerThbMicro}
	}
	n, err := plan.Convert(rate, plan.Money{Minor: thbMinor, Cur: plan.CurTHB}, cur)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// writeLimitError: свои причины лимитов, остальное — как у прочих команд.
func (s *Server) writeLimitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plan.ErrLimitExists):
		writeError(w, http.StatusConflict, "На эту категорию в этом окне лимит уже есть. Измени его.")
	case errors.Is(err, errLimitFields):
		writeError(w, http.StatusBadRequest, "Нужны поля category (только при создании), amount, currency и repeat.")
	case errors.Is(err, plan.ErrLimitCategory):
		writeError(w, http.StatusBadRequest, "Выбери существующую категорию расходов (не «Без категории»).")
	default:
		s.writeCommandError(w, err)
	}
}

type dismissBody struct {
	Category string `json:"category"`
}

// hiderOn отвечает 404, если лимиты или скрытие не подключены.
func (s *Server) hiderOn(w http.ResponseWriter) bool {
	if !s.limitsOn(w) {
		return false
	}
	if s.hider == nil {
		writeError(w, http.StatusNotFound, "Скрытие категорий не включено.")
		return false
	}
	return true
}

// handleDismissCategory: POST /api/plan/categories/dismissed {category} — «Не планировать».
func (s *Server) handleDismissCategory(w http.ResponseWriter, r *http.Request) {
	if !s.hiderOn(w) {
		return
	}
	var b dismissBody
	if !decode(w, r, &b) {
		return
	}
	if err := s.hider.DismissCategory(r.Context(), b.Category); err != nil {
		s.writeLimitError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "category_dismiss", "category", b.Category, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleRestoreCategory: DELETE /api/plan/categories/dismissed?category=… — вернуть в блок.
func (s *Server) handleRestoreCategory(w http.ResponseWriter, r *http.Request) {
	if !s.hiderOn(w) {
		return
	}
	cat := strings.TrimSpace(r.URL.Query().Get("category"))
	if err := s.hider.RestoreCategory(r.Context(), cat); err != nil {
		s.writeLimitError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "category_restore", "category", cat, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

func (s *Server) registerLimitRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/plan/categories/dismissed", s.auth(http.HandlerFunc(s.handleDismissCategory)))
	mux.Handle("DELETE /api/plan/categories/dismissed", s.auth(http.HandlerFunc(s.handleRestoreCategory)))
	mux.Handle("POST /api/plan/limits", s.auth(http.HandlerFunc(s.handleAddLimit)))
	mux.Handle("POST /api/plan/limits/preview", s.auth(http.HandlerFunc(s.handlePreviewLimit)))
	mux.Handle("GET /api/plan/limits/suggest", s.auth(http.HandlerFunc(s.handleSuggestLimit)))
	mux.Handle("PUT /api/plan/limits/{id}", s.auth(http.HandlerFunc(s.handleEditLimit)))
	mux.Handle("DELETE /api/plan/limits/{id}", s.auth(http.HandlerFunc(s.handleDeleteLimit)))
}
