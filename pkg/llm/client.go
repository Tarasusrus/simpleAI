package llm

import (
	"context"
	"errors"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"log/slog"
	"simpleAI/config"
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
	retries := c.retryCount()
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
		chatCompletion, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(c.cfg.SysPrompt),
				openai.UserMessage(prompt),
			},
			Model: openai.ChatModelGPT4_1Mini,
		})
		cancel()

		if err == nil && len(chatCompletion.Choices) > 0 {
			content := strings.TrimSpace(chatCompletion.Choices[0].Message.Content)
			if content == "" {
				err = errors.New("empty chat completion content")
			} else {
				return content, nil
			}
		}
		if err == nil {
			err = errors.New("empty chat completion choices")
		}
		lastErr = err
		if attempt < retries {
			c.sleepBackoff(attempt)
		}
	}

	c.l.Error("Failed to create chat", "err", lastErr, "code", ErrChatCompletion.Error())
	return "", lastErr
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, errors.New("no inputs for embedding")
	}
	retries := c.retryCount()
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout())
		resp, err := c.api.Embeddings.New(reqCtx, openai.EmbeddingNewParams{
			Input: openai.EmbeddingNewParamsInputUnion{
				OfArrayOfStrings: inputs,
			},
			Model: openai.EmbeddingModel(c.cfg.RAG.EmbeddingModel),
		})
		cancel()

		if err == nil {
			embeddings := make([][]float32, len(inputs))
			for _, item := range resp.Data {
				if item.Index < 0 || int(item.Index) >= len(inputs) {
					continue
				}
				vec := make([]float32, len(item.Embedding))
				for i, v := range item.Embedding {
					vec[i] = float32(v)
				}
				embeddings[item.Index] = vec
			}
			return embeddings, nil
		}

		lastErr = err
		if attempt < retries {
			if err := c.sleepBackoffWithContext(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (c *Client) timeout() time.Duration {
	if c.cfg.LLM.Timeout > 0 {
		return c.cfg.LLM.Timeout
	}
	return 30 * time.Second
}

func (c *Client) retryCount() int {
	if c.cfg.LLM.RetryCount > 0 {
		return c.cfg.LLM.RetryCount
	}
	return 0
}

func (c *Client) retryBase() time.Duration {
	if c.cfg.LLM.RetryBase > 0 {
		return c.cfg.LLM.RetryBase
	}
	return 500 * time.Millisecond
}

func (c *Client) sleepBackoff(attempt int) {
	time.Sleep(c.retryBase() * time.Duration(1<<attempt))
}

func (c *Client) sleepBackoffWithContext(ctx context.Context, attempt int) error {
	timer := time.NewTimer(c.retryBase() * time.Duration(1<<attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
