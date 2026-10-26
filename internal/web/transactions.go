package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"simpleAI/internal/plan"
	"simpleAI/internal/txedit"
)

// Страница «Транзакции» (ADR-018): список, одна строка, правка и удаление любой транзакции. Деньги и даты тут не
// считаются: BFF разбирает тело запроса и отдаёт строки как есть, правила лежат в txedit и хранилище.

// TxEditor — то, что вебу нужно от txedit.Service (ISP).
type TxEditor interface {
	Get(ctx context.Context, id string) (txedit.Tx, error)
	List(ctx context.Context, q txedit.ListQuery) (txedit.Page, error)
	Around(ctx context.Context, id string) (txedit.Page, error)
	Edit(ctx context.Context, in txedit.Input) (txedit.Tx, error)
	Delete(ctx context.Context, id, version string) error
}

// WithTransactions подключает страницу «Транзакции». Без неё маршруты отвечают 404.
func (s *Server) WithTransactions(e TxEditor) *Server {
	s.txEditor = e
	return s
}

// TxRowDTO — транзакция на странице. Amount — десятичная строка в СВОЕЙ валюте (Currency), без пересчёта: форма
// правки получает то же, что присылает обратно (как у GET /api/plan/item/{key}).
type TxRowDTO struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Date        string `json:"date"`
	Version     string `json:"version"`
	Linked      bool   `json:"linked"`
	FromRule    bool   `json:"fromRule"`
	AddedBy     string `json:"addedBy"`
	// OpensWindow — у прихода: открывает окно (зарплата, аванс) или нет (обычный приход, ADR-019); у расхода false.
	OpensWindow bool `json:"opensWindow"`
}

// TxPageDTO — страница списка. Focus — id строки, на которую открыли страницу по ссылке (пусто — обычный список).
type TxPageDTO struct {
	Rows  []TxRowDTO `json:"rows"`
	Next  string     `json:"next"`
	Focus string     `json:"focus,omitempty"`
}

func txRow(t txedit.Tx) TxRowDTO {
	return TxRowDTO{
		ID: t.ID, Kind: t.Kind, Amount: decimal(t.Amount.Minor), Currency: string(t.Amount.Cur), Category: t.Category,
		Description: t.Description, Date: dateStr(t.Date), Version: t.Version,
		Linked: t.Linked, FromRule: t.FromRule, AddedBy: t.AddedBy, OpensWindow: t.OpensWindow,
	}
}

func txPage(p txedit.Page, focus string) TxPageDTO {
	out := TxPageDTO{Rows: make([]TxRowDTO, 0, len(p.Rows)), Next: p.Next, Focus: focus}
	for _, t := range p.Rows {
		out.Rows = append(out.Rows, txRow(t))
	}
	return out
}

func (s *Server) txOn(w http.ResponseWriter) bool {
	if s.txEditor == nil {
		writeError(w, http.StatusNotFound, "Страница транзакций не включена.")
		return false
	}
	return true
}

// handleTxList: GET /api/plan/transactions?text=&category=&from=&to=&cursor=&limit=&focus=<id>.
// С focus отдаётся окно вокруг этой строки, остальные фильтры игнорируются: ссылка ведёт на конкретную транзакцию.
func (s *Server) handleTxList(w http.ResponseWriter, r *http.Request) {
	if !s.txOn(w) {
		return
	}
	q := r.URL.Query()
	if focus := strings.TrimSpace(q.Get("focus")); focus != "" {
		page, err := s.txEditor.Around(r.Context(), focus)
		if err != nil {
			s.writeTxError(w, err)
			return
		}
		writeJSON(w, txPage(page, focus))
		return
	}
	lq := txedit.ListQuery{Text: q.Get("text"), Category: q.Get("category"), Cursor: q.Get("cursor")}
	for param, dst := range map[string]**plan.Date{"from": &lq.From, "to": &lq.To} {
		if v := strings.TrimSpace(q.Get(param)); v != "" {
			d, err := plan.ParseDate(v)
			if err != nil {
				s.writeTxError(w, err)
				return
			}
			*dst = &d
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit: целое число больше нуля.")
			return
		}
		lq.Limit = n
	}
	page, err := s.txEditor.List(r.Context(), lq)
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	writeJSON(w, txPage(page, ""))
}

// handleTxGet: GET /api/plan/transactions/{id}.
func (s *Server) handleTxGet(w http.ResponseWriter, r *http.Request) {
	if !s.txOn(w) {
		return
	}
	t, err := s.txEditor.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	writeJSON(w, txRow(t))
}

// txEditBody — все поля обязательны, кроме category (пусто — категория не меняется): правка всегда присылает
// строку целиком, чтобы клиент не гадал, что останется прежним.
type txEditBody struct {
	Version     string      `json:"version"`
	Description string      `json:"description"`
	Amount      json.Number `json:"amount"`
	Currency    string      `json:"currency"`
	Date        string      `json:"date"`
	Category    string      `json:"category"`
	// OpensWindow — только у прихода; нет поля — вид не меняется.
	OpensWindow *bool `json:"opensWindow"`
}

var errTxFields = errors.New("web: нужны version, description, amount, currency, date")

func (b txEditBody) input(id string) (txedit.Input, error) {
	if b.Version == "" || b.Amount == "" || b.Currency == "" || b.Date == "" {
		return txedit.Input{}, errTxFields
	}
	minor, err := parseMinor(b.Amount)
	if err != nil {
		return txedit.Input{}, plan.ErrAmountNonPositive
	}
	d, err := plan.ParseDate(b.Date)
	if err != nil {
		return txedit.Input{}, err
	}
	return txedit.Input{
		ID: id, Version: b.Version, Description: b.Description, Category: b.Category, OpensWindow: b.OpensWindow,
		Amount: plan.Money{Minor: minor, Cur: plan.Currency(strings.ToUpper(strings.TrimSpace(b.Currency)))}, Date: d,
	}, nil
}

// handleTxEdit: PATCH /api/plan/transactions/{id}. Отвечает строкой после правки (с новой версией).
func (s *Server) handleTxEdit(w http.ResponseWriter, r *http.Request) {
	if !s.txOn(w) {
		return
	}
	var b txEditBody
	if !decode(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	in, err := b.input(id)
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	t, err := s.txEditor.Edit(r.Context(), in)
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "tx-edit", "id", id, "added_by", "web")
	writeJSON(w, txRow(t))
}

// handleTxDelete: DELETE /api/plan/transactions/{id}?version=<версия строки>.
func (s *Server) handleTxDelete(w http.ResponseWriter, r *http.Request) {
	if !s.txOn(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.txEditor.Delete(r.Context(), id, r.URL.Query().Get("version")); err != nil {
		s.writeTxError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "tx-delete", "id", id, "added_by", "web")
	w.WriteHeader(http.StatusNoContent)
}

// writeTxError: свои причины страницы транзакций, остальное — как у прочих команд плана.
func (s *Server) writeTxError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, txedit.ErrNotFound):
		writeError(w, http.StatusNotFound, "Транзакции нет: возможно, её уже удалили.")
	case errors.Is(err, txedit.ErrConflict):
		writeError(w, http.StatusConflict, "Транзакцию изменили, пока вы её правили. Обновите страницу.")
	case errors.Is(err, txedit.ErrBadID), errors.Is(err, txedit.ErrBadVersion), errors.Is(err, txedit.ErrBadCursor):
		writeError(w, http.StatusBadRequest, "Неверный id, версия или курсор.")
	case errors.Is(err, txedit.ErrUnknownCategory):
		writeError(w, http.StatusBadRequest, "Такой категории нет. Выберите из списка.")
	case errors.Is(err, txedit.ErrNotIncome):
		writeError(w, http.StatusBadRequest, "Вид «открывает окно» есть только у прихода.")
	case errors.Is(err, txedit.ErrCategoryRetired):
		writeError(w, http.StatusBadRequest, "Эта категория разделена на другие. Выберите одну из них.")
	case errors.Is(err, errTxFields):
		writeError(w, http.StatusBadRequest, "Нужны поля version, description, amount, currency, date (category по желанию).")
	default:
		s.writeCommandError(w, err)
	}
}

func (s *Server) registerTxRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/plan/transactions", s.auth(http.HandlerFunc(s.handleTxList)))
	mux.Handle("GET /api/plan/transactions/{id}", s.auth(http.HandlerFunc(s.handleTxGet)))
	mux.Handle("PATCH /api/plan/transactions/{id}", s.auth(http.HandlerFunc(s.handleTxEdit)))
	mux.Handle("DELETE /api/plan/transactions/{id}", s.auth(http.HandlerFunc(s.handleTxDelete)))
}
