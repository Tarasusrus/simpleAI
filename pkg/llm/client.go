package llm

import (
	"context"
	"errors"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"log/slog"
)

var ErrChatCompletion = errors.New("Completions.New.Err")

type Client struct {
	api openai.Client
	l   slog.Logger
}

func NewClient(key string, l slog.Logger) *Client {
	client := openai.NewClient(option.WithAPIKey(key))
	return &Client{api: client, l: l}
}

func (c *Client) Ask(promt string) (string, error) {
	var (
		ctx = context.Background()
	)
	chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(promt),
		},
		Model: openai.ChatModelGPT4_1Mini,
	})
	if err != nil {
		c.l.Error("Failed to create chat", ErrChatCompletion.Error(), err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}
