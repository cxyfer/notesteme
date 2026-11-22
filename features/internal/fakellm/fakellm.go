// Package fakellm 是驗收測試用的腳本化 LLM provider（ADR-0015、docs/specs/steps.md 4.6）。
//
// Server 同時模擬 anthropic（POST /v1/messages）與 openai（POST /v1/chat/completions）兩種 wire format，
// handler 以 fakenet.Handle 掛在正式主機名稱上，被測程式照正常路徑連線。
//
// wire 契約（S0 凍結，S3 寫成 ADR-0019；internal/llm 的 client 遵守它，要改的話 fakellm 一起改）：
//   - 步驟名稱放在強制的 tool 或 schema 名稱 notesteme_<stage>。
//   - 字幕資料放在 user message 的 <transcript_data> 區塊，內容是一行 JSON {chunk, chunks, context[], cues[]}。
//   - 不符合契約的請求回 400，並記一筆違規（Violations），由 harness 的 After hook 讓場景失敗。
//
// 所有請求（包含 401、4xx、5xx 的失敗請求）都會計數並保留在 Requests。
package fakellm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// provider 的主機名稱與測試用的 API key。
const (
	AnthropicHost = "api.anthropic.com"
	OpenAIHost    = "api.openai.com"

	TestAnthropicKey = "sk-ant-notesteme-fixture"
	TestOpenAIKey    = "sk-notesteme-fixture"
)

const (
	// AnthropicVersion 是 anthropic 請求必須帶的 anthropic-version 標頭。
	AnthropicVersion = "2023-06-01"
	// ToolPrefix 是強制的 tool 或 schema 名稱的前綴：notesteme_<stage>。
	ToolPrefix = "notesteme_"
	// DataOpen 與 DataClose 是字幕資料區塊的標記。
	DataOpen  = "<transcript_data>"
	DataClose = "</transcript_data>"

	// retryAfterSeconds 是 429 回應的 retry-after。
	retryAfterSeconds = "30"
	// maxBody 只是防止讀取失控的保險。
	maxBody = 32 << 20
)

// Provider ID。
const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
)

// IsProvider 回報 id 是不是 fakellm 模擬的 provider（anthropic 或 openai）。
func IsProvider(id string) bool { return id == providerAnthropic || id == providerOpenAI }

// Request 是收到的一個請求，失敗的請求也算（LLM-R6 期待 429 那一次也被計數）。
type Request struct {
	Seq    int    // 這個 provider 的第幾個請求，從 1 起算
	Stage  string // 取自 notesteme_<stage>；取不到時為空，並記一筆違規
	Chunk  int    // 取自 <transcript_data> 的 chunk；沒有時為 0
	Model  string
	Text   string // 解碼後的 system 與各 message 的文字依序串接，再接上「不跳脫 HTML 的 compact body」：contains 斷言比對這裡，所以 JSON 欄位名稱（例如 cache_control）也比對得到
	Prefix string // Text 中資料區塊（找不到時為第一個 "<transcript_data>"）之前的部分；兩者都沒有時為空
	Status int
	At     time.Time
}

// Server 是一個場景的腳本化 LLM provider。零值不可用，用 New 建立。並行安全。
type Server struct {
	fixturesDir string
	clk         clock.Clock

	mu         sync.Mutex
	providers  map[string]*provider
	violations []string
}

// provider 是單一 provider 的腳本與請求紀錄。
type provider struct {
	id   string
	path string // 唯一接受的請求路徑
	key  string // 非空時，請求必須帶這把 key

	seq      int
	fail     *failSpec
	answers  map[string]*sequence        // stage → 依序回答
	chunks   map[string]map[int]*fixture // stage → chunk → 回答
	requests []Request
}

type failSpec struct {
	status    int
	remaining int // < 0 代表一直失敗
}

// sequence 是 Answer 的依序回答：用完之後一直回傳最後一個。
type sequence struct {
	fixtures []*fixture
	next     int
}

// New 建立 Server。fixturesDir 是 features/fixtures 的絕對路徑（fixture 讀自其下的 llm/）；clk 為 nil 時用真實時間。
func New(fixturesDir string, clk clock.Clock) *Server {
	if clk == nil {
		clk = clock.Real{}
	}
	s := &Server{fixturesDir: fixturesDir, clk: clk, providers: map[string]*provider{}}
	for id, path := range map[string]string{providerAnthropic: "/v1/messages", providerOpenAI: "/v1/chat/completions"} {
		s.providers[id] = &provider{
			id: id, path: path,
			answers: map[string]*sequence{},
			chunks:  map[string]map[int]*fixture{},
		}
	}
	return s
}

// Handler 回傳 provider（"anthropic" 或 "openai"）的 http.Handler，給 fakenet.Handle 使用。
// 不是已知的 provider 時，每個請求都回 500 並記一筆違規。
func (s *Server) Handler(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		res := s.handle(id, r, body)
		w.Header().Set("Content-Type", "application/json")
		for k, v := range res.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(res.status)
		_, _ = w.Write(res.body)
	})
}

// RequireKey 設定請求必須帶的 key；空字串代表不檢查（RemoveAPIKey 用：產品沒檢查 key 就呼叫時，會拿到正常的回答，場景因此失敗）。
// anthropic 檢查 x-api-key，openai 檢查 Authorization: Bearer。不是已知的 provider 時不動作。
func (s *Server) RequireKey(id, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.providers[id]; p != nil {
		p.key = key
	}
}

// Answer 讓 stage 的請求依序回傳 fixtures；用完之後一直回傳最後一個（steps.md 4.6）。
// 重新呼叫會取代之前的設定並從第一個重新開始。fixture 不存在或格式不正確時回傳錯誤，且不改變任何設定。
func (s *Server) Answer(id, stage string, fixtures ...string) error {
	if err := s.checkTarget(id, stage); err != nil {
		return err
	}
	if len(fixtures) == 0 {
		return fmt.Errorf("fakellm: Answer(%s, %s) 至少要有一個 fixture", id, stage)
	}
	loaded := make([]*fixture, 0, len(fixtures))
	for _, name := range fixtures {
		f, err := s.loadFixture(name)
		if err != nil {
			return err
		}
		loaded = append(loaded, f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[id].answers[stage] = &sequence{fixtures: loaded}
	return nil
}

// AnswerChunk 只針對 chunk（從 1 起算）；優先於 Answer。
func (s *Server) AnswerChunk(id, stage string, chunk int, name string) error {
	if err := s.checkTarget(id, stage); err != nil {
		return err
	}
	if chunk < 1 {
		return fmt.Errorf("fakellm: chunk 從 1 起算，收到 %d", chunk)
	}
	f, err := s.loadFixture(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.providers[id]
	if p.chunks[stage] == nil {
		p.chunks[stage] = map[int]*fixture{}
	}
	p.chunks[stage][chunk] = f
	return nil
}

// Fail 讓這個 provider 的請求回傳 HTTP status；times <= 0 代表一直失敗，1 代表只有下一個請求失敗。優先於所有 Answer。
// 失敗的請求不會用掉 Answer 的依序回答。不是已知的 provider 時不動作。
func (s *Server) Fail(id string, status, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.providers[id]
	if p == nil {
		return
	}
	if times <= 0 {
		times = -1
	}
	p.fail = &failSpec{status: status, remaining: times}
}

// Requests 回傳 provider 收到的全部請求（依抵達順序，複本）。
func (s *Server) Requests(id string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.providers[id]
	if p == nil {
		return nil
	}
	return append([]Request(nil), p.requests...)
}

// Violations 回傳違反 wire 契約或沒有腳本的請求紀錄（複本）；非空時場景必須失敗。
func (s *Server) Violations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.violations...)
}

func (s *Server) checkTarget(id, stage string) error {
	if !IsProvider(id) {
		return fmt.Errorf("fakellm 不認識 LLM provider %q（只有 anthropic、openai）", id)
	}
	if stage == "" {
		return fmt.Errorf("fakellm: stage 不可為空")
	}
	return nil
}

// result 是一個請求的回應。
type result struct {
	status int
	header map[string]string
	body   []byte
}

// violate 記一筆違規（呼叫時須持有 s.mu）。
func (s *Server) violate(id string, seq int, format string, args ...any) {
	s.violations = append(s.violations, fmt.Sprintf("%s 請求 %d：%s", id, seq, fmt.Sprintf(format, args...)))
}

// handle 決定一個請求的回應：路徑與方法 → 解析 → 401 → wire 契約 → Fail → 腳本。
// 每個請求（不論結果）都記進 Requests。
func (s *Server) handle(id string, r *http.Request, body []byte) result {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.providers[id]
	if p == nil {
		s.violate(id, 0, "不認識的 provider")
		return result{status: http.StatusInternalServerError, body: []byte(`{"error":"fakellm: unknown provider"}`)}
	}
	p.seq++
	req := Request{Seq: p.seq, At: s.clk.Now()}
	finish := func(res result) result {
		req.Status = res.status
		p.requests = append(p.requests, req)
		return res
	}

	if r.Method != http.MethodPost || r.URL.Path != p.path {
		s.violate(id, req.Seq, "非預期的端點 %s %s（只接受 POST %s）", r.Method, r.URL.Path, p.path)
		return finish(p.errorResult(http.StatusNotFound, ""))
	}

	pr := parseRequest(id, body)
	req.Stage, req.Chunk, req.Model, req.Text, req.Prefix = pr.stage, pr.chunk, pr.model, pr.text, pr.prefix

	if p.key != "" && !p.authorized(r, p.key) {
		return finish(p.errorResult(http.StatusUnauthorized, req.Model))
	}
	wireErrs := pr.errs
	if id == providerAnthropic && r.Header.Get("anthropic-version") != AnthropicVersion {
		wireErrs = append(wireErrs, fmt.Sprintf("anthropic-version 標頭是 %q，預期 %q", r.Header.Get("anthropic-version"), AnthropicVersion))
	}
	if len(wireErrs) > 0 {
		s.violate(id, req.Seq, "違反 wire 契約：%s", strings.Join(wireErrs, "；"))
		return finish(p.errorResult(http.StatusBadRequest, req.Model))
	}

	if f := p.fail; f != nil {
		if f.remaining > 0 {
			f.remaining--
			if f.remaining == 0 {
				p.fail = nil
			}
		}
		return finish(p.errorResult(f.status, req.Model))
	}

	fx := p.pick(req.Stage, req.Chunk)
	if fx == nil {
		s.violate(id, req.Seq, "stage %q（chunk %d）沒有腳本：場景缺少對應的 `answers` 步驟", req.Stage, req.Chunk)
		return finish(p.errorResult(http.StatusInternalServerError, req.Model))
	}
	gin := generatorInput(req.Stage, pr.data)
	gin.Prefix = pr.prefix
	out, err := fx.render(gin)
	if err != nil {
		s.violate(id, req.Seq, "fixture %q 無法產生回答：%v", fx.name, err)
		return finish(p.errorResult(http.StatusBadRequest, req.Model))
	}
	return finish(p.successResult(req, out))
}

// pick 選出這個請求的回答：chunk 腳本優先於依序回答；依序回答只在被選中時前進。沒有腳本時回傳 nil。
func (p *provider) pick(stage string, chunk int) *fixture {
	if chunk > 0 {
		if f := p.chunks[stage][chunk]; f != nil {
			return f
		}
	}
	seq := p.answers[stage]
	if seq == nil {
		return nil
	}
	f := seq.fixtures[min(seq.next, len(seq.fixtures)-1)]
	seq.next++
	return f
}

// authorized 檢查憑證。
func (p *provider) authorized(r *http.Request, key string) bool {
	if p.id == providerAnthropic {
		return r.Header.Get("x-api-key") == key
	}
	return r.Header.Get("Authorization") == "Bearer "+key
}

// estimateTokens 是粗略的 token 估算（約 4 個位元組一個），只為了讓 usage 有確定的數字。
func estimateTokens(n int) int { return (n + 3) / 4 }

// successResult 依 provider 的 wire format 組出成功的回應。
func (p *provider) successResult(req Request, out answer) result {
	inTok, outTok := estimateTokens(len(req.Text)), estimateTokens(len(out.raw()))
	var v any
	if p.id == providerAnthropic {
		block := map[string]any{"type": "text", "text": out.text}
		stop := "end_turn"
		if !out.isText {
			block = map[string]any{
				"type": "tool_use", "id": fmt.Sprintf("toolu_fake_%d", req.Seq),
				"name": ToolPrefix + req.Stage, "input": out.output,
			}
			stop = "tool_use"
		}
		v = map[string]any{
			"id": fmt.Sprintf("msg_fake_%d", req.Seq), "type": "message", "role": "assistant",
			"model": req.Model, "content": []any{block}, "stop_reason": stop, "stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens": inTok, "output_tokens": outTok,
				"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			},
		}
	} else {
		v = map[string]any{
			"id": fmt.Sprintf("chatcmpl-fake-%d", req.Seq), "object": "chat.completion", "model": req.Model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": out.raw()},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": inTok, "completion_tokens": outTok, "total_tokens": inTok + outTok},
		}
	}
	b, _ := json.Marshal(v)
	return result{status: http.StatusOK, body: b}
}

// errorResult 依 provider 的原生錯誤格式組出錯誤回應；429 帶 retry-after。
func (p *provider) errorResult(status int, model string) result {
	res := result{status: status}
	var typ, code, msg string
	switch {
	case status == http.StatusUnauthorized:
		typ, code, msg = "authentication_error", "invalid_api_key", "invalid api key"
	case status == http.StatusNotFound:
		typ, code, msg = "not_found_error", "model_not_found", "model: "+model
	case status == http.StatusTooManyRequests:
		typ, code, msg = "rate_limit_error", "rate_limit_exceeded", "rate limit exceeded"
		res.header = map[string]string{"retry-after": retryAfterSeconds}
	case status >= 500:
		typ, code, msg = "api_error", "server_error", "internal server error"
	case status == http.StatusBadRequest:
		typ, code, msg = "invalid_request_error", "invalid_request", "invalid request"
	default:
		typ, code, msg = "api_error", "error", http.StatusText(status)
	}
	var v any
	if p.id == providerAnthropic {
		v = map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}}
	} else {
		// openai 的 type 與 code 分開：401、404 的 type 是 invalid_request_error，code 才是細項。
		if status == http.StatusUnauthorized || status == http.StatusNotFound {
			typ = "invalid_request_error"
		}
		if status >= 500 {
			typ = "server_error"
		}
		v = map[string]any{"error": map[string]any{"message": msg, "type": typ, "param": nil, "code": code}}
	}
	res.body, _ = json.Marshal(v)
	return res
}

// dataRE 找出 <transcript_data> 區塊：標記之間是一行 JSON 物件。
var dataRE = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(DataOpen) + `\s*(\{.*?\})\s*` + regexp.QuoteMeta(DataClose))

// dataBlock 是 <transcript_data> 內的 JSON。cue 保留原樣（i 以 json.Number 保持數字）。
// dataBlock 是 <transcript_data> 中的資料：chunk 編號、總數、重疊的前文與這一段的 cue。
// cues 的每一項只包含 i（從 1 起算的句號）與 text（契約明定，見 LLM-execution.md）；
// 多出的欄位記為 wire 違規，避免產品依賴沒有約定的欄位。
type dataBlock struct {
	Chunk   int              `json:"chunk"`
	Chunks  int              `json:"chunks"`
	Context []map[string]any `json:"context"`
	Cues    []map[string]any `json:"cues"`
}

// validate 檢查資料區塊的欄位（LLM-execution.md）：chunk 與 chunks 從 1 起算且 chunk 不超過 chunks（沒寫就是 0，同樣違規）；
// cues[] 與 context[] 的每一項只能有 i 與 text，i 必須是正整數。
func (d *dataBlock) validate() []string {
	var errs []string
	if d.Chunks < 1 {
		errs = append(errs, fmt.Sprintf("chunks 必須是從 1 起算的整數（得到 %d）", d.Chunks))
	}
	if d.Chunk < 1 {
		errs = append(errs, fmt.Sprintf("chunk 必須是從 1 起算的整數（得到 %d）", d.Chunk))
	} else if d.Chunks >= 1 && d.Chunk > d.Chunks {
		errs = append(errs, fmt.Sprintf("chunk %d 超過 chunks %d", d.Chunk, d.Chunks))
	}
	// 契約要求 context[] 與 cues[] 都是陣列（沒有前文時是 []）：缺少或 null 解碼後都是 nil slice，
	// 不檢查的話漏掉字幕的 client 仍可以通過；非陣列的值在解碼時就失敗，視為沒有可解析的資料區塊。
	if d.Context == nil {
		errs = append(errs, "context 必須是陣列（缺少或是 null；沒有前文時要送 []）")
	}
	if d.Cues == nil {
		errs = append(errs, "cues 必須是陣列（缺少或是 null）")
	}
	errs = append(errs, validateEntries("context", d.Context)...)
	return append(errs, validateEntries("cues", d.Cues)...)
}

// validateEntries 檢查一組 {i, text} 項目：只能有這兩個欄位，i 是正整數，text 是字串；name 是它在資料區塊中的欄位名（context 或 cues）。
func validateEntries(name string, entries []map[string]any) []string {
	var errs []string
	for j, c := range entries {
		for k := range c {
			if k != "i" && k != "text" {
				errs = append(errs, fmt.Sprintf("%s[%d] 有契約以外的欄位 %q（只能有 i 與 text）", name, j, k))
			}
		}
		if _, ok := c["text"].(string); !ok {
			errs = append(errs, fmt.Sprintf("%s[%d].text 必須是字串", name, j))
		}
		switch v := c["i"].(type) {
		case json.Number:
			if n, err := v.Int64(); err != nil || n < 1 {
				errs = append(errs, fmt.Sprintf("%s[%d].i 必須是從 1 起算的整數", name, j))
			}
		default:
			errs = append(errs, fmt.Sprintf("%s[%d].i 必須是從 1 起算的整數", name, j))
		}
	}
	return errs
}

// parsed 是請求解析的結果；errs 是違反 wire 契約之處（空代表沒有）。
type parsed struct {
	stage, model, text, prefix string
	chunk                      int
	data                       *dataBlock
	errs                       []string
}

type part struct{ role, text string }

// checkStrictSchema 檢查 OpenAI strict 模式要求的 schema 形狀：最上層是 type object、
// additionalProperties 為 false，而且 required 列出所有 properties 的 key。不符時回傳違規說明。
func checkStrictSchema(raw json.RawMessage) []string {
	var s struct {
		Type                 string         `json:"type"`
		Properties           map[string]any `json:"properties"`
		Required             []string       `json:"required"`
		AdditionalProperties *bool          `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return []string{"json_schema.schema 不是合法的 JSON：" + err.Error()}
	}
	var errs []string
	if s.Type != "object" {
		errs = append(errs, `json_schema.schema.type 必須是 "object"（strict 模式的限制）`)
	}
	if s.AdditionalProperties == nil || *s.AdditionalProperties {
		errs = append(errs, "json_schema.schema.additionalProperties 必須是 false（strict 模式的限制）")
	}
	req := map[string]bool{}
	for _, k := range s.Required {
		req[k] = true
	}
	for k := range s.Properties {
		if !req[k] {
			errs = append(errs, fmt.Sprintf("json_schema.schema.required 必須列出所有屬性（缺少 %q，strict 模式的限制）", k))
		}
	}
	return errs
}

// parseRequest 依 provider 的 wire format 解析請求；解析失敗的部分記在 errs，其餘欄位盡量填入。
func parseRequest(id string, body []byte) parsed {
	var pr parsed
	var parts []part
	var raw struct {
		Model     string          `json:"model"`
		MaxTokens int             `json:"max_tokens"`
		System    json.RawMessage `json:"system"`
		Messages  []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		ToolChoice *struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_choice"`
		ResponseFormat *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		pr.text = string(body)
		pr.errs = append(pr.errs, "body 不是合法的 JSON："+err.Error())
		return pr
	}
	pr.model = raw.Model
	// 兩家 API 的 model 都是必填欄位，缺了或是空字串真實 API 會回 400。
	if strings.TrimSpace(raw.Model) == "" {
		pr.errs = append(pr.errs, "缺少 model（兩家 API 的必填欄位，不可以是空字串）")
	}
	if id == providerAnthropic {
		if t := contentText(raw.System); t != "" {
			parts = append(parts, part{"system", t})
		}
	}
	for _, m := range raw.Messages {
		parts = append(parts, part{m.Role, contentText(m.Content)})
	}

	// 步驟名稱：強制的 tool 或 schema 名稱 notesteme_<stage>。
	var name string
	switch id {
	case providerAnthropic:
		// Anthropic 的 max_tokens 是必填欄位，缺了真實 API 會回 400。
		if raw.MaxTokens <= 0 {
			pr.errs = append(pr.errs, "缺少 max_tokens（Anthropic 的必填欄位，必須大於 0）")
		}
		switch {
		case raw.ToolChoice == nil || raw.ToolChoice.Type != "tool":
			pr.errs = append(pr.errs, "缺少強制的 tool_choice（type 必須是 tool）")
		default:
			name = raw.ToolChoice.Name
			found := false
			for _, t := range raw.Tools {
				found = found || t.Name == name
			}
			if !found {
				pr.errs = append(pr.errs, fmt.Sprintf("tool_choice 的 tool %q 不在 tools 之中", name))
			}
		}
	default:
		switch {
		case raw.ResponseFormat == nil || raw.ResponseFormat.Type != "json_schema" || raw.ResponseFormat.JSONSchema == nil:
			pr.errs = append(pr.errs, "缺少 response_format（type 必須是 json_schema）")
		default:
			js := raw.ResponseFormat.JSONSchema
			name = js.Name
			if !js.Strict {
				pr.errs = append(pr.errs, "json_schema.strict 必須是 true")
			}
			if len(bytes.TrimSpace(js.Schema)) == 0 || string(bytes.TrimSpace(js.Schema)) == "null" {
				pr.errs = append(pr.errs, "json_schema.schema 不可為空")
			} else if js.Strict {
				pr.errs = append(pr.errs, checkStrictSchema(js.Schema)...)
			}
		}
	}
	if name != "" {
		if stage, ok := strings.CutPrefix(name, ToolPrefix); ok && stage != "" {
			pr.stage = stage
		} else {
			pr.errs = append(pr.errs, fmt.Sprintf("未知的 tool 名稱 %q（預期 %s<stage>）", name, ToolPrefix))
		}
	}

	// Text：解碼後的各段文字，再接上不跳脫 HTML 的 compact body。
	var sb strings.Builder
	offsets := make([]int, len(parts))
	for i, p := range parts {
		if i > 0 {
			sb.WriteByte('\n')
		}
		offsets[i] = sb.Len()
		sb.WriteString(p.text)
	}
	if len(parts) > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(compactBody(body))
	pr.text = sb.String()

	// 資料區塊只在 user message 中找；系統指示若提到標記的名稱，不算資料區塊。
	dataAt := -1
	for i, p := range parts {
		if p.role != "user" {
			continue
		}
		for _, m := range dataRE.FindAllStringSubmatchIndex(p.text, -1) {
			var blk dataBlock
			dec := json.NewDecoder(strings.NewReader(p.text[m[2]:m[3]]))
			dec.UseNumber()
			if dec.Decode(&blk) != nil {
				continue
			}
			pr.data, pr.chunk, dataAt = &blk, blk.Chunk, offsets[i]+m[0]
			pr.errs = append(pr.errs, blk.validate()...)
			break
		}
		if pr.data != nil {
			break
		}
	}
	if pr.data == nil {
		// 契約要求每個請求都在 user message 中帶一個可以解析的資料區塊：沒有、或解析不了都是違規，
		// 不能讓漏掉字幕的 client 靠靜態 fixture 通過場景。
		pr.errs = append(pr.errs, "user message 中沒有可以解析的 "+DataOpen+" 資料區塊（內容要是一行 JSON 物件 {chunk, chunks, context, cues}）")
	}
	if dataAt < 0 {
		dataAt = strings.Index(pr.text, DataOpen)
	}
	if dataAt >= 0 {
		pr.prefix = pr.text[:dataAt]
	}
	return pr
}

// compactBody 把請求 body 解碼後重新編碼：compact、不跳脫 HTML、中文與符號維持原字。無法解碼時回傳原文。
func compactBody(body []byte) string {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(body)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return string(body)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// contentText 取出 message 或 system 的文字：字串，或 text block 的陣列（以換行串接）。
func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}
