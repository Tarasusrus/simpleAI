// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"fmt"
	"strings"

	"simpleAI/internal/constants"
)

func HandleStart(ctx context.Context, tctx *Context) error {
	return tctx.Reply(constants.MsgTelegramStart)
}

func HandleHelp(ctx context.Context, tctx *Context) error {
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
	return tctx.Reply(constants.MsgTelegramHelp)
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

	stopTyping := tctx.StartTyping(ctx)
	chatID := tctx.Update.ChatID
	reply, err := tctx.Agent.AskWithMeta(ctx, prompt, &chatID)
	stopTyping()
	if err != nil {
		return tctx.Reply(fmt.Sprintf("Ошибка агента: %v", err))
	}
	if strings.TrimSpace(reply) == "" {
		reply = "Нет ответа от агента."
	}
	return tctx.Reply(reply)
}
