// Package llm реализует клиент к LLM (чат и эмбеддинги).
package llm

import (
	"context"
	"errors"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"log/slog"
	"simpleAI/config"
	"simpleAI/internal/apperr"
	"strings"
	"time"
)

var ErrChatCompletion = errors.New("Completions.New.Err")

type Client struct {
	api openai.Client
	l   *slog.Logger
	cfg config.Config
}

func NewClient(key string, l *slog.Logger, c config.Config) Client {
	client := openai.NewClient(option.WithAPIKey(key))
	return Client{api: client, l: l, cfg: c}
}

func (c *Client) Ask(prompt string) (string, error) {
	var (
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	)
	defer cancel()
	chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(c.cfg.SysPrompt),
			openai.UserMessage(prompt),
		},
		Model: openai.ChatModelGPT4_1Mini,
	})
	if err != nil {
		c.l.Error("Failed to create chat", "err", err, "code", ErrChatCompletion.Error())
		return "", err
	}

	if len(chatCompletion.Choices) == 0 {
		return "", apperr.New("LLM_EMPTY_CHOICES", "empty chat completion choices", nil)
	}
	content := strings.TrimSpace(chatCompletion.Choices[0].Message.Content)
	if content == "" {
		return "", apperr.New("LLM_EMPTY_CONTENT", "empty chat completion content", nil)
	}
	return content, nil
}
