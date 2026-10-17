package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"simpleAI/internal/core"
)

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
	File struct {
		FileData string `json:"file_data"`
	} `json:"file"`
}

type capturedRequest struct {
	Model       string   `json:"model"`
	MaxTokens   *int     `json:"max_tokens"`
	Temperature *float64 `json:"temperature"`
	Messages    []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// userParts — content-части единственного user-сообщения.
func (r capturedRequest) userParts(t *testing.T) []contentPart {
	t.Helper()
	for _, m := range r.Messages {
		if m.Role != "user" {
			continue
		}
		var parts []contentPart
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			t.Fatalf("user content is not a list of parts: %v", err)
		}
		return parts
	}
	t.Fatal("no user message")
	return nil
}

type visionServer struct {
	*httptest.Server
	calls     atomic.Int32
	last      atomic.Pointer[capturedRequest]
	redaction atomic.Value // заголовок x-litellm-enable-message-redaction
}

func newVisionServer(t *testing.T, status int, reply string) *visionServer {
	t.Helper()
	s := &visionServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.redaction.Store(r.Header.Get("x-litellm-enable-message-redaction"))
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var req capturedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		s.last.Store(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(reply)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *visionServer) lastRequest(t *testing.T) capturedRequest {
	t.Helper()
	r := s.last.Load()
	if r == nil {
		t.Fatal("no request captured")
	}
	return *r
}

func okReply(t *testing.T, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"id": "x", "object": "chat.completion", "created": 1, "model": "vision",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTestVisionClient(t *testing.T, url string) *VisionClient {
	t.Helper()
	c, err := NewVisionClient(url, "key", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAskVision_SendsImageAsDataURLToRequestedModel(t *testing.T) {
	s := newVisionServer(t, http.StatusOK, okReply(t, `{"is_receipt":true}`))
	img := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}

	got, err := newTestVisionClient(t, s.URL).AskVision(context.Background(), core.VisionRequest{
		Model: "vision", System: "sys", Prompt: "is it a receipt?",
		MimeType: "image/jpeg", Data: img, MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("AskVision: %v", err)
	}
	if got != `{"is_receipt":true}` {
		t.Fatalf("reply = %q", got)
	}

	if got, ok := s.redaction.Load().(string); !ok || got != "true" {
		t.Errorf("x-litellm-enable-message-redaction = %q, want true: photo must not reach proxy logs", got)
	}
	req := s.lastRequest(t)
	if req.Model != "vision" {
		t.Errorf("model = %q, want vision", req.Model)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 10 {
		t.Errorf("max_tokens = %v, want 10", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", req.Temperature)
	}
	parts := req.userParts(t)
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "image_url" {
		t.Fatalf("user parts = %+v, want text + image_url", parts)
	}
	url := parts[1].ImageURL.URL
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("url = %.40q, want %q prefix", url, prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil || !bytes.Equal(decoded, img) {
		t.Fatalf("image bytes changed in transit")
	}
}

func TestAskVision_SendsPDFAsFilePart(t *testing.T) {
	s := newVisionServer(t, http.StatusOK, okReply(t, "ok"))
	_, err := newTestVisionClient(t, s.URL).AskVision(context.Background(), core.VisionRequest{
		Model: "vision", Prompt: "parse", MimeType: "application/pdf", Data: []byte("%PDF-1.7"),
	})
	if err != nil {
		t.Fatalf("AskVision: %v", err)
	}
	req := s.lastRequest(t)
	parts := req.userParts(t)
	if len(parts) != 2 || parts[1].Type != "file" {
		t.Fatalf("user parts = %+v, want text + file", parts)
	}
	if !strings.HasPrefix(parts[1].File.FileData, "data:application/pdf;base64,") {
		t.Fatalf("file_data = %.40q", parts[1].File.FileData)
	}
	if req.MaxTokens != nil {
		t.Errorf("max_tokens sent although not requested")
	}
	if len(req.Messages) != 1 {
		t.Errorf("messages = %d, want only user message when System is empty", len(req.Messages))
	}
}

// Картинка не должна уйти другой модели: ни повтора, ни fallback-клиента.
func TestAskVision_WhenUpstreamFails_ShouldNotRetryOrSwitchModel(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError} {
		s := newVisionServer(t, status, `{"error":{"message":"busy"}}`)
		_, err := newTestVisionClient(t, s.URL).AskVision(context.Background(), core.VisionRequest{
			Model: "vision", Prompt: "p", MimeType: "image/png", Data: []byte("\x89PNG"),
		})
		if err == nil {
			t.Fatalf("status %d: want error", status)
		}
		if n := s.calls.Load(); n != 1 {
			t.Fatalf("status %d: calls = %d, want 1", status, n)
		}
	}
}

func TestAskVision_WhenReplyIsEmpty_ShouldFail(t *testing.T) {
	tests := []struct {
		name  string
		reply string
	}{
		{"no choices", `{"id":"x","object":"chat.completion","created":1,"model":"vision","choices":[]}`},
		{"blank content", okReply(t, "   ")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newVisionServer(t, http.StatusOK, tt.reply)
			_, err := newTestVisionClient(t, s.URL).AskVision(context.Background(), core.VisionRequest{
				Model: "vision", Prompt: "p", MimeType: "image/png", Data: []byte("x"),
			})
			if err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestNewVisionClient_RequiresURLAndKey(t *testing.T) {
	for _, c := range [][2]string{{"", "k"}, {"http://x", ""}, {" ", " "}} {
		if _, err := NewVisionClient(c[0], c[1], time.Second); err == nil {
			t.Errorf("NewVisionClient(%q, %q): want error", c[0], c[1])
		}
	}
}
