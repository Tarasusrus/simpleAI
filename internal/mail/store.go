package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) UpsertAccount(ctx context.Context, account Account) (Account, error) {
	var (
		id        uuid.UUID
		createdAt time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, created_at
		FROM mail_account
		WHERE provider = $1 AND email = $2
	`, account.Provider, account.Email).Scan(&id, &createdAt)

	if err != nil && err != pgx.ErrNoRows {
		return Account{}, err
	}

	if err == pgx.ErrNoRows {
		id = uuid.New()
		createdAt = time.Now().UTC()
		_, err := s.pool.Exec(ctx, `
			INSERT INTO mail_account (
				id, provider, email, access_token, refresh_token, client_id, client_secret,
				labels, folders, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, id, account.Provider, account.Email,
			emptyToNull(account.AccessToken), emptyToNull(account.RefreshToken),
			emptyToNull(account.ClientID), emptyToNull(account.ClientSecret),
			account.Labels, account.Folders, createdAt,
		)
		if err != nil {
			return Account{}, err
		}
	} else {
		_, err := s.pool.Exec(ctx, `
			UPDATE mail_account
			SET access_token = $1,
			    refresh_token = $2,
			    client_id = $3,
			    client_secret = $4,
			    labels = $5,
			    folders = $6
			WHERE id = $7
		`, emptyToNull(account.AccessToken), emptyToNull(account.RefreshToken),
			emptyToNull(account.ClientID), emptyToNull(account.ClientSecret),
			account.Labels, account.Folders, id,
		)
		if err != nil {
			return Account{}, err
		}
	}

	account.ID = id.String()
	account.CreatedAt = createdAt
	return account, nil
}

func (s *Store) GetCheckpoint(ctx context.Context, accountID string) (Checkpoint, error) {
	var cp Checkpoint
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, cursor, last_uid, last_seen_at, updated_at
		FROM mail_checkpoint
		WHERE account_id = $1
	`, accountID).Scan(&cp.ID, &cp.AccountID, &cp.Cursor, &cp.LastUID, &cp.LastSeenAt, &cp.UpdatedAt)

	if err == pgx.ErrNoRows {
		id := uuid.New()
		now := time.Now().UTC()
		_, err := s.pool.Exec(ctx, `
			INSERT INTO mail_checkpoint (id, account_id, updated_at)
			VALUES ($1, $2, $3)
		`, id, accountID, now)
		if err != nil {
			return Checkpoint{}, err
		}
		return Checkpoint{
			ID:        id.String(),
			AccountID: accountID,
			UpdatedAt: now,
		}, nil
	}
	if err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

func (s *Store) UpdateCheckpoint(ctx context.Context, accountID string, cursor string, lastUID string, lastSeen *time.Time) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mail_checkpoint (id, account_id, cursor, last_uid, last_seen_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (account_id)
		DO UPDATE SET cursor = EXCLUDED.cursor,
		              last_uid = EXCLUDED.last_uid,
		              last_seen_at = EXCLUDED.last_seen_at,
		              updated_at = EXCLUDED.updated_at
	`, uuid.New(), accountID, emptyToNull(cursor), emptyToNull(lastUID), lastSeen, now)
	return err
}

func (s *Store) StartRun(ctx context.Context, accountID string) (string, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mail_run (id, account_id, status, started_at)
		VALUES ($1, $2, $3, $4)
	`, id, emptyToNull(accountID), "running", time.Now().UTC())
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (s *Store) FinishRun(ctx context.Context, runID string, status string, count int, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mail_run
		SET status = $1,
		    finished_at = $2,
		    messages_count = $3,
		    error_text = $4
		WHERE id = $5
	`, status, time.Now().UTC(), count, emptyToNull(errText), runID)
	return err
}

func (s *Store) InsertMessages(ctx context.Context, accountID string, messages []FetchedMessage) (int, error) {
	inserted := 0
	for _, msg := range messages {
		metaPayload, err := json.Marshal(msg.Metadata)
		if err != nil {
			return inserted, err
		}
		fromEmail := strings.TrimSpace(msg.FromEmail)
		if fromEmail == "" {
			fromEmail = "unknown"
		}
		subject := strings.TrimSpace(msg.Subject)
		if subject == "" {
			subject = "unknown"
		}
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO mail_message (
				id, account_id, message_id, provider_uid, from_email, subject,
				received_at, preview, category, importance, metadata
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT DO NOTHING
		`, uuid.New(), accountID,
			emptyToNull(msg.MessageID),
			emptyToNull(msg.ProviderUID),
			fromEmail,
			subject,
			msg.ReceivedAt,
			emptyToNull(msg.Preview),
			emptyToNull(msg.MetadataString("category")),
			emptyToNull(msg.MetadataString("importance")),
			metaPayload,
		)
		if err != nil {
			return inserted, err
		}
		if tag.RowsAffected() == 1 {
			inserted++
		}
	}
	return inserted, nil
}

func emptyToNull(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}

func (m FetchedMessage) MetadataString(key string) string {
	if m.Metadata == nil {
		return ""
	}
	value, ok := m.Metadata[key]
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}
