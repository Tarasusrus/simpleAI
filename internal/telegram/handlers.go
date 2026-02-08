// Package telegram реализует прикладной слой Telegram-бота: роутер, middleware, контекст и обработку вложений.
// Пакет также сохраняет raw-ingest payload для дальнейшей обработки; точка входа Router.HandleUpdate.
package telegram

import (
	"context"
	"fmt"
	"strings"
)

func HandleStart(ctx context.Context, tctx *Context) error {
	return tctx.Reply("Привет! Я фасад для агентов. Напиши сообщение, и я отвечу.")
}

func HandleHelp(ctx context.Context, tctx *Context) error {
	help := strings.Join([]string{
		"Доступные команды:",
		"/start - приветствие",
		"/help - справка",
		"",
		"Поддержка файлов и фото будет добавлена отдельно.",
	}, "\n")
	return tctx.Reply(help)
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

	reply, err := tctx.Agent.Ask(ctx, prompt)
	if err != nil {
		return tctx.Reply(fmt.Sprintf("Ошибка агента: %v", err))
	}
	if strings.TrimSpace(reply) == "" {
		reply = "Нет ответа от агента."
	}
	return tctx.Reply(reply)
}
