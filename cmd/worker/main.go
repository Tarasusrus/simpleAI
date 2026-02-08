// Package main содержит код пакета main и его задачи.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"simpleAI/config"
	"simpleAI/internal/db"
	"simpleAI/internal/mail"
	"simpleAI/internal/notify"
	"simpleAI/internal/tools"
	"simpleAI/pkg/llm"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Err while load the config: ", err)
	}

	logger := tools.NewLogger()
	pool, err := db.NewPool(context.Background(), cfg.DB)
	if err != nil {
		log.Fatal("Err while init db pool: ", err)
	}
	defer pool.Close()

	store := mail.NewStore(pool)
	gmailProvider := mail.NewGmailProvider(&http.Client{Timeout: 15 * time.Second})
	imapProvider := mail.NewIMAPProvider()
	telegram := notify.NewTelegram(cfg.Mail.Telegram.Token, cfg.Mail.Telegram.ChatID)
	llmClient := llm.NewClient(cfg.APIKey, logger, cfg)

	runner := mail.NewRunner(store, gmailProvider, imapProvider, telegram, llmClient, logger)

	if len(cfg.Mail.Accounts) == 0 {
		logger.Warn("no mail accounts configured")
		return
	}

	if runOnce() {
		if err := runner.RunOnce(context.Background(), mapAccounts(cfg.Mail.Accounts)); err != nil {
			logger.Error("mail worker run failed", "err", err)
		}
		return
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		if err := runner.RunOnce(context.Background(), mapAccounts(cfg.Mail.Accounts)); err != nil {
			logger.Error("mail worker run failed", "err", err)
		}
		<-ticker.C
	}
}

func runOnce() bool {
	if v := os.Getenv("MAIL_RUN_ONCE"); v != "" && v != "0" && v != "false" {
		return true
	}
	return false
}

func mapAccounts(accounts []config.MailAccount) []mail.Account {
	out := make([]mail.Account, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, mail.Account{
			Provider:     account.Provider,
			Email:        account.Email,
			AccessToken:  account.AccessToken,
			RefreshToken: account.RefreshToken,
			ClientID:     account.ClientID,
			ClientSecret: account.ClientSecret,
			Host:         account.Host,
			Port:         account.Port,
			UseTLS:       account.UseTLS,
			Labels:       account.Labels,
			Folders:      account.Folders,
		})
	}
	return out
}
