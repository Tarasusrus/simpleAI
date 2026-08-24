// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"fmt"
	"strings"

	"simpleAI/internal/constants"
	"simpleAI/internal/core"
	"simpleAI/internal/notify"
	budgetskill "simpleAI/internal/skills/budget"
)

func HandleStart(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgTelegramStart)
}

func NewHandleHelp(generalHelp string) Handler {
	return func(ctx context.Context, tctx *Context) error {
		parts := strings.Fields(strings.TrimSpace(tctx.Update.Text))
		if len(parts) >= 2 {
			switch strings.ToLower(parts[1]) {
			case "budget", "бюджет":
				return tctx.Reply(constants.MsgHelpBudget)
			case "recurring", "регулярные":
				return tctx.Reply(constants.MsgHelpRecurring)
			case "forecast", "прогноз":
				return tctx.Reply(constants.MsgHelpForecast)
			case "reminders", "reminder", "напоминания":
				return tctx.Reply(constants.MsgHelpReminders)
			}
		}
		return tctx.Reply(generalHelp)
	}
}

func HandleBudget(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgHelpBudget)
}

func HandleRecurring(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgHelpRecurring)
}

func HandleForecast(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgHelpForecast)
}

func HandleReminders(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgHelpReminders)
}

// BudgetCallbackSkill — минимальный интерфейс для обработки budget callbacks.
type BudgetCallbackSkill interface {
	HandleCallbackData(ctx context.Context, chatID int64, data string) (core.CallbackResult, error)
}

// NewHandleBudgetCallback возвращает handler для inline-кнопок budget skill.
func NewHandleBudgetCallback(skill BudgetCallbackSkill) Handler {
	return func(ctx context.Context, tctx *Context) error {
		result, err := skill.HandleCallbackData(ctx, tctx.Update.ChatID, tctx.Update.CallbackData)
		if err != nil {
			return tctx.Reply(fmt.Sprintf("Ошибка: %v", err))
		}
		return tctx.EditWithButtons(ctx, result.Text, result.Buttons)
	}
}

func HandleDefault(ctx context.Context, tctx *Context) error {
	incoming, ok := FromUpdate(tctx.Update)
	if !ok {
		return nil
	}

	if len(incoming.Attachments) > 0 {
		paths, err := SaveAttachments(ctx, tctx.Fetcher, incoming.Attachments, tctx.MediaDir)
		if err != nil && tctx.Logger != nil {
			tctx.Logger.Error("failed to save attachments", "err", err)
		}
		if _, err := SaveIngestPayload(ctx, tctx.MediaDir, incoming, paths); err != nil {
			if tctx.Logger != nil {
				tctx.Logger.Error("failed to save ingest payload", "err", err)
			}
		}
		if incoming.Text == "" {
			if len(paths) == 0 {
				return tctx.Reply("Вложения получены. Обработка будет добавлена скоро.")
			}
			return tctx.Reply(fmt.Sprintf("Вложения сохранены: %s", strings.Join(paths, ", ")))
		}
	}
	if strings.TrimSpace(incoming.Text) == "" {
		return tctx.Reply("Пустое сообщение. Напиши запрос.")
	}
	if handled, err := handleEnvelopeSchedule(ctx, tctx, incoming.Text); handled {
		return err
	}
	if _, err := SaveIngestPayload(ctx, tctx.MediaDir, incoming, nil); err != nil {
		if tctx.Logger != nil {
			tctx.Logger.Error("failed to save ingest payload", "err", err)
		}
	}
	if tctx.Agent == nil {
		return tctx.Reply("Агент не настроен.")
	}

	prompt := incoming.Text
	if summary := incoming.AttachmentSummary(); summary != "" {
		prompt = fmt.Sprintf("%s\n\n[Вложения: %s]", prompt, summary)
	}

	sink := &budgetskill.ButtonSink{}
	agentCtx := budgetskill.WithButtonSink(ctx, sink)

	stopTyping := tctx.StartTyping(ctx)
	chatID := tctx.Update.ChatID
	reply, err := tctx.Agent.AskWithMeta(agentCtx, prompt, &chatID)
	stopTyping()
	if err != nil {
		return tctx.Reply(fmt.Sprintf("Ошибка агента: %v", err))
	}
	if strings.TrimSpace(reply) == "" {
		reply = "Нет ответа от агента."
	}
	if rows, ok := budgetskill.ButtonsFromContext(agentCtx); ok {
		return tctx.ReplyWithButtons(ctx, reply, rows)
	}
	return tctx.Reply(reply)
}

// handleEnvelopeSchedule перехватывает «присылай конверты по утрам» / «не
// присылай конверты» до похода к LLM.
//
// Детерминированно и мимо агента: включение рассылки — настройка, а не ответ.
// Ошибка маршрутизации здесь молчаливая — оператор получил бы вежливый текст и
// думал, что пуш включён, пока месяц не приходило ни одного.
func handleEnvelopeSchedule(ctx context.Context, tctx *Context, text string) (bool, error) {
	if tctx.EnvelopeReminders == nil {
		return false, nil
	}
	cmd, ok := notify.ParseEnvelopeCommand(text)
	if !ok {
		return false, nil
	}
	chatID, err := tctx.ChatID()
	if err != nil {
		return false, nil
	}

	reply, err := notify.ApplyEnvelopeCommand(ctx, tctx.EnvelopeReminders, chatID, cmd, notify.DefaultEnvelopeTimezone)
	if err != nil {
		if tctx.Logger != nil {
			tctx.Logger.Error("envelope schedule failed", "chat_id", chatID, "err", err)
		}
		return true, tctx.Reply("Не удалось сохранить настройку утренних конвертов — попробуй ещё раз.")
	}
	return true, tctx.Reply(reply)
}
