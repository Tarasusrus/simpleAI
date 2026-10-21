package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
)

var (
	clsFood = uuid.MustParse("00000000-0000-0000-0000-00000000c001")
	clsCafe = uuid.MustParse("00000000-0000-0000-0000-00000000c002")
	clsList = []categories.Candidate{
		{ID: clsFood, Name: "Продукты", Hint: "магазины, рынок"},
		{ID: clsCafe, Name: "Рестораны", Hint: "кафе, кофейни"},
	}
)

type clsServer struct {
	*httptest.Server
	calls  atomic.Int32
	body   atomic.Pointer[[]byte]
	header atomic.Pointer[http.Header]
}

// newClsServer отвечает content как сообщением ассистента; status != 200 — ошибкой прокси.
func newClsServer(t *testing.T, status int, content string) *clsServer {
	t.Helper()
	s := &clsServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		b, _ := io.ReadAll(r.Body) //nolint:errcheck // тестовый сервер
		s.body.Store(&b)
		h := r.Header.Clone()
		s.header.Store(&h)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"id": "x", "object": "chat.completion", "choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}}
		_ = json.NewEncoder(w).Encode(out) //nolint:errcheck // тестовый сервер
	}))
	t.Cleanup(s.Close)
	return s
}

func newCls(t *testing.T, s *clsServer) *ClassifierClient {
	t.Helper()
	c, err := NewClassifierClient(s.URL, "key", "standard", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Аргументы запроса: модель, temperature 0, json_schema strict c enum из id кандидатов, в user-сообщении
// только описание и список категорий, заголовок редакции сообщений в прокси.
//
// Мутации: убрать response_format, enum id, Temperature(0) или заголовок — тест краснеет.
func TestClassifier_RequestShape(t *testing.T) {
	s := newClsServer(t, 200, `{"action":"existing","category_id":"`+clsCafe.String()+`","name":null,"confidence":0.9,"reason":"кафе"}`)
	got, err := newCls(t, s).Classify(context.Background(), "поход в кафе", clsList)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != categories.ActionExisting || got.CategoryID != clsCafe || got.Confidence != 0.9 {
		t.Fatalf("разбор ответа: %+v", got)
	}

	var req struct {
		Model          string   `json:"model"`
		Temperature    *float64 `json:"temperature"`
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string `json:"name"`
				Strict bool   `json:"strict"`
				Schema struct {
					Required   []string `json:"required"`
					Properties struct {
						CategoryID struct {
							Enum []any `json:"enum"`
						} `json:"category_id"`
					} `json:"properties"`
				} `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(*s.body.Load(), &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "standard" || req.Temperature == nil || *req.Temperature != 0 {
		t.Errorf("модель/temperature: %q %v", req.Model, req.Temperature)
	}
	rf := req.ResponseFormat
	if rf.Type != "json_schema" || !rf.JSONSchema.Strict || rf.JSONSchema.Name == "" || len(rf.JSONSchema.Schema.Required) != 5 {
		t.Errorf("response_format: %+v", rf)
	}
	enum := rf.JSONSchema.Schema.Properties.CategoryID.Enum
	if len(enum) != 3 || enum[0] != clsFood.String() || enum[1] != clsCafe.String() || enum[2] != nil {
		t.Errorf("enum category_id = %v, ждали [id продуктов, id ресторанов, null]", enum)
	}
	var user string
	for _, m := range req.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(user), &in); err != nil {
		t.Fatalf("user-сообщение не JSON: %v", err)
	}
	if len(in) != 2 || in["description"] != "поход в кафе" {
		t.Errorf("в запросе должны быть только description и categories: %v", in)
	}
	if !strings.Contains(user, "Рестораны") || !strings.Contains(user, "кафе, кофейни") {
		t.Errorf("кандидаты с подсказками не ушли: %s", user)
	}
	if h := s.header.Load().Get("x-litellm-enable-message-redaction"); h != "true" {
		t.Errorf("заголовок редакции сообщений = %q", h)
	}
}

func TestClassifier_ParsesCreateAndNone(t *testing.T) {
	s := newClsServer(t, 200, `{"action":"create","category_id":null,"name":"Хобби","confidence":0.7,"reason":"новое"}`)
	got, err := newCls(t, s).Classify(context.Background(), "краски", clsList)
	if err != nil || got.Action != categories.ActionCreate || got.Name != "Хобби" || got.CategoryID != uuid.Nil {
		t.Fatalf("create: %+v err=%v", got, err)
	}
	s = newClsServer(t, 200, `{"action":"none","category_id":null,"name":null,"confidence":0.1,"reason":"?"}`)
	got, err = newCls(t, s).Classify(context.Background(), "ъъъ", clsList)
	if err != nil || got.Action != categories.ActionNone {
		t.Fatalf("none: %+v err=%v", got, err)
	}
}

// Сбои: ошибка прокси, не JSON, id не UUID, пустой ответ — ошибка; без ретраев (ровно один запрос).
func TestClassifier_Failures(t *testing.T) {
	for name, c := range map[string]struct {
		status  int
		content string
	}{
		"5xx":        {503, ""},
		"не JSON":    {200, "извини, не знаю"},
		"id не UUID": {200, `{"action":"existing","category_id":"abc","name":null,"confidence":1,"reason":""}`},
		"пусто":      {200, "  "},
	} {
		s := newClsServer(t, c.status, c.content)
		if _, err := newCls(t, s).Classify(context.Background(), "x", clsList); err == nil {
			t.Errorf("%s: ошибки нет", name)
		}
		if n := s.calls.Load(); n != 1 {
			t.Errorf("%s: запросов %d, ждали 1 (ретраев быть не должно)", name, n)
		}
	}
}

func TestNewClassifierClient_RequiresSettings(t *testing.T) {
	for _, a := range [][3]string{{"", "k", "m"}, {"http://x", "", "m"}, {"http://x", "k", " "}} {
		if _, err := NewClassifierClient(a[0], a[1], a[2], time.Second); err == nil {
			t.Errorf("%v: ошибки нет", a)
		}
	}
}
