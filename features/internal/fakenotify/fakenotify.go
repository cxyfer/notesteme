// Package fakenotify 是驗收測試用的假通知服務（NOTIFY，M4）。
//
// 一個 http.Handler 同時扮演四種通知端點的接收端，依主機與路徑判斷種類：
//
//   - ntfy：`POST <server>/`，JSON 發布（{topic, title, message, tags, priority, click}），回 200 {"id": …}；
//   - Discord webhook：`POST /api/webhooks/<id>/<token>`，回 204；
//   - Telegram Bot API：`POST /bot<token>/sendMessage`，回 200 {"ok": true, "result": {…}}；
//   - 一般 webhook：其他路徑，回 204。
//
// 每個請求都記錄 method、host、path、query、標頭、原始 body。端點以「主機（不分大小寫）＋路徑」識別，忽略 query。
// 可以讓某個端點「接下來 N 次」或「一律」回指定狀態碼；429 時加上 Retry-After。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 由 features/steps 負責（M4 的實作切片）。
package fakenotify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Hosts 是預設登記的主機（ntfy、Discord、Telegram 的官方主機，以及一般 webhook 用的測試主機）。
var Hosts = []string{"ntfy.sh", "discord.com", "api.telegram.org", "hooks.example.com"}

// MaxBody 是單一請求 body 的記錄上限；超過時回 413，模擬真實服務的大小限制。
const MaxBody = 1 << 20

// RetryAfterSeconds 是回 429 時 Retry-After 標頭的值。
const RetryAfterSeconds = 120

// Request 是收到的一個請求的紀錄。
type Request struct {
	Method string
	Host   string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// Endpoint 回傳這個請求所屬的端點鍵（小寫主機＋路徑）。
func (r Request) Endpoint() string { return endpointKey(r.Host, r.Path) }

// Text 回傳請求所有可見的文字：路徑、query、標頭、原始 body，以及 body 為 JSON 時解碼後的所有字串值。
// 用來斷言「通知中不含某段文字」（secrets、字幕全文）。
func (r Request) Text() string {
	var b strings.Builder
	b.WriteString(r.Path)
	b.WriteString("\n")
	b.WriteString(r.Query)
	b.WriteString("\n")
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString(k + ": " + strings.Join(r.Header[k], ",") + "\n")
	}
	b.Write(r.Body)
	b.WriteString("\n")
	var v any
	if json.Unmarshal(r.Body, &v) == nil {
		collectStrings(v, &b)
	}
	return b.String()
}

func collectStrings(v any, b *strings.Builder) {
	switch x := v.(type) {
	case string:
		b.WriteString(x)
		b.WriteString("\n")
	case []any:
		for _, e := range x {
			collectStrings(e, b)
		}
	case map[string]any:
		for k, e := range x {
			b.WriteString(k)
			b.WriteString("\n")
			collectStrings(e, b)
		}
	}
}

type failure struct {
	status    int
	remaining int // <0 代表一律
}

// Server 是假通知服務的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	mu       sync.Mutex
	fail     map[string]*failure // 端點鍵 → 失敗設定
	requests []Request
}

// New 建立一律回預設成功回應的假通知服務。
func New() *Server {
	return &Server{fail: map[string]*failure{}}
}

// EndpointOf 把網址（`https://host/path?query`）轉成端點鍵；網址無法解析時回傳空字串。
func EndpointOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return endpointKey(u.Host, u.Path)
}

func endpointKey(host, path string) string {
	h := strings.ToLower(host)
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i] // 去掉 port，httptest 的位址帶 port
	}
	if path == "" {
		path = "/"
	}
	return h + path
}

// FailNext 讓這個端點接下來 n 個請求回應 status，之後恢復預設回應。
func (s *Server) FailNext(rawURL string, status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.fail[EndpointOf(rawURL)]; ok && old.remaining < 0 {
		return // 「一律」優先於「接下來 N 次」
	}
	s.fail[EndpointOf(rawURL)] = &failure{status: status, remaining: n}
}

// FailAlways 讓這個端點的所有請求都回應 status。
func (s *Server) FailAlways(rawURL string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[EndpointOf(rawURL)] = &failure{status: status, remaining: -1}
}

// Requests 回傳收到的全部請求（依抵達順序）。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// RequestsTo 回傳這個端點收到的請求（依抵達順序）。
func (s *Server) RequestsTo(rawURL string) []Request {
	key := EndpointOf(rawURL)
	var out []Request
	for _, r := range s.Requests() {
		if r.Endpoint() == key {
			out = append(out, r)
		}
	}
	return out
}

// Last 回傳這個端點最近一次收到的請求；沒有時 ok 為 false。
func (s *Server) Last(rawURL string) (Request, bool) {
	reqs := s.RequestsTo(rawURL)
	if len(reqs) == 0 {
		return Request{}, false
	}
	return reqs[len(reqs)-1], true
}

// Signature 計算 webhook 的簽章：`sha256=` 加上 hex(HMAC-SHA256(secret, timestamp + "." + body))。
func Signature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature 檢查請求的 X-Notesteme-Signature 是否以 secret 簽出，並回傳 X-Notesteme-Timestamp（Unix 秒）。
func VerifySignature(r Request, secret string) (timestamp int64, ok bool) {
	ts := r.Header.Get("X-Notesteme-Timestamp")
	sig := r.Header.Get("X-Notesteme-Signature")
	if ts == "" || sig == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, false
	}
	want := Signature(secret, ts, r.Body)
	return n, hmac.Equal([]byte(sig), []byte(want))
}

// ServeHTTP 實作 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	rec := Request{
		Method: r.Method,
		Host:   r.Host,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	}
	key := rec.Endpoint()

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	status := 0
	if f, ok := s.fail[key]; ok {
		status = f.status
		if f.remaining > 0 {
			f.remaining--
			if f.remaining == 0 {
				delete(s.fail, key)
			}
		}
	}
	s.mu.Unlock()

	switch {
	case len(body) > MaxBody:
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	case status != 0:
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
		return
	case r.Method != http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	host := strings.TrimSuffix(key[:strings.Index(key, "/")], ".")
	switch {
	case host == "ntfy.sh" || isNtfyPublish(rec):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fakentfy" + strconv.Itoa(len(s.Requests())), "event": "message"})
	case strings.HasPrefix(rec.Path, "/api/webhooks/"):
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(rec.Path, "/bot") && strings.HasSuffix(rec.Path, "/sendMessage"):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": len(s.Requests())}})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// isNtfyPublish 判斷自架 ntfy 的 JSON 發布：路徑是 `/`，body 是含 topic 的 JSON。
func isNtfyPublish(r Request) bool {
	if r.Path != "/" {
		return false
	}
	var v struct {
		Topic string `json:"topic"`
	}
	return json.Unmarshal(r.Body, &v) == nil && v.Topic != ""
}
