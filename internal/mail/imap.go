// Package mail реализует сбор почты из провайдеров, нормализацию и запись в БД.
// Здесь же формируется LLM-дайджест и оркестрация запусков; основные точки входа Runner.RunOnce и Store.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
	"github.com/emersion/go-sasl"
)

const (
	imapDefaultPort = 993
	imapPreviewSize = 512
	imapMaxFetch    = 500
)

type IMAPProvider struct{}

func NewIMAPProvider() *IMAPProvider {
	return &IMAPProvider{}
}

func (p *IMAPProvider) Fetch(ctx context.Context, account Account, checkpoint Checkpoint) (FetchResult, error) {
	host, port, useTLS, err := resolveIMAPEndpoint(account)
	if err != nil {
		return FetchResult{}, err
	}

	client, err := dialIMAP(ctx, host, port, useTLS)
	if err != nil {
		return FetchResult{}, err
	}
	defer func() {
		if err := client.Logout(); err != nil {
			_ = err
		}
	}()

	if err := client.Authenticate(newXOAuth2Client(account.Email, account.AccessToken)); err != nil {
		return FetchResult{}, fmt.Errorf("imap authenticate failed: %w", err)
	}

	folders := account.Folders
	if len(folders) == 0 {
		folders = []string{"INBOX"}
	}

	var (
		allMessages []FetchedMessage
		lastSeen    = checkpoint.LastSeenAt
	)

	for _, folder := range folders {
		if _, err := client.Select(folder, true); err != nil {
			return FetchResult{}, fmt.Errorf("imap select %s failed: %w", folder, err)
		}

		uids, err := searchUIDs(client, checkpoint.LastSeenAt)
		if err != nil {
			return FetchResult{}, err
		}
		if len(uids) == 0 {
			continue
		}

		if checkpoint.LastUID != "" {
			uids = filterUIDs(uids, checkpoint.LastUID)
		}
		if len(uids) == 0 {
			continue
		}

		if len(uids) > imapMaxFetch {
			uids = uids[len(uids)-imapMaxFetch:]
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uids...)
		section := &imap.BodySectionName{
			Peek:    true,
			Partial: []int{0, imapPreviewSize},
		}
		items := []imap.FetchItem{
			imap.FetchEnvelope,
			imap.FetchUid,
			imap.FetchInternalDate,
			section.FetchItem(),
		}

		messages := make(chan *imap.Message, 16)
		done := make(chan error, 1)
		go func() {
			done <- client.UidFetch(seqset, items, messages)
		}()

		for msg := range messages {
			if msg == nil || msg.Envelope == nil {
				continue
			}
			fetched := mapIMAPMessage(msg, section, folder)
			allMessages = append(allMessages, fetched)
			if fetched.ReceivedAt != nil {
				if lastSeen == nil || fetched.ReceivedAt.After(*lastSeen) {
					ls := *fetched.ReceivedAt
					lastSeen = &ls
				}
			}
		}

		if err := <-done; err != nil {
			return FetchResult{}, fmt.Errorf("imap fetch failed: %w", err)
		}
	}

	return FetchResult{
		Messages: allMessages,
		LastSeen: lastSeen,
		HasMore:  false,
	}, nil
}

func resolveIMAPEndpoint(account Account) (string, int, bool, error) {
	host := strings.TrimSpace(account.Host)
	port := account.Port
	useTLS := true
	if account.UseTLS != nil {
		useTLS = *account.UseTLS
	}

	if host == "" {
		switch strings.ToLower(account.Provider) {
		case "icloud":
			host = "imap.mail.me.com"
		case "yandex":
			host = "imap.yandex.com"
		case "gmail":
			host = "imap.gmail.com"
		default:
			return "", 0, useTLS, fmt.Errorf("imap host not configured for provider %q", account.Provider)
		}
	}

	if port == 0 {
		port = imapDefaultPort
	}

	return host, port, useTLS, nil
}

func dialIMAP(ctx context.Context, host string, port int, useTLS bool) (*imapclient.Client, error) {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := net.Dialer{}

	if useTLS {
		tlsConfig := &tls.Config{ServerName: host}
		conn, err := tls.DialWithDialer(&dialer, "tcp", address, tlsConfig)
		if err != nil {
			return nil, err
		}
		return imapclient.New(conn)
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return imapclient.New(conn)
}

func searchUIDs(client *imapclient.Client, lastSeen *time.Time) ([]uint32, error) {
	criteria := imap.NewSearchCriteria()
	if lastSeen != nil {
		criteria.Since = *lastSeen
	}
	return client.UidSearch(criteria)
}

func filterUIDs(uids []uint32, lastUID string) []uint32 {
	if lastUID == "" {
		return uids
	}
	last, err := parseUID(lastUID)
	if err != nil {
		return uids
	}
	filtered := make([]uint32, 0, len(uids))
	for _, uid := range uids {
		if uid > last {
			filtered = append(filtered, uid)
		}
	}
	return filtered
}

func parseUID(raw string) (uint32, error) {
	var uid uint32
	if _, err := fmt.Sscanf(raw, "%d", &uid); err != nil {
		return 0, err
	}
	return uid, nil
}

func mapIMAPMessage(msg *imap.Message, section *imap.BodySectionName, folder string) FetchedMessage {
	envelope := msg.Envelope
	from := ""
	if len(envelope.From) > 0 {
		from = formatIMAPAddress(envelope.From[0])
	}
	received := envelope.Date
	if received.IsZero() {
		received = msg.InternalDate
	}
	var receivedPtr *time.Time
	if !received.IsZero() {
		receivedPtr = &received
	}

	preview := ""
	if body := msg.GetBody(section); body != nil {
		if data, err := io.ReadAll(body); err == nil {
			preview = strings.TrimSpace(string(data))
		}
	}

	providerUID := fmt.Sprintf("%d", msg.Uid)

	meta := map[string]any{
		"folder": folder,
	}

	return FetchedMessage{
		MessageID:   envelope.MessageId,
		ProviderUID: providerUID,
		FromEmail:   from,
		Subject:     envelope.Subject,
		ReceivedAt:  receivedPtr,
		Preview:     preview,
		Metadata:    meta,
	}
}

func formatIMAPAddress(addr *imap.Address) string {
	if addr == nil {
		return ""
	}
	email := strings.TrimSpace(addr.MailboxName + "@" + addr.HostName)
	if strings.TrimSpace(addr.PersonalName) == "" {
		return email
	}
	return fmt.Sprintf("%s <%s>", addr.PersonalName, email)
}

type xoauth2Client struct {
	username string
	token    string
	started  bool
}

func newXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

func (c *xoauth2Client) Start() (string, []byte, error) {
	if c.started {
		return "", nil, sasl.ErrUnexpectedClientResponse
	}
	c.started = true
	resp := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.username, c.token)
	return "XOAUTH2", []byte(resp), nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	if len(challenge) > 0 {
		return nil, sasl.ErrUnexpectedServerChallenge
	}
	return nil, nil
}
