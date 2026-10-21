package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"simpleAI/internal/core"
	"simpleAI/internal/receipt"
	"simpleAI/internal/receipt/filecheck"
	"simpleAI/internal/tools"
)

const (
	msgReceiptNotConfigured = "Обработка фото не настроена."
	msgReceiptTooLarge      = "Файл слишком большой — пришли фото чека поменьше."
	msgReceiptUnknownFile   = "Это не похоже на фото или PDF — такой файл не принимаю."
	msgReceiptUnsupported   = "Этот формат модель не принимает — пришли обычное фото чека."
	msgReceiptNotReceipt    = "На фото не чек — расход не создан."
	msgReceiptUnreadable    = "Не удалось распознать фото. Сними чек ещё раз, ровно и при хорошем свете."
	msgReceiptNoCurrency    = "Не понял валюту чека — расход не создан. Можно ввести трату текстом."
	msgReceiptNoTotal       = "Не нашёл итоговую сумму на чеке — расход не создан. Можно ввести трату текстом."
	msgReceiptFailed        = "Не удалось обработать фото, попробуй позже."
	msgReceiptProxyDown     = "Сервис распознавания сейчас недоступен — пришли фото чуть позже."
)

// WithReceiptPhotos отправляет фото и документы-картинки/PDF в сценарий
// чека, а всё остальное — в next. Подпись к фото игнорируется: фото — это
// ввод данных, а не вопрос агенту (ADR-014 §1). Фото не сохраняется на
// диск и не попадает в ingest-JSON: ветка стоит до next.
//
// svc == nil — сценарий не настроен: фото получает «не настроена», а не
// старый путь с сохранением на диск.
func WithReceiptPhotos(svc ReceiptService, next Handler) Handler {
	return func(ctx context.Context, tctx *Context) error {
		att, ok := receiptAttachment(tctx.Update.Attachments)
		if !ok {
			return next(ctx, tctx)
		}
		if svc == nil {
			return tctx.Reply(msgReceiptNotConfigured)
		}
		return handleReceiptAttachment(ctx, tctx, svc, att)
	}
}

// receiptAttachment выбирает вложение для сценария чека. Заявленный клиентом
// тип только выбирает путь; допуск решает проверка по содержимому.
func receiptAttachment(atts []core.Attachment) (core.Attachment, bool) {
	for _, a := range atts {
		switch {
		case a.Kind == "photo":
			return a, true
		case a.Kind == "document" && (strings.HasPrefix(a.MimeType, "image/") || a.MimeType == "application/pdf"):
			return a, true
		}
	}
	return core.Attachment{}, false
}

func handleReceiptAttachment(ctx context.Context, tctx *Context, svc ReceiptService, att core.Attachment) error {
	if tctx.Fetcher == nil {
		return tctx.Reply(msgReceiptFailed)
	}
	body, _, err := tctx.Fetcher.FetchAttachment(ctx, att)
	if err != nil {
		logReceiptError(ctx, tctx, "receipt fetch failed", att, err)
		return tctx.Reply(msgReceiptFailed)
	}
	defer func() {
		if err := body.Close(); err != nil {
			_ = err
		}
	}()

	// Два вызова модели подряд — до пары минут; без индикатора пользователь
	// решит, что бот завис, и пришлёт фото ещё раз.
	stopTyping := tctx.StartTyping(ctx)
	draft, err := svc.Process(ctx, tctx.Update.ChatID, body)
	stopTyping()
	if err != nil {
		msg, known := receiptErrorMessage(err)
		if !known || errors.Is(err, receipt.ErrNotConfigured) {
			logReceiptError(ctx, tctx, "receipt processing failed", att, err)
		}
		return tctx.Reply(msg)
	}
	text, rows := ReceiptDraftMessage(draft)
	return tctx.ReplyWithButtons(ctx, text, rows)
}

// logReceiptError пишет только тип вложения и ошибку: ни байтов, ни
// распознанного текста чека в логах быть не должно (ADR-014 §6).
func logReceiptError(ctx context.Context, tctx *Context, msg string, att core.Attachment, err error) {
	if tctx.Logger != nil {
		tctx.Logger.ErrorContext(ctx, msg, "kind", att.Kind, "size", att.Size, "err", scrubError(err))
	}
}

// base64Run — длинный кусок base64. Ошибка провайдера может процитировать
// тело запроса, а в нём картинка data-URL.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/=]{64,}`)

// errTextLimit — сколько текста ошибки оставлять в логе.
const errTextLimit = 500

// scrubError: ошибка скачивания вложения содержит URL с токеном бота —
// его маскирует tools.Redact.
func scrubError(err error) string {
	s := base64Run.ReplaceAllString(tools.Redact(err.Error()), "[base64]")
	if len(s) > errTextLimit {
		s = strings.ToValidUTF8(s[:errTextLimit], "") + "…"
	}
	return s
}

// receiptErrorMessage переводит отказ шага в текст для пользователя;
// known=false — сбой, а не отказ, его надо залогировать.
func receiptErrorMessage(err error) (msg string, known bool) {
	switch {
	case errors.Is(err, receipt.ErrNotConfigured):
		return msgReceiptNotConfigured, true
	case errors.Is(err, receipt.ErrCatalogUnavailable):
		return msgReceiptProxyDown, false
	case errors.Is(err, filecheck.ErrTooLarge):
		return msgReceiptTooLarge, true
	case errors.Is(err, filecheck.ErrUnknown):
		return msgReceiptUnknownFile, true
	case errors.Is(err, filecheck.ErrNotSupported):
		return msgReceiptUnsupported, true
	case errors.Is(err, receipt.ErrNotReceipt):
		return msgReceiptNotReceipt, true
	case errors.Is(err, receipt.ErrUnreadable):
		return msgReceiptUnreadable, true
	case errors.Is(err, receipt.ErrNoCurrency):
		return msgReceiptNoCurrency, true
	case errors.Is(err, receipt.ErrNoTotal):
		return msgReceiptNoTotal, true
	}
	return msgReceiptFailed, false
}

// NewHandleModels — команда /models: модели прокси и что они принимают.
// catalog == nil — VISION_* не заданы.
func NewHandleModels(catalog core.ModelCatalog, receiptModel string) Handler {
	return func(ctx context.Context, tctx *Context) error {
		if catalog == nil {
			return tctx.Reply(msgReceiptNotConfigured + " Нужны VISION_LLM_BASE_URL и VISION_LLM_API_KEY.")
		}
		models, err := catalog.Models(ctx)
		if err != nil {
			if tctx.Logger != nil {
				tctx.Logger.ErrorContext(ctx, "model catalog failed", "err", err)
			}
			return tctx.Reply("Прокси моделей недоступен — фото сейчас не обработать.")
		}
		var b strings.Builder
		b.WriteString("Модели прокси:")
		found := false
		for _, m := range models {
			fmt.Fprintf(&b, "\n• %s — %s", m.Name, describeCapabilities(m.Capabilities))
			if m.Name == receiptModel {
				b.WriteString(" ← для чеков")
				found = true
			}
		}
		if !found {
			fmt.Fprintf(&b, "\n\nМодели %q для чеков в прокси нет — фото не обработать.", receiptModel)
		}
		return tctx.Reply(b.String())
	}
}

func describeCapabilities(c core.ModelCapabilities) string {
	switch {
	case c.Vision && c.PDFInput:
		return "фото и PDF"
	case c.Vision:
		return "фото"
	case c.PDFInput:
		return "PDF"
	}
	return "только текст"
}
