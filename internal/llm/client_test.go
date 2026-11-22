package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// recorder 是假的 provider：記下請求，回傳指定的狀態碼與 body。
type recorder struct {
	status int
	header map[string]string
	body   string
	got    *http.Request
	raw    []byte
}

func (rc *recorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.got = r
		rc.raw, _ = io.ReadAll(r.Body)
		for k, v := range rc.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(rc.status)
		_, _ = io.WriteString(w, rc.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRequest() Request {
	return Request{
		Model: "m1", MaxTokens: 2048, Spec: CorrectSpec(),
		Fixed: Fixed{System: "SYSTEM", Prefix: MetadataOpen + `{"title":"T"}` + MetadataClose},
		Data:  DataOpen + `{"chunk":1,"chunks":1,"context":[],"cues":[{"i":1,"text":"a"}]}` + DataClose,
	}
}

func TestAnthropicWire(t *testing.T) {
	rc := &recorder{status: 200, body: `{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"notesteme_correct","input":{"cues":[{"i":1,"text":"A"}]}}],
		"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3}}`}
	srv := rc.server(t)
	c := &HTTPClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: srv.URL, APIKey: "sk-test", HTTP: srv.Client()}
	req := testRequest()
	req.Retry = "RETRY"
	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Output) != `{"cues":[{"i":1,"text":"A"}]}` || resp.Usage.InputTokens != 10 || resp.Usage.CacheReadTokens != 3 {
		t.Errorf("回應 = %s %+v", resp.Output, resp.Usage)
	}
	if rc.got.URL.Path != "/v1/messages" || rc.got.Header.Get("x-api-key") != "sk-test" || rc.got.Header.Get("anthropic-version") != AnthropicVersion {
		t.Errorf("端點或標頭不正確：%s %v", rc.got.URL.Path, rc.got.Header)
	}
	var body struct {
		Model      string `json:"model"`
		MaxTokens  int    `json:"max_tokens"`
		System     []struct{ Text string }
		ToolChoice struct{ Type, Name string } `json:"tool_choice"`
		Tools      []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"input_schema"`
		}
		Messages []struct {
			Role    string
			Content []struct {
				Text         string
				CacheControl *struct{ Type string } `json:"cache_control"`
			}
		}
	}
	if err := json.Unmarshal(rc.raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "m1" || body.MaxTokens != 2048 || body.System[0].Text != "SYSTEM" {
		t.Errorf("body = %s", rc.raw)
	}
	if body.ToolChoice.Type != "tool" || body.ToolChoice.Name != "notesteme_correct" || body.Tools[0].Name != "notesteme_correct" {
		t.Errorf("tool_choice 沒有強制 notesteme_correct：%s", rc.raw)
	}
	blocks := body.Messages[0].Content
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || len(blocks) != 3 {
		t.Fatalf("user message 不正確：%s", rc.raw)
	}
	// cache 標記只在固定前綴的結尾（LLM-R3），資料區塊與重試說明在它之後。
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" || blocks[1].CacheControl != nil || blocks[2].CacheControl != nil {
		t.Errorf("cache_control 的位置不正確：%s", rc.raw)
	}
	if !strings.HasPrefix(blocks[1].Text, DataOpen) || blocks[2].Text != "RETRY" {
		t.Errorf("資料區塊或重試說明的位置不正確：%s", rc.raw)
	}
}

func TestAnthropicWithoutToolUseIsAValidationProblem(t *testing.T) {
	for name, body := range map[string]string{
		"只有文字":            `{"content":[{"type":"text","text":"this is not json"}],"stop_reason":"end_turn","usage":{}}`,
		"被 max_tokens 截斷": `{"content":[{"type":"tool_use","name":"notesteme_correct","input":{}}],"stop_reason":"max_tokens","usage":{}}`,
		"tool 名稱不同":       `{"content":[{"type":"tool_use","name":"other","input":{}}],"stop_reason":"tool_use","usage":{}}`,
	} {
		rc := &recorder{status: 200, body: body}
		srv := rc.server(t)
		c := &HTTPClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
		resp, err := c.Complete(context.Background(), testRequest())
		if err != nil || resp.Output != nil || resp.Problem == "" {
			t.Errorf("%s：應該是沒有輸出的成功回應：%+v %v", name, resp, err)
		}
	}
}

func TestOpenAIWire(t *testing.T) {
	rc := &recorder{status: 200, body: `{"choices":[{"message":{"role":"assistant","content":"{\"cues\":[]}"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":7,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}`}
	srv := rc.server(t)
	c := &HTTPClient{ID: "local-vllm", Type: TypeOpenAI, BaseURL: srv.URL + "/v1", APIKey: "sk-o", HTTP: srv.Client()}
	resp, err := c.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Output) != `{"cues":[]}` || resp.Usage.InputTokens != 7 || resp.Usage.CacheReadTokens != 4 {
		t.Errorf("回應 = %s %+v", resp.Output, resp.Usage)
	}
	if rc.got.URL.Path != "/v1/chat/completions" || rc.got.Header.Get("Authorization") != "Bearer sk-o" {
		t.Errorf("端點或標頭不正確：%s %v", rc.got.URL.Path, rc.got.Header)
	}
	var body struct {
		Messages       []struct{ Role, Content string }
		ResponseFormat struct {
			Type       string
			JSONSchema struct {
				Name   string
				Strict bool
				Schema map[string]any
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(rc.raw, &body); err != nil {
		t.Fatal(err)
	}
	rf := body.ResponseFormat
	if rf.Type != "json_schema" || rf.JSONSchema.Name != "notesteme_correct" || !rf.JSONSchema.Strict {
		t.Errorf("response_format 不正確：%s", rc.raw)
	}
	if err := ValidateStrictSchema(rf.JSONSchema.Schema); err != nil {
		t.Errorf("送出的 schema 不符合 strict：%v", err)
	}
	if len(body.Messages) != 2 || body.Messages[0].Content != "SYSTEM" || !strings.HasPrefix(body.Messages[1].Content, MetadataOpen) ||
		!strings.Contains(body.Messages[1].Content, DataOpen) {
		t.Errorf("messages 不正確：%s", rc.raw)
	}
}

func TestOpenAIProblems(t *testing.T) {
	for name, body := range map[string]string{
		"沒有 choices": `{"choices":[]}`,
		"拒絕":         `{"choices":[{"message":{"content":null,"refusal":"no"},"finish_reason":"stop"}]}`,
		"截斷":         `{"choices":[{"message":{"content":"{"},"finish_reason":"length"}]}`,
	} {
		rc := &recorder{status: 200, body: body}
		srv := rc.server(t)
		c := &HTTPClient{ID: "openai", Type: TypeOpenAI, BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
		resp, err := c.Complete(context.Background(), testRequest())
		if err != nil || resp.Output != nil || resp.Problem == "" {
			t.Errorf("%s：應該是沒有輸出的成功回應：%+v %v", name, resp, err)
		}
	}
}

// 錯誤分類（LLM-R6、R11、R12）：訊息不帶 key 與請求內容。
func TestStatusErrors(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		status     int
		header     map[string]string
		class      errclass.Class
		message    string
		actions    []string
		retryAfter time.Duration
	}{
		{404, nil, errclass.ClassNeedsAction, "模型 m1 已停用或不存在。", []string{ActionChangeModel}, 0},
		{401, nil, errclass.ClassNeedsAction, "LLM provider anthropic 拒絕了 API key（HTTP 401）。", []string{ActionSetAPIKey}, 0},
		{403, nil, errclass.ClassNeedsAction, "LLM provider anthropic 拒絕了 API key（HTTP 403）。", []string{ActionSetAPIKey}, 0},
		{429, map[string]string{"Retry-After": "30"}, errclass.ClassRetryable, "", nil, 30 * time.Second},
		{429, map[string]string{"Retry-After": now.Add(90 * time.Second).Format(http.TimeFormat)}, errclass.ClassRetryable, "", nil, 90 * time.Second},
		{429, map[string]string{"Retry-After": "-5"}, errclass.ClassRetryable, "", nil, 0},
		{500, nil, errclass.ClassRetryable, "", nil, 0},
		{529, map[string]string{"Retry-After": "99999"}, errclass.ClassRetryable, "", nil, time.Hour},
		{400, nil, errclass.ClassPermanent, "LLM provider anthropic 拒絕了請求（HTTP 400），重試也不會成功。", nil, 0},
		{302, map[string]string{"Location": "http://example.invalid/"}, errclass.ClassPermanent, "LLM provider anthropic 拒絕了請求（HTTP 302），重試也不會成功。", nil, 0},
	}
	for _, tc := range cases {
		rc := &recorder{status: tc.status, header: tc.header, body: `{"type":"error","error":{"type":"x","message":"model: m1"}}`}
		srv := rc.server(t)
		c := &HTTPClient{ID: "anthropic", Type: TypeAnthropic, BaseURL: srv.URL, APIKey: "sk-secret-key", HTTP: srv.Client(), Now: func() time.Time { return now }}
		_, err := c.Complete(context.Background(), testRequest())
		var ce *errclass.Error
		if !errors.As(err, &ce) {
			t.Fatalf("HTTP %d：錯誤不是 *errclass.Error：%v", tc.status, err)
		}
		if ce.Class != tc.class || ce.Message != tc.message || strings.Join(ce.Actions, ",") != strings.Join(tc.actions, ",") || ce.RetryAfter != tc.retryAfter {
			t.Errorf("HTTP %d：%+v", tc.status, ce)
		}
		if strings.Contains(err.Error(), "sk-secret-key") {
			t.Errorf("HTTP %d：錯誤訊息含 API key：%v", tc.status, err)
		}
	}
}

// FuzzDecode：provider 的回應是不可信的輸入，解析不能 panic。
func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		`{"content":[{"type":"tool_use","name":"notesteme_correct","input":{"cues":[]}}]}`,
		`{"choices":[{"message":{"content":"{}"}}]}`, `{"choices":[{"message":{"content":null}}]}`, `null`, `[]`, `{"content":null}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if r, err := decodeAnthropic(b, "notesteme_correct"); err == nil && r.Output == nil && r.Problem == "" {
			t.Fatalf("anthropic：沒有輸出也沒有原因：%s", b)
		}
		if r, err := decodeOpenAI(b); err == nil && r.Output == nil && r.Problem == "" {
			t.Fatalf("openai：沒有輸出也沒有原因：%s", b)
		}
		_ = errorSummary(b)
	})
}
