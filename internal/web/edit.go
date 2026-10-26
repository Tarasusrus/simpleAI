package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"simpleAI/internal/plan"
)

// Правка записи плана (issue #81): GET отдаёт запись для формы, PATCH сохраняет.

// ItemDTO — запись для формы «изменить». Сумма — десятичная строка в исходной валюте
// записи («1234.50»): ровно то, что PATCH принимает обратно, без float.
type ItemDTO struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	Recurring   bool      `json:"recurring"`
	Fact        bool      `json:"fact"` // закрытая запись: правятся название, сумма и дата
	Name        string    `json:"name"`
	Amount      *string   `json:"amount"`
	Currency    string    `json:"currency"` // пусто, если суммы нет
	Category    string    `json:"category"`
	Date        *string   `json:"date"`
	WindowStart *string   `json:"windowStart"`
	Repeat      *RepeatDT `json:"repeat"`
	// OpensWindow — у ожидаемого поступления: открывает окно (зарплата, аванс) или нет (ADR-019).
	OpensWindow bool `json:"opensWindow"`
}

// RepeatDT — расписание правила, только для подписи («каждые 2 недели, до …»).
type RepeatDT struct {
	N     int     `json:"n"`
	Unit  string  `json:"unit"`
	Until *string `json:"until"`
}

func decimal(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

func toItemDTO(it plan.EditableItem) ItemDTO {
	out := ItemDTO{
		Key: it.Key, Kind: string(it.Kind), Recurring: it.Recurring, Fact: it.Fact, Name: it.Name, Category: it.Category,
		Date: datePtr(it.Date), WindowStart: datePtr(it.WindowStart), OpensWindow: it.OpensWindow,
	}
	if it.Amount != nil {
		a := decimal(it.Amount.Minor)
		out.Amount, out.Currency = &a, string(it.Amount.Cur)
	}
	if it.Rule != nil {
		out.Repeat = &RepeatDT{N: it.Rule.N, Unit: string(it.Rule.Unit), Until: datePtr(it.Rule.Until)}
	}
	return out
}

// handleItem: GET /api/plan/item/{key} — запись для формы «изменить».
func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	it, err := s.planner.Item(r.Context(), r.PathValue("key"))
	if err != nil {
		s.writeEditError(w, err)
		return
	}
	writeJSON(w, toItemDTO(it))
}

// editBody — новое содержимое записи. name, amount и category обязательны и
// заменяются целиком (amount: null — без суммы): забытое поле не должно молча
// стереть сумму. date / window необязательны: нет обоих — размещение не меняется;
// window "none" — убрать в «без даты».
type editBody struct {
	Name     *string         `json:"name"`
	Amount   json.RawMessage `json:"amount"`
	Currency string          `json:"currency"`
	Category *string         `json:"category"`
	Date     string          `json:"date"`
	Window   string          `json:"window"`
	// OpensWindow — вид ожидаемого поступления: нет поля — не менять (ADR-019).
	OpensWindow *bool `json:"opensWindow"`
}

var errEditFields = errors.New("web: нет обязательных полей правки")

// editInput переводит тело в команду домена.
func (b editBody) editInput() (plan.EditInput, error) {
	if b.Name == nil || b.Category == nil || len(b.Amount) == 0 {
		return plan.EditInput{}, errEditFields
	}
	in := plan.EditInput{Name: *b.Name, Category: *b.Category, OpensWindow: b.OpensWindow}
	raw := strings.TrimSpace(string(b.Amount))
	if raw != "null" && raw != `""` {
		var n json.Number
		if err := json.Unmarshal(b.Amount, &n); err != nil {
			return plan.EditInput{}, plan.ErrAmountNonPositive
		}
		money, err := parseMoney(n, b.Currency)
		if err != nil {
			return plan.EditInput{}, err
		}
		in.Amount = &money
	}
	date, window := strings.TrimSpace(b.Date), strings.TrimSpace(b.Window)
	switch {
	case date != "" && window != "":
		return plan.EditInput{}, fmt.Errorf("%w: дата и окно вместе", plan.ErrInvalidEvent)
	case date != "":
		d, err := plan.ParseDate(date)
		if err != nil {
			return plan.EditInput{}, err
		}
		in.Place = &plan.MoveInput{Date: &d}
	case window == "none":
		in.Place = &plan.MoveInput{}
	case window != "":
		in.Place = &plan.MoveInput{Window: plan.WindowChoice(window)}
	}
	return in, nil
}

// handleEdit: PATCH /api/plan/item/{key} — изменить плановую запись или правило повтора.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	var b editBody
	if !decode(w, r, &b) {
		return
	}
	in, err := b.editInput()
	if err != nil {
		s.writeEditError(w, err)
		return
	}
	key := r.PathValue("key")
	if err := s.planner.Edit(r.Context(), key, in); err != nil {
		s.writeEditError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "edit", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// handleReopen: POST /api/plan/item/{key}/reopen — вернуть закрытую запись в план.
// Факт удаляется, плановая запись снова открыта; сумму и дату, правленные после
// оплаты, она не наследует.
func (s *Server) handleReopen(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.planner.Reopen(r.Context(), key); err != nil {
		s.writeEditError(w, err)
		return
	}
	s.logger.Info("plan web command", "command", "reopen", "id", key, "added_by", "web")
	s.respondView(w, r, http.StatusOK)
}

// writeEditError: свои причины правки, остальное — как у прочих команд. Частные
// ошибки идут раньше общих: все они вложены в ErrInvalidEvent.
func (s *Server) writeEditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plan.ErrNotReopenable):
		writeError(w, http.StatusConflict, "Вернуть в план можно только оплату, закрытую с этого экрана.")
	case errors.Is(err, plan.ErrCurrencyLocked):
		writeError(w, http.StatusBadRequest, "У закрытой записи валюта не меняется.")
	case errors.Is(err, plan.ErrFactDateRequired):
		writeError(w, http.StatusBadRequest, "У закрытой записи нужна дата оплаты.")
	case errors.Is(err, errEditFields):
		writeError(w, http.StatusBadRequest, "Нужны поля name, amount (null — без суммы) и category.")
	case errors.Is(err, plan.ErrNotEditable):
		writeError(w, http.StatusBadRequest, "Изменить можно плановую запись, повторяющийся платёж или оплату, закрытую с этого экрана.")
	case errors.Is(err, plan.ErrKindNotIncome):
		writeError(w, http.StatusBadRequest, "Вид «открывает окно» есть только у поступления.")
	case errors.Is(err, plan.ErrRuleKind):
		writeError(w, http.StatusBadRequest, "У повторяющегося поступления вид пока не правится.")
	case errors.Is(err, plan.ErrRuleSchedule):
		writeError(w, http.StatusBadRequest, "У повторяющегося платежа дата не меняется. Чтобы пропустить один срок — «пропустить».")
	default:
		s.writeCommandError(w, err)
	}
}
