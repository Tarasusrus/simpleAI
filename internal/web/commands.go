package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"simpleAI/internal/plan"
)

const maxBodyBytes = 64 << 10

type incomeBody struct {
	Name     string      `json:"name"`
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
	Date     string      `json:"date"`
}

type paymentBody struct {
	Name     string       `json:"name"`
	Amount   *json.Number `json:"amount"`
	Currency string       `json:"currency"`
	Date     string       `json:"date"`
	Window   string       `json:"window"`
	Category string       `json:"category"`
}

// decode читает тело JSON: только application/json (415), не больше 64 КБ, неизвестные
// поля — ошибка (опечатка в имени поля не должна молча терять данные).
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Нужен Content-Type: application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "Не удалось разобрать запрос. Проверь поля.")
		return false
	}
	return true
}

// parseMinor переводит сумму из JSON (рубли или баты, не больше двух знаков после
// запятой) в минимальные единицы без float64: 1234.56 → 123456.
func parseMinor(n json.Number) (int64, error) {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return 0, fmt.Errorf("сумма %q", s)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 || whole == "" {
		return 0, fmt.Errorf("сумма %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	var minor int64
	for _, ch := range whole + frac {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("сумма %q", s)
		}
		if minor > (math.MaxInt64-9)/10 {
			return 0, fmt.Errorf("сумма %q слишком велика", s)
		}
		minor = minor*10 + int64(ch-'0')
	}
	return minor, nil
}

func parseMoney(amount json.Number, cur string) (plan.Money, error) {
	minor, err := parseMinor(amount)
	if err != nil {
		return plan.Money{}, plan.ErrAmountNonPositive
	}
	return plan.Money{Minor: minor, Cur: plan.Currency(strings.ToUpper(strings.TrimSpace(cur)))}, nil
}

func parseDate(s string) (plan.Date, error) {
	var y, m, d int
	if n, err := fmt.Sscanf(strings.TrimSpace(s), "%4d-%2d-%2d", &y, &m, &d); err != nil || n != 3 || len(strings.TrimSpace(s)) != 10 {
		return plan.Date{}, plan.ErrBadDate
	}
	return plan.NewDate(y, m, d)
}

func (s *Server) curFrom(r *http.Request) plan.Currency {
	if strings.EqualFold(r.URL.Query().Get("cur"), "THB") {
		return plan.CurTHB
	}
	return plan.CurRUB
}

// respondView отдаёт обновлённый WindowView после команды.
func (s *Server) respondView(w http.ResponseWriter, r *http.Request, status int) {
	cur := s.curFrom(r)
	res, err := s.planner.View(r.Context(), cur)
	if err != nil {
		s.writeViewError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(toWindowView(res, cur)) //nolint:errcheck // клиент мог уйти
}

// handleAddIncome: POST /api/plan/income — ожидаемое поступление.
func (s *Server) handleAddIncome(w http.ResponseWriter, r *http.Request) {
	var b incomeBody
	if !decode(w, r, &b) {
		return
	}
	money, err := parseMoney(b.Amount, b.Currency)
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	date, err := parseDate(b.Date)
	if err != nil {
		s.writeCommandError(w, plan.ErrIncomeNoDate)
		return
	}
	id, err := s.planner.AddIncome(r.Context(), plan.AddIncomeInput{Name: b.Name, Amount: money, Date: date})
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "income", "id", id, "added_by", "web")
	s.respondView(w, r, http.StatusCreated)
}

// handleAddPayment: POST /api/plan/payment — плановый платёж (с датой, в окно или без даты).
func (s *Server) handleAddPayment(w http.ResponseWriter, r *http.Request) {
	var b paymentBody
	if !decode(w, r, &b) {
		return
	}
	in := plan.AddPaymentInput{Name: b.Name, Window: plan.WindowChoice(b.Window), Category: b.Category}
	if b.Amount != nil && strings.TrimSpace(b.Amount.String()) != "" {
		money, err := parseMoney(*b.Amount, b.Currency)
		if err != nil {
			s.writeCommandError(w, err)
			return
		}
		in.Amount = &money
	}
	if strings.TrimSpace(b.Date) != "" {
		d, err := parseDate(b.Date)
		if err != nil {
			s.writeCommandError(w, err)
			return
		}
		in.Date = &d
	}
	id, err := s.planner.AddPayment(r.Context(), in)
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "payment", "id", id, "added_by", "web")
	s.respondView(w, r, http.StatusCreated)
}

// handleDelete: DELETE /api/plan/item/{key} — удалить плановую запись.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.planner.Delete(r.Context(), key); err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "delete", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleCategories: GET /api/plan/categories — категории расходов для формы.
func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	names, err := s.planner.ExpenseCategories(r.Context())
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, map[string]any{"categories": names})
}

// writeCommandError переводит ошибку домена в понятный ответ; детали внутренних
// ошибок наружу не уходят.
func (s *Server) writeCommandError(w http.ResponseWriter, err error) {
	bad := map[error]string{
		plan.ErrNameRequired:        "Нужно название.",
		plan.ErrNameTooLong:         "Название слишком длинное.",
		plan.ErrAmountNonPositive:   "Сумма должна быть числом больше нуля (не больше двух знаков после запятой).",
		plan.ErrAmountRequired:      "Нужна сумма.",
		plan.ErrIncomeNoDate:        "У поступления должна быть дата.",
		plan.ErrBadDate:             "Некорректная дата. Нужен формат ГГГГ-ММ-ДД.",
		plan.ErrUnsupportedCurrency: "Валюта: RUB или THB.",
		plan.ErrNoWindow:            "Открытого окна нет: сначала добавь ожидаемое поступление с датой.",
		plan.ErrInvalidEvent:        "Укажи либо дату, либо окно, а не оба сразу.",
	}
	for target, msg := range bad {
		if errors.Is(err, target) {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	switch {
	case errors.Is(err, plan.ErrNotFound):
		writeError(w, http.StatusNotFound, "Запись не найдена.")
	case errors.Is(err, plan.ErrReadOnly):
		writeError(w, http.StatusForbidden, "Запись отключена.")
	default:
		s.logger.Error("plan web: command", "err", err)
		writeError(w, http.StatusInternalServerError, "Не удалось сохранить. Попробуй ещё раз.")
	}
}
