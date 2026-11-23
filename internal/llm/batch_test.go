package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// batchServer 是測試用的 provider：依路徑回應，記下收到的請求。
type batchServer struct {
	mu   sync.Mutex
	reqs []recorded
	// handle 依 method 與 path 回應；回傳 false 時是 404。
	handle func(w http.ResponseWriter, r *http.Request, body []byte) bool
}

type recorded struct {
	Method, Path, ContentType, Key, Version string
	Body                                    []byte
}

func newBatchTLS(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte) bool) (*batchServer, *httptest.Server) {
	t.Helper()
	bs := &batchServer{handle: h}
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("x-api-key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		bs.mu.Lock()
		bs.reqs = append(bs.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Content-Type"), key, r.Header.Get("anthropic-version"), body})
		bs.mu.Unlock()
		if !bs.handle(w, r, body) {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return bs, ts
}

func (b *batchServer) requests() []recorded {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]recorded(nil), b.reqs...)
}

func testBatchRequest(id string) BatchRequest {
	return BatchRequest{CustomID: id, Request: Request{
		Model: "m", MaxTokens: 100, Spec: CorrectSpec(), Fixed: Fixed{System: "sys", Prefix: "prefix"}, Data: DataOpen + `{"chunk":1}` + DataClose,
	}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// BATCH-R2：Anthropic 的每個請求帶 custom_id 與一般 API 相同的 params（含 cache_control）。
func TestBatchSubmitAnthropic(t *testing.T) {
	bs, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages/batches" {
			writeJSON(w, 200, map[string]any{"id": "msgbatch_01", "processing_status": "in_progress"})
			return true
		}
		return false
	})
	c := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL, APIKey: "k1", HTTP: ts.Client()}
	id, err := c.Submit(context.Background(), []BatchRequest{testBatchRequest("t1"), testBatchRequest("t2")})
	if err != nil || id != "msgbatch_01" {
		t.Fatalf("Submit = %q, %v", id, err)
	}
	got := bs.requests()
	if len(got) != 1 || got[0].Key != "k1" || got[0].Version != AnthropicVersion || got[0].ContentType != "application/json" {
		t.Fatalf("請求 = %+v", got)
	}
	var body struct {
		Requests []struct {
			CustomID string          `json:"custom_id"`
			Params   json.RawMessage `json:"params"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(got[0].Body, &body); err != nil || len(body.Requests) != 2 || body.Requests[1].CustomID != "t2" {
		t.Fatalf("body = %s", got[0].Body)
	}
	// 與一般 API 的 body 完全相同。
	_, want, _ := (&HTTPClient{Type: TypeAnthropic}).encode(testBatchRequest("x").Request)
	if !bytes.Equal(body.Requests[0].Params, want) || !bytes.Contains(want, []byte("cache_control")) {
		t.Errorf("params = %s\n預期 %s", body.Requests[0].Params, want)
	}
}

// BATCH-R2：OpenAI 先上傳 JSONL（purpose: batch），再以 endpoint /v1/chat/completions、completion_window 24h 建立。
func TestBatchSubmitOpenAI(t *testing.T) {
	var lines []map[string]any
	var create map[string]any
	_, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, body []byte) bool {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/files":
			req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
			if err := req.ParseMultipartForm(1 << 20); err != nil || req.FormValue("purpose") != "batch" {
				writeJSON(w, 400, map[string]any{"error": map[string]any{"type": "invalid_request_error"}})
				return true
			}
			f, _, _ := req.FormFile("file")
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				var m map[string]any
				_ = json.Unmarshal(sc.Bytes(), &m)
				lines = append(lines, m)
			}
			writeJSON(w, 200, map[string]any{"id": "file-abc"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batches":
			_ = json.Unmarshal(body, &create)
			writeJSON(w, 200, map[string]any{"id": "batch_xyz", "status": "validating"})
		default:
			return false
		}
		return true
	})
	c := &BatchClient{ID: "openai", Type: TypeOpenAI, BaseURL: ts.URL + "/v1", APIKey: "k2", HTTP: ts.Client()}
	id, err := c.Submit(context.Background(), []BatchRequest{testBatchRequest("t1")})
	if err != nil || id != "batch_xyz" {
		t.Fatalf("Submit = %q, %v", id, err)
	}
	if len(lines) != 1 || lines[0]["custom_id"] != "t1" || lines[0]["method"] != "POST" || lines[0]["url"] != "/v1/chat/completions" {
		t.Fatalf("JSONL = %v", lines)
	}
	if create["input_file_id"] != "file-abc" || create["endpoint"] != "/v1/chat/completions" || create["completion_window"] != "24h" {
		t.Fatalf("建立 = %v", create)
	}
}

// 送出失敗依一般 API 的分類（429、5xx 是 retryable）；provider 回傳不安全的 ID 時不接受。
func TestBatchSubmitErrors(t *testing.T) {
	status, id := 429, "msgbatch_01"
	_, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if status != 200 {
			writeJSON(w, status, map[string]any{"error": map[string]any{"type": "rate_limit_error"}})
			return true
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return true
	})
	c := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()}
	if _, err := c.Submit(context.Background(), []BatchRequest{testBatchRequest("t")}); errclassOf(err) != errclass.ClassRetryable {
		t.Errorf("429 = %v", err)
	}
	status = 500
	if _, err := c.Submit(context.Background(), []BatchRequest{testBatchRequest("t")}); errclassOf(err) != errclass.ClassRetryable {
		t.Errorf("500 = %v", err)
	}
	status = 200
	for _, bad := range []string{"", "../../admin", "a/b", "x?y=1", strings.Repeat("a", 129)} {
		id = bad
		if _, err := c.Submit(context.Background(), []BatchRequest{testBatchRequest("t")}); err == nil {
			t.Errorf("不安全的 ID %q 應該被拒絕", bad)
		}
	}
	if _, err := c.Submit(context.Background(), nil); err == nil {
		t.Error("沒有請求時應該失敗")
	}
	many := make([]BatchRequest, MaxBatchRequests+1)
	if _, err := c.Submit(context.Background(), many); err == nil {
		t.Error("超過 100 個請求時應該失敗")
	}
	if _, err := c.Submit(context.Background(), []BatchRequest{{CustomID: "t"}}); err == nil {
		t.Error("沒有 Spec 時應該失敗")
	}
	nohttp := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL}
	if _, err := nohttp.Submit(context.Background(), []BatchRequest{testBatchRequest("t")}); errclassOf(err) != errclass.ClassPermanent {
		t.Errorf("沒有 HTTP client = %v", err)
	}
}

func errclassOf(err error) errclass.Class {
	c, _ := errclass.ClassOf(err)
	return c
}

// BATCH-R4：狀態的對應；結束後 results_url 在同一個主機時逐行取回，其他主機時不連線（BATCH-R11）。
func TestBatchAnthropicStatusAndResults(t *testing.T) {
	var resultsURL string
	ended := false
	results := strings.Join([]string{
		`{"custom_id":"t1","result":{"type":"succeeded","message":{"content":[{"type":"tool_use","name":"notesteme_correct","input":{"cues":[]}}],"usage":{"input_tokens":5,"output_tokens":7}}}}`,
		`not json`,
		`{"custom_id":"","result":{"type":"succeeded"}}`,
		`{"custom_id":"t2","result":{"type":"errored","error":{"type":"api_error"}}}`,
		`{"custom_id":"t3","result":{"type":"expired"}}`,
		`{"custom_id":"t4","result":{"type":"canceled"}}`,
		`{"custom_id":"t5","result":{"type":"mystery"}}`,
		``,
	}, "\r\n")
	bs, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		switch {
		case r.URL.Path == "/v1/messages/batches/msgbatch_01" && r.Method == http.MethodGet,
			r.URL.Path == "/v1/messages/batches/msgbatch_01/cancel" && r.Method == http.MethodPost:
			v := map[string]any{"id": "msgbatch_01", "processing_status": "in_progress", "results_url": nil, "ended_at": nil}
			if ended {
				v["processing_status"], v["results_url"], v["ended_at"] = "ended", resultsURL, "2026-11-23T10:10:00Z"
			}
			writeJSON(w, 200, v)
		case r.URL.Path == "/v1/messages/batches/msgbatch_01/results":
			_, _ = io.WriteString(w, results)
		default:
			return false
		}
		return true
	})
	c := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()}
	ctx := context.Background()
	st, err := c.Status(ctx, "msgbatch_01")
	if err != nil || st.State != BatchInProgress || st.EndedAt != nil {
		t.Fatalf("處理中 = %+v, %v", st, err)
	}
	ended, resultsURL = true, ts.URL+"/v1/messages/batches/msgbatch_01/results"
	st, err = c.Cancel(ctx, "msgbatch_01")
	if err != nil || st.State != BatchEnded || st.EndedAt == nil || !st.EndedAt.Equal(time.Date(2026, 11, 23, 10, 10, 0, 0, time.UTC)) {
		t.Fatalf("結束 = %+v, %v", st, err)
	}
	var got []BatchResult
	if err := c.Results(ctx, st, func(r BatchResult) error { got = append(got, r); return nil }); err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, r := range got {
		kinds = append(kinds, r.CustomID+":"+r.Kind)
	}
	if strings.Join(kinds, ",") != "t1:succeeded,t2:errored,t3:expired,t4:canceled" {
		t.Fatalf("結果 = %v", kinds)
	}
	resp, err := c.DecodeBatchBody(got[0].Body, CorrectSpec())
	if err != nil || string(resp.Output) != `{"cues":[]}` || resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("解碼 = %+v, %v", resp, err)
	}
	if resp, err := c.DecodeBatchBody(got[0].Body, nil); err != nil || resp.Usage.OutputTokens != 7 || resp.Output != nil {
		t.Fatalf("只取用量 = %+v, %v", resp, err)
	}
	// fn 的錯誤原樣回傳。
	stop := errors.New("stop")
	if err := c.Results(ctx, st, func(BatchResult) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("fn 的錯誤 = %v", err)
	}

	before := len(bs.requests())
	for _, foreign := range []string{
		"https://results.evil.example/batch.jsonl",
		"http://" + strings.TrimPrefix(ts.URL, "https://") + "/v1/messages/batches/msgbatch_01/results", // 降級成 http
		"https://user:pw@" + strings.TrimPrefix(ts.URL, "https://") + "/x",
		"::not a url",
	} {
		st.resultsURL = foreign
		if err := c.Results(ctx, st, func(BatchResult) error { return nil }); !errors.Is(err, ErrForeignResults) {
			t.Errorf("%q = %v，預期 ErrForeignResults", foreign, err)
		}
	}
	if n := len(bs.requests()); n != before {
		t.Errorf("其他主機的結果網址不應該連線（多了 %d 個請求）", n-before)
	}
	st.resultsURL = ""
	if err := c.Results(ctx, st, func(BatchResult) error { t.Error("沒有結果網址時不應該有結果"); return nil }); err != nil {
		t.Error(err)
	}
	if _, err := c.Status(ctx, "../x"); err == nil {
		t.Error("不安全的 batch ID 應該被拒絕")
	}
	if _, err := c.Cancel(ctx, ""); err == nil {
		t.Error("空的 batch ID 應該被拒絕")
	}
}

// OpenAI 的狀態對應與輸出檔、錯誤檔（BATCH-R4）。
func TestBatchOpenAIStatusAndResults(t *testing.T) {
	status := "in_progress"
	_, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		switch r.URL.Path {
		case "/v1/batches/batch_1", "/v1/batches/batch_1/cancel":
			v := map[string]any{"id": "batch_1", "status": status, "output_file_id": nil, "error_file_id": nil}
			switch status {
			case "completed":
				v["completed_at"], v["output_file_id"], v["error_file_id"] = 1763892600, "file-out", "file-err"
			case "failed":
				v["failed_at"] = 1763892600
			case "expired":
				v["expired_at"], v["output_file_id"] = 1763892600, "file-out"
			case "cancelled":
				v["cancelled_at"] = 1763892600
			}
			writeJSON(w, 200, v)
		case "/v1/files/file-out/content":
			_, _ = io.WriteString(w, `{"custom_id":"t1","response":{"status_code":200,"body":{"choices":[{"message":{"content":"{\"cues\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}},"error":null}`+"\n"+
				`{"custom_id":"t2","response":{"status_code":500,"body":{}},"error":null}`+"\n")
		case "/v1/files/file-err/content":
			_, _ = io.WriteString(w, `{"custom_id":"t3","response":null,"error":{"code":"server_error"}}`+"\n"+`garbage`+"\n")
		default:
			return false
		}
		return true
	})
	c := &BatchClient{ID: "openai", Type: TypeOpenAI, BaseURL: ts.URL + "/v1", APIKey: "k", HTTP: ts.Client()}
	ctx := context.Background()
	for _, tc := range []struct{ status, state string }{
		{"validating", BatchInProgress}, {"in_progress", BatchInProgress}, {"finalizing", BatchInProgress}, {"cancelling", BatchInProgress},
		{"failed", BatchTerminated}, {"expired", BatchTerminated}, {"cancelled", BatchTerminated}, {"completed", BatchEnded},
	} {
		status = tc.status
		st, err := c.Status(ctx, "batch_1")
		if err != nil || st.State != tc.state || st.Provider != tc.status {
			t.Errorf("%s = %+v, %v", tc.status, st, err)
		}
		if tc.state != BatchInProgress && (st.EndedAt == nil || st.EndedAt.Unix() != 1763892600) {
			t.Errorf("%s 的結束時間 = %v", tc.status, st.EndedAt)
		}
	}
	st, err := c.Cancel(ctx, "batch_1")
	if err != nil || st.State != BatchEnded {
		t.Fatalf("Cancel = %+v, %v", st, err)
	}
	var got []string
	var body json.RawMessage
	if err := c.Results(ctx, st, func(r BatchResult) error {
		got = append(got, r.CustomID+":"+r.Kind)
		if r.Kind == ResultSucceeded {
			body = r.Body
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "t1:succeeded,t2:errored,t3:errored" {
		t.Fatalf("結果 = %v", got)
	}
	resp, err := c.DecodeBatchBody(body, CorrectSpec())
	if err != nil || string(resp.Output) != `{"cues":[]}` || resp.Usage.InputTokens != 3 {
		t.Fatalf("解碼 = %+v, %v", resp, err)
	}
	st.outputFileID = "../secret"
	if err := c.Results(ctx, st, func(BatchResult) error { return nil }); err == nil {
		t.Error("不安全的檔案 ID 應該被拒絕")
	}
}

// BATCH-R11：單一結果超過 10 MiB 時整個 batch 視為 batch_failed。
func TestBatchResultLineTooLarge(t *testing.T) {
	big := `{"custom_id":"t1","result":{"type":"succeeded","message":{"pad":"` + strings.Repeat("x", MaxBatchResultLine) + `"}}}` + "\n"
	_, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		_, _ = io.WriteString(w, big)
		return true
	})
	c := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()}
	st := BatchStatus{State: BatchEnded, resultsURL: ts.URL + "/results"}
	called := false
	err := c.Results(context.Background(), st, func(BatchResult) error { called = true; return nil })
	if !errors.Is(err, ErrBatchResultsTooLarge) || called {
		t.Fatalf("超過上限 = %v（called=%v）", err, called)
	}
	if _, err := c.DecodeBatchBody(make([]byte, MaxBatchResultLine+1), CorrectSpec()); err == nil {
		t.Error("超過上限的結果不應該解碼")
	}
}

// 結果檔的 HTTP 錯誤依一般 API 分類（例如 404 是 needs_action 的模型錯誤以外的分類），不會當成沒有結果。
func TestBatchResultsHTTPError(t *testing.T) {
	_, ts := newBatchTLS(t, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		writeJSON(w, 503, map[string]any{"error": map[string]any{"type": "overloaded_error"}})
		return true
	})
	c := &BatchClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: ts.URL, APIKey: "k", HTTP: ts.Client()}
	err := c.Results(context.Background(), BatchStatus{resultsURL: ts.URL + "/r"}, func(BatchResult) error { return nil })
	if errclassOf(err) != errclass.ClassRetryable {
		t.Fatalf("503 = %v", err)
	}
	if _, err := c.Status(context.Background(), "msgbatch_1"); errclassOf(err) != errclass.ClassRetryable {
		t.Fatalf("Status 503 = %v", err)
	}
}

// BATCH-R1：只有內建 base_url 的 anthropic、openai 可以用 batch。
func TestBatchSupported(t *testing.T) {
	for _, tc := range []struct {
		cfg  ProviderConfig
		want bool
	}{
		{ProviderConfig{Type: TypeAnthropic, BaseURL: DefaultAnthropicBaseURL}, true},
		{ProviderConfig{Type: TypeAnthropic, BaseURL: DefaultAnthropicBaseURL + "/"}, true},
		{ProviderConfig{Type: TypeOpenAI, BaseURL: DefaultOpenAIBaseURL}, true},
		{ProviderConfig{Type: TypeOpenAI, BaseURL: "https://openrouter.ai/api/v1"}, false},
		{ProviderConfig{Type: TypeAnthropic, BaseURL: "https://proxy.example/anthropic"}, false},
		{ProviderConfig{Type: TypeGemini, BaseURL: DefaultGeminiBaseURL}, false},
	} {
		if got := BatchSupported(tc.cfg); got != tc.want {
			t.Errorf("%+v = %v", tc.cfg, got)
		}
	}
	if NewBatchClient(nil) != nil {
		t.Error("nil client 不應該有 batch client")
	}
	hc := &HTTPClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: DefaultAnthropicBaseURL, APIKey: "k"}
	if bc := NewBatchClient(hc); bc == nil || bc.APIKey != "k" || bc.BaseURL != DefaultAnthropicBaseURL {
		t.Errorf("NewBatchClient = %+v", bc)
	}
}

// 第一次請求就附上 Retry（batch 的結果沒有通過驗證後改走一般 API，BATCH-R4）。
func TestChunkRequestCarriesRetry(t *testing.T) {
	o := RunOptions{Spec: CorrectSpec(), Model: "m", Input: ChunkInput{Chunk: Chunk{Index: 1, From: 1, To: 1}, Chunks: 1, Cues: []Cue{{I: 1, Text: "a"}}, Context: []Cue{}}, Retry: "fix it"}
	req, err := ChunkRequest(o)
	if err != nil || req.Retry != "fix it" || req.Model != "m" || !strings.Contains(req.Data, `"chunk":1`) {
		t.Fatalf("ChunkRequest = %+v, %v", req, err)
	}
	if _, verr := ValidateResponse(CorrectSpec(), o.Input, Response{Problem: "no tool"}); verr == nil || verr.Reason != ReasonInvalidJSON {
		t.Fatalf("沒有輸出時 = %+v", verr)
	}
}

// readLine 對任意輸入不 panic，回傳的行不含換行、不超過上限（不可信的結果檔）。
func FuzzReadLine(f *testing.F) {
	for _, s := range []string{"", "a\nb", "\r\n\r\n", strings.Repeat("x", 100) + "\n", "{\"custom_id\":1}\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		br := bufio.NewReaderSize(bytes.NewReader(data), 16)
		for range len(data) + 2 {
			line, err := readLine(br, 64)
			if len(line) > 65 || bytes.ContainsAny(line, "\n") {
				t.Fatalf("行 = %q", line)
			}
			if err != nil {
				return
			}
		}
	})
}
