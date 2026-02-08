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

	if incoming.Text == "" && len(incoming.Attachments) > 0 {
		return tctx.Reply("Файлы/фото/голос получены. Обработка будет добавлена скоро.")
	}
	if strings.TrimSpace(incoming.Text) == "" {
		return tctx.Reply("Пустое сообщение. Напиши запрос.")
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
