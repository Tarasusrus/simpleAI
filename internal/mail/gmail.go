package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	gmailBaseURL    = "https://gmail.googleapis.com/gmail/v1"
	gmailMaxResults = 50
	gmailMaxPages   = 10
)

type GmailProvider struct {
	client  *http.Client
	baseURL string
}

func NewGmailProvider(client *http.Client) *GmailProvider {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &GmailProvider{
		client:  client,
		baseURL: gmailBaseURL,
	}
}

func (g *GmailProvider) Fetch(ctx context.Context, account Account, checkpoint Checkpoint) (FetchResult, error) {
	query := buildGmailQuery(checkpoint.LastSeenAt)
	labels := account.Labels
	if len(labels) == 0 {
		labels = []string{"INBOX"}
	}

	var (
		allMessages []FetchedMessage
		pageToken   = checkpoint.Cursor
		lastSeen    = checkpoint.LastSeenAt
	)

	for page := 0; page < gmailMaxPages; page++ {
		listResp, err := g.listMessages(ctx, account.AccessToken, labels, query, pageToken)
		if err != nil {
			return FetchResult{}, err
		}

		for _, msg := range listResp.Messages {
			meta, err := g.getMessageMetadata(ctx, account.AccessToken, msg.ID)
			if err != nil {
				return FetchResult{}, err
			}
			allMessages = append(allMessages, meta)
			if meta.ReceivedAt != nil {
				if lastSeen == nil || meta.ReceivedAt.After(*lastSeen) {
					ls := *meta.ReceivedAt
					lastSeen = &ls
				}
			}
		}

		if listResp.NextPageToken == "" {
			return FetchResult{
				Messages: allMessages,
				Cursor:   "",
				LastSeen: lastSeen,
				HasMore:  false,
			}, nil
		}

		pageToken = listResp.NextPageToken
	}

	return FetchResult{
		Messages: allMessages,
		Cursor:   pageToken,
		LastSeen: lastSeen,
		HasMore:  true,
	}, nil
}

type gmailListResponse struct {
	Messages      []gmailMessageRef `json:"messages"`
	NextPageToken string            `json:"nextPageToken"`
}

type gmailMessageRef struct {
	ID string `json:"id"`
}

func (g *GmailProvider) listMessages(ctx context.Context, accessToken string, labels []string, query, pageToken string) (gmailListResponse, error) {
	if strings.TrimSpace(accessToken) == "" {
		return gmailListResponse{}, fmt.Errorf("gmail access token is required")
	}

	params := url.Values{}
	params.Set("maxResults", fmt.Sprintf("%d", gmailMaxResults))
	if query != "" {
		params.Set("q", query)
	}
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}
	for _, label := range labels {
		params.Add("labelIds", label)
	}

	endpoint := fmt.Sprintf("%s/users/me/messages?%s", g.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return gmailListResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := g.client.Do(req)
	if err != nil {
		return gmailListResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return gmailListResponse{}, fmt.Errorf("gmail list messages failed: status=%d", resp.StatusCode)
	}

	var payload gmailListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return gmailListResponse{}, err
	}
	return payload, nil
}

type gmailMessageResponse struct {
	ID      string              `json:"id"`
	Snippet string              `json:"snippet"`
	Payload gmailMessagePayload `json:"payload"`
}

type gmailMessagePayload struct {
	Headers []gmailHeader `json:"headers"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (g *GmailProvider) getMessageMetadata(ctx context.Context, accessToken, messageID string) (FetchedMessage, error) {
	if strings.TrimSpace(accessToken) == "" {
		return FetchedMessage{}, fmt.Errorf("gmail access token is required")
	}
	endpoint := fmt.Sprintf("%s/users/me/messages/%s?format=metadata&metadataHeaders=From&metadataHeaders=Subject&metadataHeaders=Date", g.baseURL, messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FetchedMessage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := g.client.Do(req)
	if err != nil {
		return FetchedMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return FetchedMessage{}, fmt.Errorf("gmail get message failed: status=%d", resp.StatusCode)
	}

	var payload gmailMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return FetchedMessage{}, err
	}

	headerMap := map[string]string{}
	for _, header := range payload.Payload.Headers {
		headerMap[strings.ToLower(header.Name)] = header.Value
	}

	var receivedAt *time.Time
	if rawDate := headerMap["date"]; rawDate != "" {
		if parsed, err := time.Parse(time.RFC1123Z, rawDate); err == nil {
			receivedAt = &parsed
		} else if parsed, err := time.Parse(time.RFC1123, rawDate); err == nil {
			receivedAt = &parsed
		}
	}

	return FetchedMessage{
		MessageID:   payload.ID,
		ProviderUID: payload.ID,
		FromEmail:   headerMap["from"],
		Subject:     headerMap["subject"],
		ReceivedAt:  receivedAt,
		Preview:     payload.Snippet,
		Metadata: map[string]any{
			"raw_headers": headerMap,
		},
	}, nil
}

func buildGmailQuery(lastSeen *time.Time) string {
	if lastSeen == nil {
		return ""
	}
	return fmt.Sprintf("after:%d", lastSeen.Unix())
}
