// Package openai реализует LLM-адаптер для провайдера OpenAI.
package openai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"simpleAI/config"
	"simpleAI/internal/apperr"
	"simpleAI/internal/constants"
)

var ErrChatCompletion = errors.New("Completions.New.Err")

type Client struct {
	api openai.Client
	l   *slog.Logger
	cfg config.Config
}

func NewClient(cfg config.Config, logger *slog.Logger) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("API key not found in .env")
	}
	client := openai.NewClient(option.WithAPIKey(cfg.APIKey))
	if logger != nil {
		logger.Debug("openai client initialized", "model", cfg.LLM.ChatModel)
	}
	return &Client{api: client, l: logger, cfg: cfg}, nil
}

func (c *Client) Ask(ctx context.Context, prompt string) (string, error) {
	return c.askWithCustomSystem(ctx, c.cfg.SysPrompt, prompt)
}

func (c *Client) AskWithSystem(ctx context.Context, systemAddition, userPrompt string) (string, error) {
	system := c.cfg.SysPrompt
	if strings.TrimSpace(systemAddition) != "" {
		if system != "" {
			system += "\n\n" + systemAddition
		} else {
			system = systemAddition
		}
	}
	return c.askWithCustomSystem(ctx, system, userPrompt)
}

func (c *Client) askWithCustomSystem(ctx context.Context, system, prompt string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	retries := c.retryCount()
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, c.timeout())
		chatCompletion, err := c.api.Chat.Completions.New(reqCtx, openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(system),
				openai.UserMessage(prompt),
			},
			Model: c.chatModel(),
		})
		cancel()

		if err == nil && len(chatCompletion.Choices) > 0 {
			content := strings.TrimSpace(chatCompletion.Choices[0].Message.Content)
			if content == "" {
				err = apperr.New(constants.ErrCodeLLMEmptyContent, constants.ErrMsgLLMEmptyContent, nil)
			} else {
				return content, nil
			}
		}
		if err == nil {
			err = apperr.New(constants.ErrCodeLLMEmptyChoices, constants.ErrMsgLLMEmptyChoices, nil)
		}
		lastErr = err
		if attempt < retries {
			c.sleepBackoff(attempt)
		}
	}

	if c.l != nil {
		c.l.Error("Failed to create chat", "err", lastErr, "code", ErrChatCompletion.Error())
	}
	return "", lastErr
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, apperr.New(constants.ErrCodeLLMEmbedEmptyInput, constants.ErrMsgLLMEmbedEmptyInput, nil)
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

func (c *Client) chatModel() openai.ChatModel {
	model := strings.TrimSpace(c.cfg.LLM.ChatModel)
	if model == "" {
		return openai.ChatModelGPT4_1Mini
	}
	return openai.ChatModel(model)
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
