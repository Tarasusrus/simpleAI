// Package ollama реализует LLM-адаптер для локального Ollama.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"simpleAI/config"
	"simpleAI/internal/apperr"
	"simpleAI/internal/constants"
)

type Client struct {
	baseURL    string
	model      string
	embedModel string
	sysPrompt  string
	httpClient *http.Client
	logger     *slog.Logger
	cfg        config.Config
}

func NewClient(cfg config.Config, logger *slog.Logger) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.LLM.OllamaBaseURL)
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := strings.TrimSpace(cfg.LLM.OllamaModel)
	if model == "" {
		model = "llama3.2"
	}
	embedModel := strings.TrimSpace(cfg.LLM.OllamaEmbedModel)
	if embedModel == "" {
		embedModel = model
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		embedModel: embedModel,
		sysPrompt:  cfg.SysPrompt,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		logger:     logger,
		cfg:        cfg,
	}, nil
}

func (c *Client) Ask(ctx context.Context, prompt string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	retries := c.retryCount()
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		reqBody := map[string]any{
			"model":  c.model,
			"prompt": prompt,
			"stream": false,
		}
		if strings.TrimSpace(c.sysPrompt) != "" {
			reqBody["system"] = c.sysPrompt
		}
		resp, err := c.postJSON(ctx, "/api/generate", reqBody)
		if err == nil {
			var payload struct {
				Response string `json:"response"`
			}
			if unmarshalErr := json.Unmarshal(resp, &payload); unmarshalErr != nil {
				err = fmt.Errorf("ollama parse response: %w", unmarshalErr)
			} else {
				text := strings.TrimSpace(payload.Response)
				if text != "" {
					return text, nil
				}
				err = apperr.New(constants.ErrCodeLLMEmptyContent, constants.ErrMsgLLMEmptyContent, nil)
			}
		}
		lastErr = err
		if attempt < retries {
			c.sleepBackoff(attempt)
		}
	}
	return "", lastErr
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, apperr.New(constants.ErrCodeLLMEmbedEmptyInput, constants.ErrMsgLLMEmbedEmptyInput, nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	embeddings := make([][]float32, len(inputs))
	for i, input := range inputs {
		vec, err := c.embedOne(ctx, input)
		if err != nil {
			return nil, err
		}
		embeddings[i] = vec
	}
	return embeddings, nil
}

func (c *Client) embedOne(ctx context.Context, input string) ([]float32, error) {
	retries := c.retryCount()
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := c.postJSON(ctx, "/api/embeddings", map[string]any{
			"model":  c.embedModel,
			"prompt": input,
		})
		if err == nil {
			var payload struct {
				Embedding []float64 `json:"embedding"`
			}
			if unmarshalErr := json.Unmarshal(resp, &payload); unmarshalErr != nil {
				err = fmt.Errorf("ollama parse embedding: %w", unmarshalErr)
			} else {
				if len(payload.Embedding) == 0 {
					return nil, apperr.New(constants.ErrCodeLLMEmptyContent, constants.ErrMsgLLMEmptyContent, nil)
				}
				out := make([]float32, len(payload.Embedding))
				for i, v := range payload.Embedding {
					out[i] = float32(v)
				}
				return out, nil
			}
		}
		lastErr = err
		if attempt < retries {
			c.sleepBackoff(attempt)
		}
	}
	return nil, lastErr
}

func (c *Client) postJSON(ctx context.Context, path string, body any) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && c.logger != nil {
			c.logger.Warn("ollama response close failed", "err", err)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
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
