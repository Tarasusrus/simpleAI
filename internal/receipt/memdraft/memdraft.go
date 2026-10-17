// Package memdraft — receipt.DraftStore в памяти процесса. После рестарта
// черновики теряются: пользователь пришлёт фото ещё раз (ADR-010 §5).
package memdraft

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"simpleAI/internal/receipt"
)

type entry struct {
	chatID  int64
	receipt receipt.Receipt
	expires time.Time
}

type Store struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]entry
}

func New(ttl time.Duration) *Store {
	return &Store{ttl: ttl, now: time.Now, entries: make(map[string]entry)}
}

// idBytes: 8 случайных байт — 16 hex-символов; с префиксом callback это
// укладывается в 64 байта, которые Telegram даёт на данные кнопки.
const idBytes = 8

func (s *Store) Put(chatID int64, r receipt.Receipt) (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("memdraft: id: %w", err)
	}
	id := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	// Черновики, которые так и не подтвердили, иначе копились бы до рестарта.
	for k, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, k)
		}
	}
	s.entries[id] = entry{chatID: chatID, receipt: r, expires: now.Add(s.ttl)}
	return id, nil
}

func (s *Store) Take(chatID int64, id string) (receipt.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || e.chatID != chatID {
		return receipt.Receipt{}, receipt.ErrDraftNotFound
	}
	delete(s.entries, id)
	if !s.now().Before(e.expires) {
		return receipt.Receipt{}, receipt.ErrDraftNotFound
	}
	return e.receipt, nil
}
