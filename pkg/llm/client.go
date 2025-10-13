package llm

import (
	"context"
	"errors"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"log/slog"
	"simpleAI/config"
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
		ctx = context.Background()
	)
	chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(c.cfg.SysPrompt),
			openai.UserMessage(prompt),
		},
		Model: openai.ChatModelGPT4_1Mini,
	})
	if err != nil {
		c.l.Error("Failed to create chat", ErrChatCompletion.Error(), err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}

func (c *Client) Asksssss(prompt string) (string, error) {
	var (
		ctx = context.Background()
	)
	chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(c.cfg.SysPrompt),
			openai.UserMessage(prompt),
		},
		Model: openai.ChatModelGPT4_1Mini,
	})
	if err != nil {
		c.l.Error("Failed to create chat", ErrChatCompletion.Error(), err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}

func (c *Client) Asksssssssss(prompt string) (string, error) {
	var (
		ctx = context.Background()
	)
	chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(c.cfg.SysPrompt),
			openai.UserMessage(prompt),
		},
		Model: openai.ChatModelGPT4_1Mini,
	})
	if err != nil {
		c.l.Error("Failed to create chat", ErrChatCompletion.Error(), err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}
