// Package providers 是設定頁「服務與金鑰」的 provider 目錄與「測試連線」（SEC-R11、pages.md §12）。
//
// 目錄列出內建與設定檔定義的 provider：LLM（內建 anthropic、openai、gemini 與 llm.providers）、
// 語音轉錄（內建 groq 與 asr.providers，含 command）、字幕服務（內建 supadata）。
// 回傳的資訊只有識別資訊與停用公告，不含 API key、command 的參數與環境變數。
//
// 測試連線送一個帶認證、不產生費用的請求（列出模型或帳號資訊），走呼叫端提供的 netguard client、
// 不跟隨轉址、有逾時；command provider 只檢查指令存在且可以執行，不實際執行（SEC 待決問題）。
package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// provider 的種類。
const (
	KindLLM        = "llm"
	KindASR        = "asr"
	KindTranscript = "transcript"
)

// provider 的定義來源。
const (
	DefinedInBuiltin    = "builtin"
	DefinedInConfigFile = "config_file"
)

// 內建的字幕來源與語音轉錄 provider id。
const (
	IDGroq     = "groq"
	IDSupadata = "supadata"
)

// TestTimeout 是一次測試連線的逾時。
const TestTimeout = 15 * time.Second

// maxDrain 是測試請求的回應最多讀取（並丟棄）的大小。
const maxDrain = 64 << 10

// 測試連線的動作。
const (
	ActionSetAPIKey   = llm.ActionSetAPIKey
	ActionEditConfig  = "修改設定檔"
	ActionCheckConfig = "檢查設定檔的 base_url"
)

// Deprecation 是一個模型的停用公告。
type Deprecation struct {
	// Model 是模型 ID。
	Model string `json:"model"`
	// After 是停止服務的日期（YYYY-MM-DD，provider 公告的日期）。
	After string `json:"after"`
	// Replacement 是 provider 建議的替代模型；沒有時省略。
	Replacement string `json:"replacement,omitempty"`
	// Message 是補充說明；沒有時省略。
	Message string `json:"message,omitempty"`
}

// Deprecations 是內建的模型停用公告，以 provider id 為 key。
//
// 只放能對照 provider 官方公告確認的資料，並在註解記下來源與查證日期；不要依記憶或第三方文章填入日期。
// 2026-11-25 查證：程式內建的模型只有 Groq 的 whisper-large-v3-turbo，不在官方停用清單，所以是空的（SEC 待決問題）。
var Deprecations = map[string][]Deprecation{}

// Info 是目錄中的一個 provider。
type Info struct {
	ID        string
	Type      string
	Kind      string
	DefinedIn string
	// Testable 為 false 時不提供測試連線，原因在 UntestableReason。
	Testable         bool
	UntestableReason string
	Deprecations     []Deprecation
}

// Result 是成功的測試連線。
type Result struct {
	Latency time.Duration
	Message string
}

var (
	// ErrNotFound 表示目錄中沒有這個 provider（或指定的種類中沒有）。
	ErrNotFound = errors.New("找不到指定的 provider。")
	// ErrNotTestable 表示這個 provider 不提供測試連線（Info.UntestableReason）。
	ErrNotTestable = errors.New("這個 provider 不提供測試連線。")
)

// LLM 是 LLM provider 的來源（*llm.Registry）。
type LLM interface {
	IDs() []string
	Provider(id string) (llm.ProviderConfig, bool)
	PingRequest(ctx context.Context, id string) (*http.Request, error)
}

// Pinger 組出內建字幕來源的測試請求（*groq.Client、*supadata.Client）。
type Pinger interface {
	PingRequest(ctx context.Context) (*http.Request, error)
}

// Catalog 是 provider 目錄。零值可用（只列出 asr.providers 以外的什麼都沒有）。
type Catalog struct {
	// LLM 是 LLM provider；LLMConfigured 是其中來自設定檔 llm.providers 的 id。
	LLM           LLM
	LLMConfigured map[string]bool
	// ASR 是設定檔 asr.providers（SEC-R3：command 只能在這裡定義）。
	ASR map[string]config.ASRProvider
	// Groq、Supadata 是內建的來源；nil 時測試回 needs_action（沒有設定 API key）。
	Groq     Pinger
	Supadata Pinger
	// HTTP 是 netguard 的 client（ADR-0014）。nil 時 HTTP 類的測試是 permanent。
	HTTP *http.Client
	// Deprecations 是停用公告；nil 時用套件的 Deprecations。
	Deprecations map[string][]Deprecation
	// LookPath 找 command 的執行檔；nil 時用 exec.LookPath。
	LookPath func(string) (string, error)
	// Timeout 是測試連線的逾時；0 時用 TestTimeout。
	Timeout time.Duration
	// Now 量測延遲；nil 時用 time.Now。
	Now func() time.Time
}

// kindOrder 是同一個 id 出現在多個種類時，單一 id 的查詢順序：asr.providers 先（維持 GET /api/providers/{id} 原本的行為）、
// 其次 LLM、最後字幕服務（SEC 待決問題）。
var kindOrder = []string{KindASR, KindLLM, KindTranscript}

// List 依 id、再依種類（kindOrder）排序列出所有 provider。
func (c *Catalog) List() []Info {
	var out []Info
	for _, k := range kindOrder {
		out = append(out, c.listKind(k)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return slices.Index(kindOrder, out[i].Kind) < slices.Index(kindOrder, out[j].Kind)
	})
	return out
}

func (c *Catalog) listKind(kind string) []Info {
	var out []Info
	switch kind {
	case KindLLM:
		if c.LLM == nil {
			return nil
		}
		for _, id := range c.LLM.IDs() {
			p, _ := c.LLM.Provider(id)
			def := DefinedInBuiltin
			if c.LLMConfigured[id] {
				def = DefinedInConfigFile
			}
			out = append(out, c.info(Info{ID: id, Type: p.Type, Kind: KindLLM, DefinedIn: def, Testable: true}))
		}
	case KindASR:
		_, groqShadowed := c.ASR[IDGroq]
		if !groqShadowed {
			out = append(out, c.info(Info{ID: IDGroq, Type: IDGroq, Kind: KindASR, DefinedIn: DefinedInBuiltin, Testable: true}))
		}
		for id, p := range c.ASR {
			in := Info{ID: id, Type: p.Type, Kind: KindASR, DefinedIn: DefinedInConfigFile, Testable: true}
			if p.Type != config.ASRTypeCommand {
				// 設定檔的 openai-compatible、mistral 還沒有接上處理流程，也沒有對應的 client 與 API key 來源（SEC 待決問題）。
				in.Testable = false
				in.UntestableReason = "這種 provider 還沒有接上處理流程，無法測試連線。"
			}
			out = append(out, c.info(in))
		}
	case KindTranscript:
		out = append(out, c.info(Info{ID: IDSupadata, Type: IDSupadata, Kind: KindTranscript, DefinedIn: DefinedInBuiltin, Testable: true}))
	}
	return out
}

func (c *Catalog) info(in Info) Info {
	deps := c.Deprecations
	if deps == nil {
		deps = Deprecations
	}
	in.Deprecations = append([]Deprecation{}, deps[in.ID]...)
	return in
}

// Get 回傳 id 的 provider；kind 為空時依 kindOrder 取第一個。
func (c *Catalog) Get(id, kind string) (Info, bool) {
	kinds := kindOrder
	if kind != "" {
		kinds = []string{kind}
	}
	for _, k := range kinds {
		for _, in := range c.listKind(k) {
			if in.ID == id {
				return in, true
			}
		}
	}
	return Info{}, false
}

// Test 測試 provider 的連線。錯誤：
//   - ErrNotFound、ErrNotTestable。
//   - *errclass.Error：needs_action（沒有 API key、key 被拒絕、找不到 command 的指令）帶 Message 與 Actions；
//     retryable（provider 失敗、逾時、連線失敗、被 netguard 擋下）帶 Message。
//
// 訊息不含 API key、請求網址與 provider 回應的內容。
func (c *Catalog) Test(ctx context.Context, id, kind string) (Result, error) {
	in, ok := c.Get(id, kind)
	if !ok {
		return Result{}, ErrNotFound
	}
	if !in.Testable {
		return Result{}, ErrNotTestable
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = TestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := now()
	var err error
	switch {
	case in.Kind == KindASR && in.DefinedIn == DefinedInConfigFile:
		err = c.testCommand(id, c.ASR[id])
		if err == nil {
			return Result{Latency: now().Sub(start), Message: "找到指令且可以執行；沒有實際執行。"}, nil
		}
		return Result{}, err
	case in.Kind == KindLLM:
		var req *http.Request
		req, err = c.LLM.PingRequest(ctx, id)
		if err == nil {
			err = c.do(ctx, id, timeout, req)
		}
	case in.ID == IDGroq:
		err = c.ping(ctx, id, timeout, c.Groq, "Groq")
	case in.ID == IDSupadata:
		err = c.ping(ctx, id, timeout, c.Supadata, "Supadata")
	default:
		return Result{}, ErrNotTestable
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Latency: now().Sub(start), Message: "連線成功。"}, nil
}

func (c *Catalog) ping(ctx context.Context, id string, timeout time.Duration, p Pinger, name string) error {
	if p == nil {
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: id, Err: errclass.ErrNotConfigured,
			Message: fmt.Sprintf("%s 沒有設定 API key。", name), Actions: []string{ActionSetAPIKey},
		}
	}
	req, err := p.PingRequest(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, id, timeout, req)
}

// do 送出測試請求並依狀態碼分類。不跟隨轉址（標頭帶有 API key），回應只讀取有限的大小並丟棄。
func (c *Catalog) do(ctx context.Context, id string, timeout time.Duration, req *http.Request) error {
	if c.HTTP == nil {
		return &errclass.Error{Class: errclass.ClassPermanent, Source: id, Message: "沒有可用的對外連線。"}
	}
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req.WithContext(ctx))
	if err != nil {
		e := &errclass.Error{Class: errclass.ClassRetryable, Source: id, Err: err}
		switch {
		case errors.Is(err, netguard.ErrBlockedAddress) || errors.Is(err, netguard.ErrUnsupportedScheme):
			e.Message = fmt.Sprintf("連到 provider %s 的請求被對外連線的保護擋下：目的位址不在允許的範圍內。", id)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			e.Message = fmt.Sprintf("provider %s 在 %d 秒內沒有回應。", id, int(math.Ceil(timeout.Seconds())))
		default:
			e.Message = fmt.Sprintf("無法連線到 provider %s。", id)
		}
		return e
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: id, Detail: fmt.Sprintf("HTTP %d", code),
			Message: fmt.Sprintf("provider %s 拒絕了 API key（HTTP %d）。", id, code), Actions: []string{ActionSetAPIKey},
		}
	case code == http.StatusTooManyRequests || code >= 500:
		return &errclass.Error{
			Class: errclass.ClassRetryable, Source: id, Detail: fmt.Sprintf("HTTP %d", code),
			Message: fmt.Sprintf("provider %s 暫時無法使用（HTTP %d），請稍後再試。", id, code),
		}
	default:
		return &errclass.Error{
			Class: errclass.ClassRetryable, Source: id, Detail: fmt.Sprintf("HTTP %d", code),
			Message: fmt.Sprintf("provider %s 拒絕了測試請求（HTTP %d），請確認設定檔的 base_url。", id, code),
		}
	}
}

// testCommand 只檢查 command 的第一個元素是存在且可以執行的檔案，不執行它（SEC 待決問題）。
// 訊息不含指令與路徑：設定檔的內容可能有敏感資訊。
func (c *Catalog) testCommand(id string, p config.ASRProvider) error {
	notFound := &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: id, Err: errclass.ErrNotConfigured,
		Message: fmt.Sprintf("找不到 command provider %s 的指令，或沒有執行權限。", id), Actions: []string{ActionEditConfig},
	}
	if len(p.Command) == 0 || strings.TrimSpace(p.Command[0]) == "" || strings.Contains(p.Command[0], "{") {
		return notFound
	}
	bin := p.Command[0]
	if strings.ContainsRune(bin, os.PathSeparator) || strings.Contains(bin, "/") {
		st, err := os.Stat(bin)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
			return notFound
		}
		return nil
	}
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if _, err := look(bin); err != nil {
		return notFound
	}
	return nil
}
