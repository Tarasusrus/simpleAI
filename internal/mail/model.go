package mail

import "time"

type Account struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Email        string    `json:"email"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	UseTLS       *bool     `json:"use_tls"`
	Labels       []string  `json:"labels"`
	Folders      []string  `json:"folders"`
	CreatedAt    time.Time `json:"created_at"`
}

type Message struct {
	ID          string         `json:"id"`
	AccountID   string         `json:"account_id"`
	MessageID   string         `json:"message_id"`
	ProviderUID string         `json:"provider_uid"`
	FromEmail   string         `json:"from_email"`
	Subject     string         `json:"subject"`
	ReceivedAt  *time.Time     `json:"received_at"`
	Preview     string         `json:"preview"`
	Category    string         `json:"category"`
	Importance  string         `json:"importance"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Checkpoint struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"account_id"`
	Cursor     string     `json:"cursor"`
	LastUID    string     `json:"last_uid"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type Run struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	MessagesCount int        `json:"messages_count"`
	ErrorText     string     `json:"error_text"`
}
