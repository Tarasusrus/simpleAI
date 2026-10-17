package memdraft

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"simpleAI/internal/receipt"
)

var r = receipt.Receipt{Merchant: "7-Eleven", Currency: "THB", Total: 100}

func newStore(ttl time.Duration) (*Store, *time.Time) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	s := New(ttl)
	s.now = func() time.Time { return now }
	return s, &now
}

func mustPut(t *testing.T, s *Store, chatID int64) string {
	t.Helper()
	id, err := s.Put(chatID, r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTake_ReturnsDraftOnce(t *testing.T) {
	s, _ := newStore(time.Minute)
	id := mustPut(t, s, 1)
	got, err := s.Take(1, id)
	if err != nil || got.Total != r.Total {
		t.Fatalf("first Take = %+v, %v", got, err)
	}
	if _, err := s.Take(1, id); !errors.Is(err, receipt.ErrDraftNotFound) {
		t.Fatalf("second Take err = %v, want ErrDraftNotFound", err)
	}
}

// Нажатие из чужого чата не получает черновик и не сжигает его владельцу.
func TestTake_WhenChatIsForeign_ShouldNotConsume(t *testing.T) {
	s, _ := newStore(time.Minute)
	id := mustPut(t, s, 1)
	if _, err := s.Take(2, id); !errors.Is(err, receipt.ErrDraftNotFound) {
		t.Fatalf("foreign Take err = %v, want ErrDraftNotFound", err)
	}
	if _, err := s.Take(1, id); err != nil {
		t.Fatalf("owner Take after foreign attempt: %v", err)
	}
}

func TestTake_WhenExpired_ShouldFail(t *testing.T) {
	s, now := newStore(time.Minute)
	id := mustPut(t, s, 1)
	*now = now.Add(time.Minute)
	if _, err := s.Take(1, id); !errors.Is(err, receipt.ErrDraftNotFound) {
		t.Fatalf("err = %v, want ErrDraftNotFound", err)
	}
}

func TestPut_DropsExpiredDrafts(t *testing.T) {
	s, now := newStore(time.Minute)
	mustPut(t, s, 1)
	*now = now.Add(2 * time.Minute)
	mustPut(t, s, 1)
	if n := len(s.entries); n != 1 {
		t.Fatalf("entries = %d, want 1 after sweep", n)
	}
}

func TestPut_IDsAreUniqueAndFitCallbackData(t *testing.T) {
	s, _ := newStore(time.Minute)
	seen := map[string]bool{}
	for range 100 {
		id := mustPut(t, s, 1)
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		if len("rcpt:ok:"+id) > 64 {
			t.Fatalf("callback data %d bytes > 64", len("rcpt:ok:"+id))
		}
	}
}

// Двойное нажатие приходит конкурентно: расход должен получить ровно один.
func TestTake_ConcurrentTakesYieldOneWinner(t *testing.T) {
	s, _ := newStore(time.Minute)
	id := mustPut(t, s, 1)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Take(1, id); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := wins.Load(); n != 1 {
		t.Fatalf("winners = %d, want 1", n)
	}
}
