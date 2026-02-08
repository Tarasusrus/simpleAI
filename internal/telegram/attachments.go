// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"simpleAI/internal/core"
)

// AttachmentFetcher получает содержимое вложения по идентификатору.
type AttachmentFetcher interface {
	FetchAttachment(ctx context.Context, attachment core.Attachment) (io.ReadCloser, string, error)
}

func SaveAttachments(ctx context.Context, fetcher AttachmentFetcher, attachments []core.Attachment, dir string) ([]string, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("media dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(attachments))
	for _, att := range attachments {
		name := fileNameForAttachment(att)
		target := filepath.Join(dir, name)
		remotePath := ""
		if att.Path != "" {
			remotePath = att.Path
			if err := copyFile(att.Path, target); err != nil {
				return paths, err
			}
		} else {
			if fetcher == nil {
				return paths, fmt.Errorf("attachment fetcher is nil")
			}
			reader, filePath, err := fetcher.FetchAttachment(ctx, att)
			if err != nil {
				return paths, err
			}
			remotePath = filePath
			if err := writeFromReader(target, reader); err != nil {
				return paths, err
			}
		}
		if remotePath != "" {
			if newTarget, err := renameWithExt(target, remotePath); err == nil && newTarget != "" {
				target = newTarget
			}
		}
		paths = append(paths, target)
	}
	return paths, nil
}

func fileNameForAttachment(att core.Attachment) string {
	base := ""
	if att.Name != "" {
		base = filepath.Base(att.Name)
	}
	ext := filepath.Ext(base)
	if ext == "" {
		ext = ".bin"
	}
	id := sanitizeName(att.ID)
	if id == "" {
		id = time.Now().UTC().Format("20060102T150405")
	}
	kind := strings.TrimSpace(att.Kind)
	if kind == "" {
		kind = "file"
	}
	return fmt.Sprintf("%s_%s%s", kind, id, ext)
}

func sanitizeName(raw string) string {
	if raw == "" {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, raw)
	return strings.Trim(clean, "_")
}

func writeFromReader(target string, reader io.ReadCloser) error {
	defer func() {
		if err := reader.Close(); err != nil {
			_ = err
		}
	}()
	tmp := target + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, reader); err != nil {
		if cerr := out.Close(); cerr != nil {
			return cerr
		}
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func copyFile(src, target string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := in.Close(); err != nil {
			_ = err
		}
	}()
	return writeFromReader(target, in)
}

func renameWithExt(target string, filePath string) (string, error) {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "", nil
	}
	currentExt := filepath.Ext(target)
	if currentExt == ext {
		return target, nil
	}
	newPath := strings.TrimSuffix(target, currentExt) + ext
	return newPath, os.Rename(target, newPath)
}
