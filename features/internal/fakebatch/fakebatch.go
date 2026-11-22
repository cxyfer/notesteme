// Package fakebatch 是驗收測試用的假 Batch API（BATCH，M4）。
//
// 模擬兩家 provider 的 batch 端點（ADR-0019 的主機與驗證方式）：
//
//	Anthropic Message Batches（api.anthropic.com）
//	  POST /v1/messages/batches                 建立：{requests: [{custom_id, params}]}
//	  GET  /v1/messages/batches/{id}            查詢：processing_status、request_counts、results_url
//	  GET  /v1/messages/batches/{id}/results    取結果（JSONL）：{custom_id, result: {type, message|error}}
//	  POST /v1/messages/batches/{id}/cancel     取消
//
//	OpenAI Batch API（api.openai.com）
//	  POST /v1/files                            上傳 JSONL（multipart，purpose=batch）
//	  POST /v1/batches                          建立：{input_file_id, endpoint, completion_window}
//	  GET  /v1/batches/{id}                     查詢：status、output_file_id、error_file_id、request_counts
//	  POST /v1/batches/{id}/cancel              取消
//	  GET  /v1/files/{id}/content               取檔案內容（輸入、輸出、錯誤檔）
//
// batch 在建立後經過 CompleteAfter（以注入的 clock 判斷）才完成；Never 讓它永遠不完成。
// 每個請求的回答由 Answerer 產生：harness 接 fakellm（共用 steps.md 4.6 的 answers），
// 單元測試用 FixtureAnswerer 直接讀 features/fixtures/llm/ 的 {"output": …}。
//
// 這個套件只提供 handler 與狀態，登記到 fakenet 由 features/steps 負責（BATCH 的實作切片）；
// 和 fakellm 共用主機，依 Owns 判斷路徑屬於哪一個。
package fakebatch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// 已知的 provider。
const (
	Anthropic = "anthropic"
	OpenAI    = "openai"
)

// maxBody 是建立 batch 與上傳檔案的本體上限（測試用，足夠容納場景中的請求）。
const maxBody = 32 << 20

// Answerer 回答 batch 中的一個請求。provider 是 Anthropic 或 OpenAI，body 是請求本體
// （Anthropic 的 params、OpenAI JSONL 一列的 body，與一般 API 的請求相同）。
// 回傳 HTTP 狀態碼與回應本體（一般 API 的格式）；狀態碼不是 200 時，這個請求的結果視為失敗。
type Answerer interface {
	Answer(provider string, body []byte) (status int, resp []byte)
}

// Server 是假 Batch API 的狀態；可以同時被多個 goroutine 使用。
type Server struct {
	mu        sync.Mutex
	clk       clock.Clock
	answerer  Answerer
	providers map[string]*provider
	seq       int
}

type provider struct {
	completeAfter time.Duration
	never         bool
	key           string       // 非空時要求相同的 API key
	failChunks    map[int]bool // chunk 編號（從 1 起算）→ 這個請求的結果是失敗
	resultsURL    string       // 只用於 Anthropic：覆寫 results_url
	batches       map[string]*batch
	order         []string
	files         map[string][]byte // 只用於 OpenAI
	cancelled     int
}

type request struct {
	customID string
	body     []byte
	chunk    int
}

type batch struct {
	id              string
	createdAt       time.Time
	requests        []request
	endedAt         time.Time // 零值代表還沒結束
	cancelled       bool      // 在完成之前被取消
	cancelRequested bool
	resolved        bool
	results         []result
	// OpenAI 專用
	inputFileID, outputFileID, errorFileID, endpoint string
}

// result 是一個請求的結果；kind 是 succeeded、errored 或 canceled。
type result struct {
	customID string
	kind     string
	status   int
	body     []byte
}

// New 建立 Server。clk 為 nil 時用真實時間；answerer 為 nil 時每個請求都回 500。
// 兩個 provider 預設都在建立後 10 分鐘完成。
func New(clk clock.Clock, answerer Answerer) *Server {
	if clk == nil {
		clk = clock.Real{}
	}
	s := &Server{clk: clk, answerer: answerer, providers: map[string]*provider{}}
	for _, id := range []string{Anthropic, OpenAI} {
		s.providers[id] = &provider{
			completeAfter: 10 * time.Minute,
			failChunks:    map[int]bool{},
			batches:       map[string]*batch{},
			files:         map[string][]byte{},
		}
	}
	return s
}

func (s *Server) provider(id string) *provider {
	p, ok := s.providers[id]
	if !ok {
		panic(fmt.Sprintf("fakebatch：不認識的 provider %q", id))
	}
	return p
}

// CompleteAfter 讓這個 provider 之後的 batch 在建立後經過 d 完成（取代先前的 CompleteAfter 或 Never）。
func (s *Server) CompleteAfter(id string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.provider(id)
	p.completeAfter, p.never = d, false
}

// Never 讓這個 provider 的 batch 永遠不完成；被取消時才結束。
func (s *Server) Never(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider(id).never = true
}

// FailChunk 讓這個 provider 的 batch 中，chunk 編號（從 1 起算，同 fakellm 的 for chunk）相符的請求結果是失敗。
func (s *Server) FailChunk(id string, chunk int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider(id).failChunks[chunk] = true
}

// PointResultsTo 讓 Anthropic 的 results_url 指向 url（驗證產品不會連到其他主機，BATCH-R11）。
func (s *Server) PointResultsTo(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider(Anthropic).resultsURL = url
}

// RequireKey 要求這個 provider 的請求帶指定的 API key；沒有呼叫時只要求有帶 key。
func (s *Server) RequireKey(id, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider(id).key = key
}

// Batches 回傳這個 provider 建立過的 batch 數。
func (s *Server) Batches(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.provider(id).order)
}

// Requests 回傳這個 provider 所有 batch 中的請求總數。
func (s *Server) Requests(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.provider(id)
	n := 0
	for _, bid := range p.order {
		n += len(p.batches[bid].requests)
	}
	return n
}

// Cancelled 回傳這個 provider 收到取消請求的 batch 數（同一個 batch 取消多次只算一次）。
func (s *Server) Cancelled(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provider(id).cancelled
}

// Owns 回報 path 是否屬於 batch 端點（其他路徑由 fakellm 處理）。
func Owns(id, path string) bool {
	switch id {
	case Anthropic:
		return path == "/v1/messages/batches" || strings.HasPrefix(path, "/v1/messages/batches/")
	case OpenAI:
		return path == "/v1/batches" || strings.HasPrefix(path, "/v1/batches/") ||
			path == "/v1/files" || strings.HasPrefix(path, "/v1/files/")
	}
	return false
}

// Handler 回傳 provider 的 http.Handler，給 fakenet 依 Owns 分派使用。
func (s *Server) Handler(id string) http.Handler {
	s.provider(id) // 不認識的 provider 在登記時就 panic
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(id, r) {
			writeJSON(w, http.StatusUnauthorized, errorBody(id, "authentication_error", "invalid x-api-key"))
			return
		}
		if id == Anthropic {
			s.serveAnthropic(w, r)
		} else {
			s.serveOpenAI(w, r)
		}
	})
}

func (s *Server) authorized(id string, r *http.Request) bool {
	s.mu.Lock()
	want := s.provider(id).key
	s.mu.Unlock()
	got := r.Header.Get("x-api-key")
	if id == OpenAI {
		got, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if want != "" {
		return got == want
	}
	return got != ""
}

// state 在持有鎖時判斷 batch 是否已經結束，結束時把時間記下來。
func (s *Server) state(p *provider, b *batch) (ended bool) {
	if !b.endedAt.IsZero() {
		return true
	}
	if p.never {
		return false
	}
	if done := b.createdAt.Add(p.completeAfter); !s.clk.Now().Before(done) {
		b.endedAt = done
		return true
	}
	return false
}

// cancel 在持有鎖時處理取消：還沒完成時結束 batch、所有請求為 canceled；已經完成時不改變任何東西。
func (s *Server) cancel(p *provider, b *batch) {
	if !b.cancelRequested {
		b.cancelRequested = true
		p.cancelled++
	}
	if s.state(p, b) {
		return
	}
	b.cancelled = true
	b.endedAt = s.clk.Now()
}

// resolve 在持有鎖時產生結果（只產生一次）；呼叫 Answerer 時不持有鎖。
func (s *Server) resolve(id string, b *batch) []result {
	if b.resolved {
		return b.results
	}
	p := s.provider(id)
	reqs, fail, cancelled := slices.Clone(b.requests), cloneSet(p.failChunks), b.cancelled
	answerer := s.answerer
	s.mu.Unlock()
	out := make([]result, 0, len(reqs))
	for _, rq := range reqs {
		switch {
		case cancelled:
			out = append(out, result{customID: rq.customID, kind: "canceled"})
		case fail[rq.chunk]:
			out = append(out, result{customID: rq.customID, kind: "errored", status: http.StatusInternalServerError})
		case answerer == nil:
			out = append(out, result{customID: rq.customID, kind: "errored", status: http.StatusInternalServerError})
		default:
			status, body := answerer.Answer(id, rq.body)
			kind := "succeeded"
			if status != http.StatusOK {
				kind = "errored"
			}
			out = append(out, result{customID: rq.customID, kind: kind, status: status, body: body})
		}
	}
	s.mu.Lock()
	if !b.resolved {
		b.results, b.resolved = out, true
	}
	return b.results
}

func cloneSet(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Server) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s%04d", prefix, s.seq)
}

// ---- Anthropic ----

func (s *Server) serveAnthropic(w http.ResponseWriter, r *http.Request) {
	rest, _ := strings.CutPrefix(r.URL.Path, "/v1/messages/batches")
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.Split(rest, "/")
	switch {
	case rest == "" && r.Method == http.MethodPost:
		s.anthropicCreate(w, r)
	case len(parts) == 1 && rest != "" && r.Method == http.MethodGet:
		s.anthropicGet(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "results" && r.Method == http.MethodGet:
		s.anthropicResults(w, parts[0])
	case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
		s.mu.Lock()
		p := s.provider(Anthropic)
		b, ok := p.batches[parts[0]]
		if ok {
			s.cancel(p, b)
		}
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, errorBody(Anthropic, "not_found_error", "batch not found"))
			return
		}
		s.anthropicGet(w, r, parts[0])
	default:
		writeJSON(w, http.StatusNotFound, errorBody(Anthropic, "not_found_error", "not found"))
	}
}

func (s *Server) anthropicCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Requests []struct {
			CustomID string          `json:"custom_id"`
			Params   json.RawMessage `json:"params"`
		} `json:"requests"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil || len(in.Requests) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody(Anthropic, "invalid_request_error", "requests: at least one request is required"))
		return
	}
	b := &batch{createdAt: s.clk.Now()}
	seen := map[string]bool{}
	for _, rq := range in.Requests {
		if rq.CustomID == "" || seen[rq.CustomID] || len(rq.Params) == 0 {
			writeJSON(w, http.StatusBadRequest, errorBody(Anthropic, "invalid_request_error", "custom_id must be unique and params required"))
			return
		}
		seen[rq.CustomID] = true
		b.requests = append(b.requests, request{customID: rq.CustomID, body: rq.Params, chunk: chunkOf(rq.Params)})
	}
	s.mu.Lock()
	p := s.provider(Anthropic)
	b.id = s.nextID("msgbatch_fake_")
	p.batches[b.id] = b
	p.order = append(p.order, b.id)
	s.mu.Unlock()
	s.anthropicGet(w, r, b.id)
}

func (s *Server) anthropicGet(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	p := s.provider(Anthropic)
	b, ok := p.batches[id]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, errorBody(Anthropic, "not_found_error", "batch not found"))
		return
	}
	ended := s.state(p, b)
	counts := map[string]int{"processing": 0, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0}
	status := "in_progress"
	var resultsURL, endedAt any
	if ended {
		status = "ended"
		for _, res := range s.resolve(Anthropic, b) {
			counts[res.kind]++
		}
		endedAt = b.endedAt.UTC().Format(time.RFC3339)
		resultsURL = p.resultsURL
		if resultsURL == "" {
			resultsURL = scheme(r) + "://" + r.Host + "/v1/messages/batches/" + id + "/results"
		}
	} else {
		counts["processing"] = len(b.requests)
		if b.cancelRequested {
			status = "canceling"
		}
	}
	v := map[string]any{
		"id": id, "type": "message_batch", "processing_status": status, "request_counts": counts,
		"created_at": b.createdAt.UTC().Format(time.RFC3339), "ended_at": endedAt,
		"expires_at":  b.createdAt.Add(24 * time.Hour).UTC().Format(time.RFC3339),
		"results_url": resultsURL, "archived_at": nil, "cancel_initiated_at": nil,
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) anthropicResults(w http.ResponseWriter, id string) {
	s.mu.Lock()
	p := s.provider(Anthropic)
	b, ok := p.batches[id]
	if !ok || !s.state(p, b) {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, errorBody(Anthropic, "not_found_error", "results not available yet"))
		return
	}
	results := s.resolve(Anthropic, b)
	s.mu.Unlock()
	var buf bytes.Buffer
	for _, res := range results {
		line := map[string]any{"custom_id": res.customID}
		switch res.kind {
		case "succeeded":
			line["result"] = map[string]any{"type": "succeeded", "message": json.RawMessage(res.body)}
		case "errored":
			line["result"] = map[string]any{"type": "errored", "error": errorBody(Anthropic, "api_error", "Internal server error")}
		default:
			line["result"] = map[string]any{"type": res.kind}
		}
		raw, _ := json.Marshal(line)
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	w.Header().Set("Content-Type", "application/binary")
	_, _ = w.Write(buf.Bytes())
}

// ---- OpenAI ----

func (s *Server) serveOpenAI(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case p == "/v1/files" && r.Method == http.MethodPost:
		s.openaiUpload(w, r)
	case strings.HasPrefix(p, "/v1/files/") && strings.HasSuffix(p, "/content") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/files/"), "/content")
		s.mu.Lock()
		content, ok := s.provider(OpenAI).files[id]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, errorBody(OpenAI, "invalid_request_error", "No such File object: "+id))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(content)
	case p == "/v1/batches" && r.Method == http.MethodPost:
		s.openaiCreate(w, r)
	case strings.HasPrefix(p, "/v1/batches/") && strings.HasSuffix(p, "/cancel") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/batches/"), "/cancel")
		s.mu.Lock()
		pr := s.provider(OpenAI)
		b, ok := pr.batches[id]
		if ok {
			s.cancel(pr, b)
		}
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, errorBody(OpenAI, "invalid_request_error", "No such Batch: "+id))
			return
		}
		s.openaiGet(w, id)
	case strings.HasPrefix(p, "/v1/batches/") && r.Method == http.MethodGet:
		s.openaiGet(w, strings.TrimPrefix(p, "/v1/batches/"))
	default:
		writeJSON(w, http.StatusNotFound, errorBody(OpenAI, "invalid_request_error", "Unknown request URL"))
	}
}

func (s *Server) openaiUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(maxBody); err != nil || r.FormValue("purpose") != "batch" {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "purpose must be batch and file is required"))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "file is required"))
		return
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "cannot read file"))
		return
	}
	s.mu.Lock()
	id := s.nextID("file-fake")
	s.provider(OpenAI).files[id] = content
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "object": "file", "bytes": len(content), "created_at": s.clk.Now().Unix(),
		"filename": hdr.Filename, "purpose": "batch",
	})
}

func (s *Server) openaiCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InputFileID      string `json:"input_file_id"`
		Endpoint         string `json:"endpoint"`
		CompletionWindow string `json:"completion_window"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil ||
		in.Endpoint != "/v1/chat/completions" || in.CompletionWindow != "24h" {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "endpoint must be /v1/chat/completions and completion_window 24h"))
		return
	}
	s.mu.Lock()
	content, ok := s.provider(OpenAI).files[in.InputFileID]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "No such File object: "+in.InputFileID))
		return
	}
	b := &batch{createdAt: s.clk.Now(), inputFileID: in.InputFileID, endpoint: in.Endpoint}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 0, 64<<10), maxBody)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rq struct {
			CustomID string          `json:"custom_id"`
			Method   string          `json:"method"`
			URL      string          `json:"url"`
			Body     json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(line, &rq); err != nil || rq.CustomID == "" || seen[rq.CustomID] ||
			rq.Method != http.MethodPost || rq.URL != in.Endpoint || len(rq.Body) == 0 {
			writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "invalid batch input line"))
			return
		}
		seen[rq.CustomID] = true
		b.requests = append(b.requests, request{customID: rq.CustomID, body: rq.Body, chunk: chunkOf(rq.Body)})
	}
	if len(b.requests) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody(OpenAI, "invalid_request_error", "input file is empty"))
		return
	}
	s.mu.Lock()
	p := s.provider(OpenAI)
	b.id = s.nextID("batch_fake_")
	p.batches[b.id] = b
	p.order = append(p.order, b.id)
	s.mu.Unlock()
	s.openaiGet(w, b.id)
}

func (s *Server) openaiGet(w http.ResponseWriter, id string) {
	s.mu.Lock()
	p := s.provider(OpenAI)
	b, ok := p.batches[id]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, errorBody(OpenAI, "invalid_request_error", "No such Batch: "+id))
		return
	}
	status := "in_progress"
	completed, failed := 0, 0
	var outID, errID, completedAt, cancelledAt any
	switch {
	case !s.state(p, b):
		if b.cancelRequested {
			status = "cancelling"
		}
	case b.cancelled:
		status, cancelledAt = "cancelled", b.endedAt.Unix()
	default:
		status, completedAt = "completed", b.endedAt.Unix()
		s.openaiFiles(p, b)
		for _, res := range b.results {
			if res.kind == "succeeded" {
				completed++
			} else {
				failed++
			}
		}
		outID, errID = nullable(b.outputFileID), nullable(b.errorFileID)
	}
	v := map[string]any{
		"id": id, "object": "batch", "endpoint": b.endpoint, "input_file_id": b.inputFileID,
		"completion_window": "24h", "status": status, "output_file_id": outID, "error_file_id": errID,
		"created_at": b.createdAt.Unix(), "completed_at": completedAt, "cancelled_at": cancelledAt,
		"expires_at":     b.createdAt.Add(24 * time.Hour).Unix(),
		"request_counts": map[string]int{"total": len(b.requests), "completed": completed, "failed": failed},
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

// openaiFiles 在持有鎖時產生輸出檔與錯誤檔（只產生一次）；沒有內容的檔案不建立（ID 為空）。
func (s *Server) openaiFiles(p *provider, b *batch) {
	if b.resolved && (b.outputFileID != "" || b.errorFileID != "") {
		return
	}
	results := s.resolve(OpenAI, b)
	var out, errs bytes.Buffer
	for i, res := range results {
		reqID := fmt.Sprintf("batch_req_fake_%d", i+1)
		var line map[string]any
		if res.kind == "succeeded" {
			line = map[string]any{"id": reqID, "custom_id": res.customID, "error": nil,
				"response": map[string]any{"status_code": 200, "request_id": reqID, "body": json.RawMessage(res.body)}}
			raw, _ := json.Marshal(line)
			out.Write(raw)
			out.WriteByte('\n')
			continue
		}
		line = map[string]any{"id": reqID, "custom_id": res.customID, "response": nil,
			"error": map[string]any{"code": "server_error", "message": "The server had an error processing your request."}}
		raw, _ := json.Marshal(line)
		errs.Write(raw)
		errs.WriteByte('\n')
	}
	if out.Len() > 0 && b.outputFileID == "" {
		b.outputFileID = s.nextID("file-fake")
		p.files[b.outputFileID] = out.Bytes()
	}
	if errs.Len() > 0 && b.errorFileID == "" {
		b.errorFileID = s.nextID("file-fake")
		p.files[b.errorFileID] = errs.Bytes()
	}
}

// ---- 共用 ----

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// errorBody 是 provider 原生的錯誤格式。
func errorBody(id, typ, msg string) map[string]any {
	if id == Anthropic {
		return map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}}
	}
	return map[string]any{"error": map[string]any{"type": typ, "message": msg, "code": nil}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// chunkRe 找出 <transcript_data> 中的 chunk 編號（fakellm 的 GeneratorInput.Chunk）。
var chunkRe = regexp.MustCompile(`"chunk"\s*:\s*(\d+)`)

// chunkOf 從請求本體中所有字串內容（解碼後）找出 chunk 編號；找不到時為 0。
func chunkOf(body []byte) int {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return 0
	}
	var sb strings.Builder
	collectStrings(v, &sb)
	text := sb.String()
	if i := strings.Index(text, "<transcript_data>"); i >= 0 {
		text = text[i:]
	}
	if m := chunkRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func collectStrings(v any, sb *strings.Builder) {
	switch t := v.(type) {
	case string:
		sb.WriteString(t)
		sb.WriteByte('\n')
	case []any:
		for _, e := range t {
			collectStrings(e, sb)
		}
	case map[string]any:
		for _, e := range t {
			collectStrings(e, sb)
		}
	}
}
