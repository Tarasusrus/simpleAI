package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type LLMClient interface {
	Ask(prompt string) (string, error)
}

type DigestResult struct {
	Digest   string
	Messages []FetchedMessage
}

type digestPayload struct {
	Digest   string             `json:"digest"`
	Messages []digestAnnotation `json:"messages"`
}

type digestAnnotation struct {
	ProviderUID string `json:"provider_uid"`
	Category    string `json:"category"`
	Importance  string `json:"importance"`
	Summary     string `json:"summary"`
}

func BuildDigest(ctx context.Context, client LLMClient, account Account, messages []FetchedMessage) (DigestResult, error) {
	_ = ctx
	if len(messages) == 0 {
		return DigestResult{Digest: "Новых писем нет."}, nil
	}
	prompt := buildDigestPrompt(account, messages)
	response, err := client.Ask(prompt)
	if err != nil {
		return fallbackDigest(messages), err
	}
	var payload digestPayload
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		return fallbackDigest(messages), err
	}
	annotations := map[string]digestAnnotation{}
	for _, item := range payload.Messages {
		if strings.TrimSpace(item.ProviderUID) == "" {
			continue
		}
		annotations[item.ProviderUID] = item
	}
	for i, msg := range messages {
		if msg.Metadata == nil {
			msg.Metadata = map[string]any{}
		}
		if ann, ok := annotations[msg.ProviderUID]; ok {
			msg.Metadata["category"] = ann.Category
			msg.Metadata["importance"] = ann.Importance
			msg.Metadata["summary"] = ann.Summary
		}
		messages[i] = msg
	}
	digest := strings.TrimSpace(payload.Digest)
	if digest == "" {
		digest = fallbackDigest(messages).Digest
	}
	return DigestResult{Digest: digest, Messages: messages}, nil
}

func buildDigestPrompt(account Account, messages []FetchedMessage) string {
	var sb strings.Builder
	sb.WriteString("Ты ассистент для подготовки дайджеста писем.\n")
	sb.WriteString("Сгруппируй письма и оцени важность (important|normal|ignore).\n")
	sb.WriteString("Верни JSON с полями digest и messages.\n")
	sb.WriteString("messages: массив объектов {provider_uid, category, importance, summary}.\n")
	sb.WriteString("digest: краткий дайджест на русском, 5-10 предложений.\n\n")
	sb.WriteString("Аккаунт: ")
	sb.WriteString(account.Email)
	sb.WriteString("\n")
	sb.WriteString("Письма:\n")
	for _, msg := range messages {
		sb.WriteString("- id: ")
		sb.WriteString(msg.ProviderUID)
		sb.WriteString(" | from: ")
		sb.WriteString(msg.FromEmail)
		sb.WriteString(" | subject: ")
		sb.WriteString(msg.Subject)
		sb.WriteString(" | received: ")
		sb.WriteString(formatTime(msg.ReceivedAt))
		sb.WriteString(" | preview: ")
		sb.WriteString(trimPreview(msg.Preview))
		sb.WriteString("\n")
	}
	return sb.String()
}

func fallbackDigest(messages []FetchedMessage) DigestResult {
	var sb strings.Builder
	sb.WriteString("Новые письма:\n")
	for _, msg := range messages {
		sb.WriteString("- ")
		sb.WriteString(msg.Subject)
		if msg.FromEmail != "" {
			sb.WriteString(" (")
			sb.WriteString(msg.FromEmail)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
	}
	return DigestResult{
		Digest:   strings.TrimSpace(sb.String()),
		Messages: messages,
	}
}

func formatTime(ts *time.Time) string {
	if ts == nil {
		return "unknown"
	}
	return ts.Format(time.RFC3339)
}

func trimPreview(preview string) string {
	preview = strings.TrimSpace(preview)
	if len(preview) <= 140 {
		return preview
	}
	return fmt.Sprintf("%s...", preview[:140])
}
