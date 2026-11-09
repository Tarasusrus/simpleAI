package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"simpleAI/internal/apperr"
	"simpleAI/internal/constants"
	"simpleAI/internal/core"
)

// VisionClient — core.VisionLLM поверх OpenAI-совместимого API (прокси
// LiteLLM). В отличие от Client у него нет ни ретраев, ни fallback-клиента:
// переключение между моделями делает сама цепочка маршрута в прокси, а
// бот обязан отправить картинку ровно в запрошенную модель (ADR-014 §4).
type VisionClient struct {
	api     openai.Client
	timeout time.Duration
}

func NewVisionClient(baseURL, apiKey string, timeout time.Duration) (*VisionClient, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("VISION_LLM_BASE_URL and VISION_LLM_API_KEY must be set")
	}
	api := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
		// Фото чека — личные данные: прокси не пишет сообщения в Langfuse и
		// spend logs. Второй слой поверх флага на деплойментах маршрута vision
		// (litellm-free-models ADR-0001 §6).
		option.WithHeader("x-litellm-enable-message-redaction", "true"),
	)
	return &VisionClient{api: api, timeout: timeout}, nil
}

// AskVision шлёт картинку как data-URL, PDF — как file с data-URL: так их
// принимает LiteLLM. Temperature всегда 0: и классификация, и разбор чека —
// извлечение фактов, а не генерация.
func (c *VisionClient) AskVision(ctx context.Context, req core.VisionRequest) (string, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	params := openai.ChatCompletionNewParams{
		Model:       req.Model,
		Messages:    visionMessages(req),
		Temperature: openai.Float(0),
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}

	resp, err := c.api.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", apperr.New(constants.ErrCodeLLMEmptyChoices, constants.ErrMsgLLMEmptyChoices, nil)
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return "", apperr.New(constants.ErrCodeLLMEmptyContent, constants.ErrMsgLLMEmptyContent, nil)
	}
	return content, nil
}

func visionMessages(req core.VisionRequest) []openai.ChatCompletionMessageParamUnion {
	dataURL := "data:" + req.MimeType + ";base64," + base64.StdEncoding.EncodeToString(req.Data)

	var media openai.ChatCompletionContentPartUnionParam
	if req.MimeType == "application/pdf" {
		media = openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
			FileData: openai.String(dataURL),
		})
	} else {
		media = openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: dataURL})
	}

	var msgs []openai.ChatCompletionMessageParamUnion
	if strings.TrimSpace(req.System) != "" {
		msgs = append(msgs, openai.SystemMessage(req.System))
	}
	return append(msgs, openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart(req.Prompt),
		media,
	}))
}
