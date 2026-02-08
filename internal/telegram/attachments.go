package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func SaveAttachments(ctx context.Context, bot *tgbotapi.BotAPI, attachments []Attachment, dir string) ([]string, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("media dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	paths := make([]string, 0, len(attachments))
	for _, att := range attachments {
		file, err := bot.GetFile(tgbotapi.FileConfig{FileID: att.FileID})
		if err != nil {
			return paths, err
		}
		url := file.Link(bot.Token)
		name := fileNameForAttachment(att, file.FilePath)
		target := filepath.Join(dir, name)
		if err := downloadFile(ctx, client, url, target); err != nil {
			return paths, err
		}
		paths = append(paths, target)
	}
	return paths, nil
}

func fileNameForAttachment(att Attachment, filePath string) string {
	if att.FileName != "" {
		return filepath.Base(att.FileName)
	}
	if filePath != "" {
		return filepath.Base(filePath)
	}
	ts := time.Now().UTC().Format("20060102T150405")
	return fmt.Sprintf("%s_%s", att.Type, ts)
}

func downloadFile(ctx context.Context, client *http.Client, url string, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status=%d", resp.StatusCode)
	}

	tmp := target + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
