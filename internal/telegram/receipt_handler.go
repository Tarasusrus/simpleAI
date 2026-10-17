package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt"
)

// ReceiptService — сценарий фото чека (receipt.Service).
type ReceiptService interface {
	Process(ctx context.Context, chatID int64, file io.Reader) (receipt.Draft, error)
	Confirm(ctx context.Context, chatID int64, draftID string) (string, error)
	Cancel(chatID int64, draftID string) error
}

// ReceiptCallbackPrefix — префикс callback_data кнопок черновика чека.
const ReceiptCallbackPrefix = "rcpt:"

const (
	receiptActionConfirm = "ok"
	receiptActionCancel  = "no"
)

// Тексты сценария чека живут здесь, а не в constants/messages.go: так
// эпик фото не правит файл, общий с параллельной работой (ADR-010).
const (
	msgReceiptDraftGone   = "Черновик чека устарел — пришли фото ещё раз."
	msgReceiptCancelled   = "Отменено, расход не записан."
	msgReceiptWriteFailed = "Не удалось записать расход. Пришли фото ещё раз."
	msgReceiptAsk         = "Записать расход?"
	receiptItemsShown     = 10
)

func receiptCallbackData(action, draftID string) string {
	return ReceiptCallbackPrefix + action + ":" + draftID
}

// ReceiptDraftMessage — текст черновика и кнопки «Записать / Отмена».
func ReceiptDraftMessage(d receipt.Draft) (string, [][]core.Button) {
	r := d.Receipt
	var b strings.Builder
	b.WriteString("🧾 ")
	if r.Merchant != "" {
		b.WriteString(r.Merchant)
	} else {
		b.WriteString("Чек")
	}
	if r.Date != "" {
		b.WriteString(" · " + r.Date)
	}
	fmt.Fprintf(&b, "\nИтого: %s %s", formatAmount(r.Total), r.Currency)
	if r.Category != "" {
		b.WriteString("\nКатегория: " + r.Category)
	} else {
		b.WriteString("\nКатегория: без категории")
	}
	for i, it := range r.Items {
		if i == receiptItemsShown {
			fmt.Fprintf(&b, "\n… ещё %d", len(r.Items)-receiptItemsShown)
			break
		}
		fmt.Fprintf(&b, "\n• %s — %s", it.Name, formatAmount(it.Amount))
	}
	b.WriteString("\n\n" + msgReceiptAsk)

	return b.String(), [][]core.Button{{
		{Text: "✅ Записать", Data: receiptCallbackData(receiptActionConfirm, d.ID)},
		{Text: "✖️ Отмена", Data: receiptCallbackData(receiptActionCancel, d.ID)},
	}}
}

func formatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// NewHandleReceiptCallback — нажатия «Записать / Отмена» под черновиком.
// Сообщение черновика заменяется итогом, кнопки убираются.
func NewHandleReceiptCallback(svc ReceiptService) Handler {
	return func(ctx context.Context, tctx *Context) error {
		action, draftID, ok := strings.Cut(strings.TrimPrefix(tctx.Update.CallbackData, ReceiptCallbackPrefix), ":")
		if !ok || draftID == "" {
			return nil
		}
		chatID := tctx.Update.ChatID

		switch action {
		case receiptActionConfirm:
			reply, err := svc.Confirm(ctx, chatID, draftID)
			switch {
			case errors.Is(err, receipt.ErrDraftNotFound):
				return tctx.EditWithButtons(ctx, msgReceiptDraftGone, nil)
			case err != nil:
				if tctx.Logger != nil {
					tctx.Logger.ErrorContext(ctx, "receipt confirm failed", "err", err)
				}
				return tctx.EditWithButtons(ctx, msgReceiptWriteFailed, nil)
			}
			return tctx.EditWithButtons(ctx, reply, nil)
		case receiptActionCancel:
			if err := svc.Cancel(chatID, draftID); errors.Is(err, receipt.ErrDraftNotFound) {
				return tctx.EditWithButtons(ctx, msgReceiptDraftGone, nil)
			}
			return tctx.EditWithButtons(ctx, msgReceiptCancelled, nil)
		}
		return nil
	}
}
