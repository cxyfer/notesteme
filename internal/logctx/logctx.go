// Package logctx 把請求與任務的識別放進 context，並由 slog handler 自動帶進每一筆 log（NFR-OBS-1）：
//
//   - HTTP 請求：request_id（api 的 middleware 設定，回應標頭 X-Request-Id 是同一個值）。
//   - 任務的一次執行：request_id（worker 每次執行任務產生一個）、task、item、stage（pipeline 讀到步驟後補上）。
//
// 任務執行期間的 log 另外擷取最後幾行（Scope.Lines），失敗或 blocked 時隨步驟保存，
// 讓 UI 看得到「相關的 log 片段」（NFR-OBS-2、PIPE-R2）。
//
// log 呼叫必須用 *Context 版本（InfoContext、WarnContext…）並傳入這個 context，handler 才拿得到這些值。
package logctx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
)

// log 的欄位名稱。
const (
	KeyRequestID = "request_id"
	KeyTask      = "task"
	KeyItem      = "item"
	KeyStage     = "stage"
)

// 擷取的上限（NFR-OBS-2）：最多保留最後 MaxLines 行，每行最多 MaxLineBytes bytes（以 rune 為界截斷）。
const (
	MaxLines     = 20
	MaxLineBytes = 500
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyScope
)

// NewID 產生一個新的 request ID：16 個十六進位字元。
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // Go 1.24 起 crypto/rand.Read 不會回傳錯誤
	return hex.EncodeToString(b[:])
}

// ValidID 回報外部帶來的 request ID（X-Request-Id）能不能沿用：1～64 個字元，只有英數字與 . _ -。
// 不合格的值（太長、有空白或控制字元）不沿用，避免把任意內容寫進 log。
func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// HeaderRequestID 是 request ID 的請求與回應標頭。
const HeaderRequestID = "X-Request-Id"

// Middleware 決定請求的 request ID 並放進 context（之後的 log 自動帶上），回應標頭帶同一個值。
// 請求帶著合法的 X-Request-Id（見 ValidID）時沿用，否則產生新的。要放在所有 middleware 的最外層，
// 驗證失敗等錯誤回應才也帶得到它。Client（internal/api）與 Hub（internal/hub/hubapi）共用。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !ValidID(id) {
			id = NewID()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// WithRequestID 把 request ID 放進 context。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// RequestID 取出 context 中的 request ID；沒有時是空字串。
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(keyRequestID).(string)
	return v
}

// Scope 是一次任務執行的 log 範圍：task、item、stage 與擷取的 log 行。可以在多個 goroutine 中使用。
type Scope struct {
	mu                sync.Mutex
	task, item, stage string
	lines             []string
}

// WithTask 建立任務的 log 範圍並放進 context；item 與 stage 之後用 SetItemStage 補上。
func WithTask(ctx context.Context, taskID string) (context.Context, *Scope) {
	s := &Scope{task: taskID}
	return context.WithValue(ctx, keyScope, s), s
}

// ScopeFrom 取出 context 中的任務範圍；不在任務中時是 nil。
func ScopeFrom(ctx context.Context) *Scope {
	s, _ := ctx.Value(keyScope).(*Scope)
	return s
}

// SetItemStage 補上影音與步驟；s 為 nil 時什麼都不做。
func (s *Scope) SetItemStage(item, stage string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.item, s.stage = item, stage
	s.mu.Unlock()
}

func (s *Scope) fields() (task, item, stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.task, s.item, s.stage
}

func (s *Scope) add(line string) {
	line = Truncate(strings.TrimRight(line, "\n"), MaxLineBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	if len(s.lines) > MaxLines {
		s.lines = append(s.lines[:0:0], s.lines[len(s.lines)-MaxLines:]...)
	}
}

// Lines 回傳擷取到的最後幾行（複本，由舊到新）；s 為 nil 時是 nil。
func (s *Scope) Lines() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

// Truncate 把 s 截到最多 max bytes，不切斷 UTF-8 字元；有截斷時結尾加上「…」（計入上限）。
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	cut := max - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// Handler 包住另一個 slog.Handler：從 context 補上 request_id、task、item、stage（record 或 With 已有同名欄位時不重複），
// 並在任務範圍中擷取 log 行。
type Handler struct {
	next slog.Handler
	// pre 是 WithAttrs 累積的欄位（擷取的行也要有）；keys 是其中最上層的欄位名稱（去重用）。
	pre    []slog.Attr
	keys   map[string]bool
	groups []string
}

// NewHandler 包住 next。
func NewHandler(next slog.Handler) *Handler { return &Handler{next: next} }

// Enabled 實作 slog.Handler。
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

// WithAttrs 實作 slog.Handler。
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)
	c.pre = append(append([]slog.Attr(nil), h.pre...), attrs...)
	if len(h.groups) == 0 {
		c.keys = make(map[string]bool, len(h.keys)+len(attrs))
		for k := range h.keys {
			c.keys[k] = true
		}
		for _, a := range attrs {
			c.keys[a.Key] = true
		}
	}
	return &c
}

// WithGroup 實作 slog.Handler。
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.next = h.next.WithGroup(name)
	c.groups = append(append([]string(nil), h.groups...), name)
	return &c
}

// Handle 實作 slog.Handler。
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	reqID := RequestID(ctx)
	scope := ScopeFrom(ctx)
	if reqID != "" || scope != nil {
		have := make(map[string]bool, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			have[a.Key] = true
			return true
		})
		r = r.Clone()
		add := func(k, v string) {
			if v != "" && !have[k] && !h.keys[k] {
				r.AddAttrs(slog.String(k, v))
			}
		}
		add(KeyRequestID, reqID)
		if scope != nil {
			task, item, stage := scope.fields()
			add(KeyTask, task)
			add(KeyItem, item)
			add(KeyStage, stage)
			scope.add(h.format(r))
		}
	}
	return h.next.Handle(ctx, r)
}

// format 把 record 寫成一行文字（slog 的 text 格式），不含識別欄位（片段本來就屬於這個任務）。
func (h *Handler) format(r slog.Record) string {
	var buf bytes.Buffer
	var th slog.Handler = slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 {
				switch a.Key {
				case KeyRequestID, KeyTask, KeyItem, KeyStage:
					return slog.Attr{}
				}
			}
			return a
		},
	})
	if len(h.pre) > 0 {
		th = th.WithAttrs(h.pre)
	}
	for _, g := range h.groups {
		th = th.WithGroup(g)
	}
	_ = th.Handle(context.Background(), r)
	return buf.String()
}
