package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
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

func (s *Server) curFrom(r *http.Request) plan.Currency {
	if strings.EqualFold(r.URL.Query().Get("cur"), "THB") {
		return plan.CurTHB
	}
	return plan.CurRUB
}

// viewFor считает окно; ?past=1 показывает закончившееся последнее окно, ?avg=N —
// за сколько дней считать «обычные» траты (7..730, по умолчанию 90).
func (s *Server) viewFor(r *http.Request, cur plan.Currency) (plan.Result, error) {
	opt := plan.ViewOptions{ShowPast: r.URL.Query().Get("past") == "1"}
	if a := r.URL.Query().Get("avg"); a != "" {
		n, err := strconv.Atoi(a)
		if err != nil {
			return plan.Result{}, plan.ErrBadAvgDays
		}
		opt.AvgDays = n
	}
	return s.planner.ViewWith(r.Context(), cur, opt)
}

// respondView отдаёт обновлённый WindowView после команды.
func (s *Server) respondView(w http.ResponseWriter, r *http.Request, status int) {
	cur := s.curFrom(r)
	res, err := s.viewFor(r, cur)
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
	date, err := plan.ParseDate(b.Date)
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
		d, err := plan.ParseDate(b.Date)
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

type doneBody struct {
	Amount   *json.Number `json:"amount"`
	Currency string       `json:"currency"`
}

// handleDone: POST /api/plan/item/{key}/done — «оплачено» / «пришло». Тело можно
// не слать: сумма берётся из записи. Нужна она только у записи без суммы.
func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	var b doneBody
	if r.ContentLength != 0 && !decode(w, r, &b) {
		return
	}
	var actual *plan.Money
	if b.Amount != nil && strings.TrimSpace(b.Amount.String()) != "" {
		money, err := parseMoney(*b.Amount, b.Currency)
		if err != nil {
			s.writeCommandError(w, err)
			return
		}
		actual = &money
	}
	key := r.PathValue("key")
	if err := s.planner.Settle(r.Context(), key, actual); err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "done", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

type moveBody struct {
	Date   string `json:"date"`
	Window string `json:"window"`
}

// handleMove: POST /api/plan/item/{key}/move — перенести плановый платёж: на дату,
// в текущее/следующее окно или в «без даты» (пустое тело).
func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var b moveBody
	if !decode(w, r, &b) {
		return
	}
	in := plan.MoveInput{Window: plan.WindowChoice(b.Window)}
	if strings.TrimSpace(b.Date) != "" {
		d, err := plan.ParseDate(b.Date)
		if err != nil {
			s.writeCommandError(w, err)
			return
		}
		in.Date = &d
	}
	key := r.PathValue("key")
	if err := s.planner.Move(r.Context(), key, in); err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "move", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleSkip: POST /api/plan/item/{key}/skip — пропустить этот срок повтора.
func (s *Server) handleSkip(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.planner.Skip(r.Context(), key); err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "skip", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

type rateBody struct {
	Rate json.Number `json:"rate"`
}

// parseMicro переводит курс из JSON (не больше шести знаков после запятой) в
// микроединицы без float64: 2.5 → 2_500_000.
func parseMicro(n json.Number) (int64, error) {
	s := strings.TrimSpace(n.String())
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 6 || whole == "" {
		return 0, plan.ErrBadRate
	}
	for len(frac) < 6 {
		frac += "0"
	}
	var micro int64
	for _, ch := range whole + frac {
		if ch < '0' || ch > '9' {
			return 0, plan.ErrBadRate
		}
		if micro > 1_000_000_000_000 { // заведомо больше потолка, до переполнения далеко
			return 0, plan.ErrBadRate
		}
		micro = micro*10 + int64(ch-'0')
	}
	if micro == 0 {
		return 0, plan.ErrBadRate
	}
	return micro, nil
}

// handleRate: POST /api/plan/rate — ручной курс бата.
func (s *Server) handleRate(w http.ResponseWriter, r *http.Request) {
	var b rateBody
	if !decode(w, r, &b) {
		return
	}
	micro, err := parseMicro(b.Rate)
	if err == nil {
		err = s.planner.SetRate(r.Context(), micro)
	}
	if err != nil {
		s.writeCommandError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "rate", "added_by", "web")
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
		plan.ErrAmountTooLarge:      "Сумма слишком большая: проверь, нет ли лишних цифр.",
		plan.ErrIncomeNoDate:        "У поступления должна быть дата.",
		plan.ErrBadDate:             "Некорректная дата. Нужен формат ГГГГ-ММ-ДД.",
		plan.ErrUnsupportedCurrency: "Валюта: RUB или THB.",
		plan.ErrBadRate:             "Курс: число больше нуля и не больше 1000 ₽ за бат.",
		plan.ErrNoWindow:            "Открытого окна нет: сначала добавь ожидаемое поступление с датой.",
		plan.ErrInvalidEvent:        "Укажи либо дату, либо окно, а не оба сразу.",
		plan.ErrUnknownCategory:     "Такой категории нет. Выбери из списка или оставь поле пустым.",
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
