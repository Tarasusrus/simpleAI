package llm

import (
	"context"
	"errors"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"log/slog"
	"simpleAI/config"
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
		ctx, cancel = context.WithTimeout(context.Background(), c.timeout())
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
		return "", errors.New("empty chat completion choices")
	}

	return chatCompletion.Choices[0].Message.Content, nil
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, errors.New("no inputs for embedding")
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	resp, err := c.api.Embeddings.New(reqCtx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: inputs,
		},
		Model: openai.EmbeddingModel(c.cfg.RAG.EmbeddingModel),
	})
	if err != nil {
		return nil, err
	}

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

func (c *Client) timeout() time.Duration {
	if c.cfg.LLM.Timeout > 0 {
		return c.cfg.LLM.Timeout
	}
	return 30 * time.Second
}
