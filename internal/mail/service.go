package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type Notifier interface {
	Send(ctx context.Context, text string) error
}

type Runner struct {
	store     *Store
	gmail     Provider
	imap      Provider
	notifier  Notifier
	llmClient LLMClient
	logger    *slog.Logger
}

func NewRunner(store *Store, gmail Provider, imap Provider, notifier Notifier, llmClient LLMClient, logger *slog.Logger) *Runner {
	return &Runner{
		store:     store,
		gmail:     gmail,
		imap:      imap,
		notifier:  notifier,
		llmClient: llmClient,
		logger:    logger,
	}
}

func (r *Runner) RunOnce(ctx context.Context, accounts []Account) error {
	for _, account := range accounts {
		if err := r.runAccount(ctx, account); err != nil {
			r.logger.Error("mail account run failed", "email", account.Email, "err", err)
		}
	}
	return nil
}

func (r *Runner) runAccount(ctx context.Context, account Account) error {
	if strings.TrimSpace(account.Provider) == "" {
		return fmt.Errorf("provider is required for account %s", account.Email)
	}
	stored, err := r.store.UpsertAccount(ctx, account)
	if err != nil {
		return err
	}

	runID, err := r.store.StartRun(ctx, stored.ID)
	if err != nil {
		return err
	}

	var runErr error
	defer func() {
		status := "success"
		if runErr != nil {
			status = "failed"
		}
		_ = r.store.FinishRun(ctx, runID, status, 0, errorText(runErr))
	}()

	checkpoint, err := r.store.GetCheckpoint(ctx, stored.ID)
	if err != nil {
		runErr = err
		return err
	}

	provider, err := r.selectProvider(stored)
	if err != nil {
		runErr = err
		return err
	}

	result, err := provider.Fetch(ctx, stored, checkpoint)
	if err != nil {
		runErr = err
		return err
	}

	digestResult, digestErr := BuildDigest(ctx, r.llmClient, stored, result.Messages)
	if digestErr != nil {
		r.logger.Warn("digest build failed, using fallback", "email", stored.Email, "err", digestErr)
	}

	inserted, err := r.store.InsertMessages(ctx, stored.ID, digestResult.Messages)
	if err != nil {
		runErr = err
		return err
	}

	lastUID := maxProviderUID(digestResult.Messages)
	if err := r.store.UpdateCheckpoint(ctx, stored.ID, result.Cursor, lastUID, result.LastSeen); err != nil {
		runErr = err
		return err
	}

	if inserted > 0 {
		if err := r.notifier.Send(ctx, formatTelegramDigest(stored, digestResult.Digest)); err != nil {
			r.logger.Error("telegram send failed", "email", stored.Email, "err", err)
		}
	}

	if err := r.store.FinishRun(ctx, runID, "success", inserted, ""); err != nil {
		return err
	}
	runErr = nil
	return nil
}

func (r *Runner) selectProvider(account Account) (Provider, error) {
	switch strings.ToLower(account.Provider) {
	case "gmail":
		if r.gmail == nil {
			return nil, fmt.Errorf("gmail provider not configured")
		}
		return r.gmail, nil
	case "imap", "icloud", "yandex":
		if r.imap == nil {
			return nil, fmt.Errorf("imap provider not configured")
		}
		return r.imap, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", account.Provider)
	}
}

func formatTelegramDigest(account Account, digest string) string {
	var sb strings.Builder
	sb.WriteString("📬 ")
	if account.Email != "" {
		sb.WriteString(account.Email)
	} else {
		sb.WriteString("Почта")
	}
	sb.WriteString("\n\n")
	sb.WriteString(strings.TrimSpace(digest))
	return sb.String()
}

func maxProviderUID(messages []FetchedMessage) string {
	var max uint64
	for _, msg := range messages {
		if msg.ProviderUID == "" {
			continue
		}
		var uid uint64
		if _, err := fmt.Sscanf(msg.ProviderUID, "%d", &uid); err != nil {
			continue
		}
		if uid > max {
			max = uid
		}
	}
	if max == 0 {
		return ""
	}
	return fmt.Sprintf("%d", max)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
