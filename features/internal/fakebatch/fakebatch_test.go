package fakebatch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

func fixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures")
}

// setup 建立假服務、一個 FixtureAnswerer（correct → correct-ok）與兩個 provider 的 httptest server。
func setup(t *testing.T) (*Server, *clock.Fake, map[string]*httptest.Server) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	ans := &FixtureAnswerer{Dir: fixturesDir(t), InputTokens: 10000, OutputTokens: 2000}
	ans.Set("correct", "correct-ok")
	s := New(clk, ans)
	srvs := map[string]*httptest.Server{}
	for _, id := range []string{Anthropic, OpenAI} {
		srv := httptest.NewServer(s.Handler(id))
		t.Cleanup(srv.Close)
		srvs[id] = srv
	}
	return s, clk, srvs
}

// dataText 是 LLM 請求中的字幕資料區塊（同 fakellm 的格式）。
func dataText(chunk int) string {
	b, _ := json.Marshal(map[string]any{"chunk": chunk, "chunks": 2, "context": []any{}, "cues": []any{map[string]any{"i": 1, "text": "x"}}})
	return "<transcript_data>\n" + string(b) + "\n</transcript_data>"
}

func anthropicParams(chunk int) map[string]any {
	return map[string]any{
		"model": "fixture-model", "max_tokens": 1000,
		"tools":       []any{map[string]any{"name": "notesteme_correct", "input_schema": map[string]any{"type": "object"}}},
		"tool_choice": map[string]any{"type": "tool", "name": "notesteme_correct"},
		"messages":    []any{map[string]any{"role": "user", "content": dataText(chunk)}},
	}
}

func openaiBody(chunk int) map[string]any {
	return map[string]any{
		"model": "fixture-model",
		"response_format": map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": "notesteme_correct", "strict": true, "schema": map[string]any{"type": "object"}}},
		"messages": []any{map[string]any{"role": "user", "content": dataText(chunk)}},
	}
}

func do(t *testing.T, srv *httptest.Server, id, method, path string, body io.Reader, contentType string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, body)
	if id == Anthropic {
		req.Header.Set("x-api-key", "sk-ant-notesteme-fixture")
	} else {
		req.Header.Set("Authorization", "Bearer sk-notesteme-fixture")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func doJSON(t *testing.T, srv *httptest.Server, id, method, path string, v any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	status, b := do(t, srv, id, method, path, body, "application/json")
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return status, out
}

func jsonl(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("結果不是 JSONL：%v（%s）", err, sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func TestAnthropicBatchCompletesAfterTheDelay(t *testing.T) {
	s, clk, srvs := setup(t)
	srv := srvs[Anthropic]
	s.FailChunk(Anthropic, 2)
	status, created := doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches", map[string]any{"requests": []any{
		map[string]any{"custom_id": "task-1", "params": anthropicParams(1)},
		map[string]any{"custom_id": "task-2", "params": anthropicParams(2)},
	}})
	if status != 200 || created["processing_status"] != "in_progress" || created["results_url"] != nil {
		t.Fatalf("建立：%d %v", status, created)
	}
	id := created["id"].(string)
	if st, _ := do(t, srv, Anthropic, "GET", "/v1/messages/batches/"+id+"/results", nil, ""); st != 404 {
		t.Errorf("完成之前取結果 = %d，預期 404", st)
	}
	clk.Advance(9 * time.Minute)
	if _, got := doJSON(t, srv, Anthropic, "GET", "/v1/messages/batches/"+id, nil); got["processing_status"] != "in_progress" {
		t.Errorf("9 分鐘時 = %v", got["processing_status"])
	}
	clk.Advance(time.Minute)
	_, got := doJSON(t, srv, Anthropic, "GET", "/v1/messages/batches/"+id, nil)
	counts, _ := got["request_counts"].(map[string]any)
	if got["processing_status"] != "ended" || counts["succeeded"] != 1.0 || counts["errored"] != 1.0 {
		t.Fatalf("10 分鐘時 = %v", got)
	}
	url, _ := got["results_url"].(string)
	if !strings.HasPrefix(url, srv.URL) {
		t.Errorf("results_url = %q，應該在同一個主機", url)
	}
	_, raw := do(t, srv, Anthropic, "GET", "/v1/messages/batches/"+id+"/results", nil, "")
	lines := jsonl(t, raw)
	if len(lines) != 2 {
		t.Fatalf("結果 %d 列", len(lines))
	}
	res := lines[0]["result"].(map[string]any)
	msg := res["message"].(map[string]any)
	block := msg["content"].([]any)[0].(map[string]any)
	if lines[0]["custom_id"] != "task-1" || res["type"] != "succeeded" || block["type"] != "tool_use" || block["input"] == nil {
		t.Errorf("第一列 = %v", lines[0])
	}
	if usage := msg["usage"].(map[string]any); usage["input_tokens"] != 10000.0 || usage["output_tokens"] != 2000.0 {
		t.Errorf("usage = %v", usage)
	}
	if res := lines[1]["result"].(map[string]any); lines[1]["custom_id"] != "task-2" || res["type"] != "errored" {
		t.Errorf("第二列（chunk 2 失敗）= %v", lines[1])
	}
	if s.Batches(Anthropic) != 1 || s.Requests(Anthropic) != 2 || s.Cancelled(Anthropic) != 0 {
		t.Errorf("計數 = %d %d %d", s.Batches(Anthropic), s.Requests(Anthropic), s.Cancelled(Anthropic))
	}
}

func TestAnthropicCancelBeforeAndAfterCompletion(t *testing.T) {
	s, clk, srvs := setup(t)
	srv := srvs[Anthropic]
	create := func() string {
		_, b := doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches", map[string]any{"requests": []any{
			map[string]any{"custom_id": "task-1", "params": anthropicParams(1)},
		}})
		return b["id"].(string)
	}
	early := create()
	_, got := doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches/"+early+"/cancel", nil)
	if got["processing_status"] != "ended" || got["request_counts"].(map[string]any)["canceled"] != 1.0 {
		t.Errorf("完成之前取消 = %v", got)
	}
	late := create()
	clk.Advance(11 * time.Minute)
	_, got = doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches/"+late+"/cancel", nil)
	if got["request_counts"].(map[string]any)["succeeded"] != 1.0 {
		t.Errorf("完成之後取消不改變結果 = %v", got)
	}
	doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches/"+late+"/cancel", nil)
	if n := s.Cancelled(Anthropic); n != 2 {
		t.Errorf("Cancelled = %d，預期 2（同一個 batch 只算一次）", n)
	}
}

func TestNeverCompletesAndResultsURLOverride(t *testing.T) {
	s, clk, srvs := setup(t)
	srv := srvs[Anthropic]
	s.Never(Anthropic)
	_, b := doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches", map[string]any{"requests": []any{
		map[string]any{"custom_id": "task-1", "params": anthropicParams(1)},
	}})
	id := b["id"].(string)
	clk.Advance(48 * time.Hour)
	if _, got := doJSON(t, srv, Anthropic, "GET", "/v1/messages/batches/"+id, nil); got["processing_status"] != "in_progress" {
		t.Errorf("Never 的 batch = %v", got["processing_status"])
	}
	s.CompleteAfter(Anthropic, time.Minute)
	s.PointResultsTo("https://results.evil.example/batch.jsonl")
	_, b = doJSON(t, srv, Anthropic, "POST", "/v1/messages/batches", map[string]any{"requests": []any{
		map[string]any{"custom_id": "task-2", "params": anthropicParams(1)},
	}})
	clk.Advance(time.Minute)
	if _, got := doJSON(t, srv, Anthropic, "GET", "/v1/messages/batches/"+b["id"].(string), nil); got["results_url"] != "https://results.evil.example/batch.jsonl" {
		t.Errorf("results_url = %v", got["results_url"])
	}
}

func upload(t *testing.T, srv *httptest.Server, lines ...map[string]any) string {
	t.Helper()
	var content bytes.Buffer
	for _, l := range lines {
		b, _ := json.Marshal(l)
		content.Write(b)
		content.WriteByte('\n')
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("purpose", "batch")
	fw, _ := mw.CreateFormFile("file", "batch.jsonl")
	_, _ = fw.Write(content.Bytes())
	_ = mw.Close()
	status, raw := do(t, srv, OpenAI, "POST", "/v1/files", &body, mw.FormDataContentType())
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if status != 200 || out["purpose"] != "batch" {
		t.Fatalf("上傳：%d %s", status, raw)
	}
	return out["id"].(string)
}

func TestOpenAIBatchUsesFiles(t *testing.T) {
	s, clk, srvs := setup(t)
	srv := srvs[OpenAI]
	s.FailChunk(OpenAI, 2)
	line := func(id string, chunk int) map[string]any {
		return map[string]any{"custom_id": id, "method": "POST", "url": "/v1/chat/completions", "body": openaiBody(chunk)}
	}
	file := upload(t, srv, line("task-1", 1), line("task-2", 2))
	status, created := doJSON(t, srv, OpenAI, "POST", "/v1/batches",
		map[string]any{"input_file_id": file, "endpoint": "/v1/chat/completions", "completion_window": "24h"})
	if status != 200 || created["status"] != "in_progress" || created["output_file_id"] != nil {
		t.Fatalf("建立：%d %v", status, created)
	}
	clk.Advance(10 * time.Minute)
	_, got := doJSON(t, srv, OpenAI, "GET", "/v1/batches/"+created["id"].(string), nil)
	if got["status"] != "completed" || got["output_file_id"] == nil || got["error_file_id"] == nil {
		t.Fatalf("完成 = %v", got)
	}
	_, out := do(t, srv, OpenAI, "GET", fmt.Sprintf("/v1/files/%s/content", got["output_file_id"]), nil, "")
	lines := jsonl(t, out)
	if len(lines) != 1 || lines[0]["custom_id"] != "task-1" {
		t.Fatalf("輸出檔 = %s", out)
	}
	resp := lines[0]["response"].(map[string]any)
	choice := resp["body"].(map[string]any)["choices"].([]any)[0].(map[string]any)
	if resp["status_code"] != 200.0 || !strings.Contains(choice["message"].(map[string]any)["content"].(string), "cues") {
		t.Errorf("輸出 = %v", resp)
	}
	_, errs := do(t, srv, OpenAI, "GET", fmt.Sprintf("/v1/files/%s/content", got["error_file_id"]), nil, "")
	if el := jsonl(t, errs); len(el) != 1 || el[0]["custom_id"] != "task-2" || el[0]["error"] == nil {
		t.Errorf("錯誤檔 = %s", errs)
	}
}

func TestOpenAICancelAndValidation(t *testing.T) {
	s, _, srvs := setup(t)
	srv := srvs[OpenAI]
	file := upload(t, srv, map[string]any{"custom_id": "task-1", "method": "POST", "url": "/v1/chat/completions", "body": openaiBody(1)})
	if st, _ := doJSON(t, srv, OpenAI, "POST", "/v1/batches",
		map[string]any{"input_file_id": file, "endpoint": "/v1/embeddings", "completion_window": "24h"}); st != 400 {
		t.Errorf("不支援的 endpoint = %d", st)
	}
	_, created := doJSON(t, srv, OpenAI, "POST", "/v1/batches",
		map[string]any{"input_file_id": file, "endpoint": "/v1/chat/completions", "completion_window": "24h"})
	_, got := doJSON(t, srv, OpenAI, "POST", "/v1/batches/"+created["id"].(string)+"/cancel", nil)
	if got["status"] != "cancelled" || got["output_file_id"] != nil {
		t.Errorf("取消 = %v", got)
	}
	if s.Cancelled(OpenAI) != 1 || s.Batches(OpenAI) != 1 {
		t.Errorf("計數 = %d %d", s.Cancelled(OpenAI), s.Batches(OpenAI))
	}
}

func TestAuthAndOwns(t *testing.T) {
	s, _, srvs := setup(t)
	s.RequireKey(Anthropic, "right")
	resp, err := srvs[Anthropic].Client().Post(srvs[Anthropic].URL+"/v1/messages/batches", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("沒有 key = %d", resp.StatusCode)
	}
	for _, c := range []struct {
		id, path string
		want     bool
	}{
		{Anthropic, "/v1/messages/batches/abc/results", true},
		{Anthropic, "/v1/messages", false},
		{OpenAI, "/v1/files/file-1/content", true},
		{OpenAI, "/v1/chat/completions", false},
	} {
		if got := Owns(c.id, c.path); got != c.want {
			t.Errorf("Owns(%s, %s) = %v", c.id, c.path, got)
		}
	}
}

func TestChunkOf(t *testing.T) {
	b, _ := json.Marshal(anthropicParams(3))
	if n := chunkOf(b); n != 3 {
		t.Errorf("chunkOf = %d", n)
	}
	if n := chunkOf([]byte(`{"messages":[]}`)); n != 0 {
		t.Errorf("沒有資料區塊 = %d", n)
	}
}
