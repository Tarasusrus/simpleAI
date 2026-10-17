// Package litellm читает возможности моделей из прокси LiteLLM
// (GET /model/info) и отдаёт их как core.ModelCatalog.
package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"simpleAI/internal/core"
)

// maxResponseBytes — потолок ответа /model/info. Живой ответ на 55
// деплойментов весит около 0,3 МБ; запас на рост списка моделей.
const maxResponseBytes = 8 << 20

// Catalog — core.ModelCatalog поверх LiteLLM /model/info. Успешный ответ
// кэшируется на ttl: прокси, поднятый после старта бота, подхватывается без
// рестарта, а каждое фото не стоит лишнего запроса. Ошибка не кэшируется.
type Catalog struct {
	baseURL string
	apiKey  string
	client  *http.Client
	ttl     time.Duration
	now     func() time.Time

	mu        sync.Mutex
	cached    []core.ModelInfo
	fetchedAt time.Time
}

// NewCatalog: baseURL — тот же адрес, что для вызовов модели (обычно с /v1);
// LiteLLM отдаёт /model/info и под /v1.
func NewCatalog(baseURL, apiKey string, client *http.Client, ttl time.Duration) *Catalog {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Catalog{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  client,
		ttl:     ttl,
		now:     time.Now,
	}
}

// Models не держит блокировку на время запроса: медленный или висящий
// прокси иначе выстроил бы в очередь все одновременные фото. Два запроса
// при истёкшем кэше могут сходить в прокси оба — это дешевле очереди.
func (c *Catalog) Models(ctx context.Context) ([]core.ModelInfo, error) {
	c.mu.Lock()
	cached, fresh := c.cached, c.cached != nil && c.now().Sub(c.fetchedAt) < c.ttl
	c.mu.Unlock()
	if fresh {
		return cached, nil
	}
	models, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cached, c.fetchedAt = models, c.now()
	c.mu.Unlock()
	return models, nil
}

type modelInfoResponse struct {
	Data []struct {
		ModelName string `json:"model_name"`
		ModelInfo struct {
			SupportsVision   *bool `json:"supports_vision"`
			SupportsPDFInput *bool `json:"supports_pdf_input"`
		} `json:"model_info"`
	} `json:"data"`
}

func (c *Catalog) fetch(ctx context.Context) ([]core.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/model/info", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("litellm: запрос /model/info: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: /model/info: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("litellm: /model/info: статус %d", resp.StatusCode)
	}
	var body modelInfoResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("litellm: разбор /model/info: %w", err)
	}

	// Запрос к группе может уйти в любой её деплоймент, поэтому группа умеет
	// флаг, только если он true у всех. Отсутствующий флаг (null) — «нет»:
	// LiteLLM не знает возможностей моделей на сторонних хостах.
	groups := make(map[string]*core.ModelCapabilities)
	for _, d := range body.Data {
		caps, seen := groups[d.ModelName]
		if !seen {
			caps = &core.ModelCapabilities{Vision: true, PDFInput: true}
			groups[d.ModelName] = caps
		}
		caps.Vision = caps.Vision && isTrue(d.ModelInfo.SupportsVision)
		caps.PDFInput = caps.PDFInput && isTrue(d.ModelInfo.SupportsPDFInput)
	}

	out := make([]core.ModelInfo, 0, len(groups))
	for name, caps := range groups {
		out = append(out, core.ModelInfo{Name: name, Capabilities: *caps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func isTrue(b *bool) bool { return b != nil && *b }
