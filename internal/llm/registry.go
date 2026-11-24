package llm

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
)

// ProviderConfig 是一個 LLM provider 的設定（設定檔 llm.providers.<id>，LLM 規格的介面）。
type ProviderConfig struct {
	ID      string
	Type    string // TypeAnthropic、TypeOpenAI 或 TypeGemini
	BaseURL string
	Limits  Limits
}

// ProviderIDPattern 是 provider id 允許的字元：id 會變成環境變數名稱的一部分。
var ProviderIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Builtin 回傳內建的 provider：anthropic、openai（L6），以及 M3 的 gemini（GEM-R1，只用於 stages.note.engine: gemini）。
func Builtin() map[string]ProviderConfig {
	return map[string]ProviderConfig{
		"anthropic": {ID: "anthropic", Type: TypeAnthropic, BaseURL: DefaultAnthropicBaseURL},
		"openai":    {ID: "openai", Type: TypeOpenAI, BaseURL: DefaultOpenAIBaseURL},
		"gemini":    {ID: "gemini", Type: TypeGemini, BaseURL: DefaultGeminiBaseURL},
	}
}

// KeyEnvVar 回傳 provider 的 API key 環境變數（L6）：NOTESTEME_<ID 大寫，- 換成 _>_API_KEY。
func KeyEnvVar(id string) string {
	return "NOTESTEME_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_API_KEY"
}

// Registry 是可用的 LLM provider：內建的加上設定檔的（同名時設定檔的取代內建的），以及 API key 的來源與限流。
type Registry struct {
	providers map[string]ProviderConfig

	// HTTP 是對外連線用的 client，必須是 netguard 的（ADR-0014）。
	HTTP *http.Client
	// Env 是程序的環境變數；API key 先看環境變數（SEC-R1）。
	Env map[string]string
	// StoredKey 讀取資料庫加密存放的 API key（PUT /api/settings/services，SEC-R4）；每次呼叫時才讀，存完即生效。可以是 nil。
	StoredKey func(ctx context.Context, id string) (string, bool, error)
	// Clock 給 Retry-After 的日期格式與限流用。
	Clock clock.Clock
	// Limiter 是程序內共用的限流。
	Limiter *Limiter
	// NewClient 讓單元測試替換 provider 的 client；nil 時用 HTTPClient。
	NewClient func(cfg ProviderConfig, key string) Client

	// shared 是第一次 Client 時由 HTTP 建立的 LLM 用 client（NewHTTP）：所有 provider、所有 chunk 共用一個連線池。
	httpOnce sync.Once
	shared   *http.Client
	// sharedGemini 同 shared，但等待回應標頭的逾時是 GeminiRequestTimeout（GEM-R4）。
	sharedGemini *http.Client
	// clients 依 provider 快取 HTTPClient；API key 改變時換新的。
	mu      sync.Mutex
	clients map[string]*HTTPClient
}

// NewRegistry 建立 Registry；configured 是設定檔的 provider（可以是 nil）。
func NewRegistry(configured map[string]ProviderConfig, clk clock.Clock) *Registry {
	if clk == nil {
		clk = clock.Real{}
	}
	all := Builtin()
	for id, c := range configured {
		c.ID = id
		if c.BaseURL == "" {
			if b, ok := all[id]; ok && b.Type == c.Type {
				c.BaseURL = b.BaseURL
			} else if c.Type == TypeAnthropic {
				c.BaseURL = DefaultAnthropicBaseURL
			} else if c.Type == TypeGemini {
				c.BaseURL = DefaultGeminiBaseURL
			} else {
				c.BaseURL = DefaultOpenAIBaseURL
			}
		}
		all[id] = c
	}
	return &Registry{providers: all, Clock: clk, Limiter: NewLimiter(clk)}
}

// HasLLMProvider 回報 provider 是否存在（pipeline.ProviderCatalog，PRF-R4）。
func (r *Registry) HasLLMProvider(id string) bool {
	_, ok := r.providers[id]
	return ok
}

// LLMProviderType 回傳 provider 的 type（pipeline.ProviderTypes，GEM-R1）。
func (r *Registry) LLMProviderType(id string) (string, bool) {
	c, ok := r.providers[id]
	return c.Type, ok
}

// IDs 依字母順序列出 provider id。
func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Provider 回傳 provider 的設定。
func (r *Registry) Provider(id string) (ProviderConfig, bool) {
	c, ok := r.providers[id]
	return c, ok
}

// Client 回傳 provider 的 client。每次呼叫都重新讀取 API key：環境變數優先，其次是資料庫（存完即生效、免重啟）。
// provider 不存在時是 permanent；沒有 API key 時是 needs_action，動作「設定 API key」（LLM-R12）。
func (r *Registry) Client(ctx context.Context, id string) (Client, error) {
	cfg, ok := r.providers[id]
	if !ok {
		return nil, &errclass.Error{
			Class: errclass.ClassPermanent, Source: "llm", Detail: "找不到 LLM provider " + id,
			Message: fmt.Sprintf("找不到 LLM provider「%s」。", id),
		}
	}
	key, err := r.apiKey(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.NewClient != nil {
		return r.NewClient(cfg, key), nil
	}
	return r.httpClient(cfg, key), nil
}

// apiKey 讀取 provider 的 API key：環境變數優先，其次是資料庫。沒有 key 時是 needs_action，動作「設定 API key」（LLM-R12）。
func (r *Registry) apiKey(ctx context.Context, id string) (string, error) {
	key := strings.TrimSpace(r.Env[KeyEnvVar(id)])
	if key == "" && r.StoredKey != nil {
		k, ok, err := r.StoredKey(ctx, id)
		if err != nil {
			return "", err
		}
		if ok {
			key = strings.TrimSpace(k)
		}
	}
	if key == "" {
		return "", &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + id, Detail: "沒有 API key（" + KeyEnvVar(id) + "）",
			Message: fmt.Sprintf("LLM provider %s 沒有設定 API key。", id), Actions: []string{ActionSetAPIKey},
			Err: errclass.ErrNotConfigured,
		}
	}
	return key, nil
}

// PingRequest 組出測試連線用的請求（設定頁的「測試連線」，SEC-R11）：列出模型的 GET 請求，
// 帶認證但不產生費用（anthropic：GET /v1/models?limit=1；openai 相容：GET <base>/models；gemini：GET /v1beta/models?pageSize=1）。
// provider 不存在時是 permanent，沒有 API key 時與 Client 相同是 needs_action。請求還沒送出；呼叫端負責逾時與 netguard。
func (r *Registry) PingRequest(ctx context.Context, id string) (*http.Request, error) {
	cfg, ok := r.providers[id]
	if !ok {
		return nil, &errclass.Error{
			Class: errclass.ClassPermanent, Source: "llm", Detail: "找不到 LLM provider " + id,
			Message: fmt.Sprintf("找不到 LLM provider「%s」。", id),
		}
	}
	key, err := r.apiKey(ctx, id)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	var url string
	switch cfg.Type {
	case TypeAnthropic:
		url = base + "/v1/models?limit=1"
	case TypeGemini:
		url = base + "/v1beta/models?pageSize=1"
	default:
		url = base + "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "llm:" + id, Detail: "組出請求失敗", Err: err}
	}
	req.Header.Set("Accept", "application/json")
	switch cfg.Type {
	case TypeAnthropic:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", AnthropicVersion)
	case TypeGemini:
		req.Header.Set(GeminiKeyHeader, key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

// httpClient 回傳 provider 的 HTTPClient：設定與 key 都沒變時重用，連線池一律共用 shared。
func (r *Registry) httpClient(cfg ProviderConfig, key string) *HTTPClient {
	r.httpOnce.Do(func() {
		r.shared = NewHTTP(r.HTTP)
		r.sharedGemini = newHTTPWithTimeout(r.HTTP, GeminiRequestTimeout)
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[cfg.ID]; ok && c.APIKey == key {
		return c
	}
	if r.clients == nil {
		r.clients = map[string]*HTTPClient{}
	}
	hc := r.shared
	if cfg.Type == TypeGemini {
		hc = r.sharedGemini
	}
	c := &HTTPClient{ID: cfg.ID, Type: cfg.Type, BaseURL: cfg.BaseURL, APIKey: key, HTTP: hc, Now: r.Clock.Now}
	r.clients[cfg.ID] = c
	return c
}
