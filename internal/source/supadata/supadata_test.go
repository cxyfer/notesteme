package supadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const okBody = `{
  "lang": "en",
  "availableLangs": ["en"],
  "content": [
    { "text": "First line.", "offset": 0, "duration": 2000, "lang": "en" },
    { "text": "Second line.", "offset": 2000, "duration": 2500, "lang": "en" }
  ]
}`

// serve 建立回應固定內容的假 Supadata，並記錄收到的請求。
func serve(t *testing.T, status int, body string) (*Client, *http.Request) {
	t.Helper()
	got := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL, APIKey: "sk-secret-123"}, got
}

func TestFetchParsesCues(t *testing.T) {
	c, req := serve(t, 200, okBody)
	res, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lang != "en" || len(res.Cues) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if c := res.Cues[1]; c.Index != 2 || c.StartMS != 2000 || c.EndMS != 4500 || c.Text != "Second line." || c.Timing != subtitle.TimingSource {
		t.Errorf("cue 2 = %+v", c)
	}
	if req.URL.Path != "/v1/transcript" || req.URL.Query().Get("url") != "https://www.youtube.com/watch?v=abc123xxxxx" || req.URL.Query().Get("mode") != "auto" {
		t.Errorf("請求 = %s", req.URL)
	}
	if req.Header.Get("x-api-key") != "sk-secret-123" {
		t.Error("API key 要放在 x-api-key 標頭")
	}
	if strings.Contains(req.URL.String(), "sk-secret-123") {
		t.Error("API key 不可以出現在網址")
	}
}

func TestFetchErrorClasses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		none   bool
		class  errclass.Class
	}{
		{"404 沒有字幕", 404, `{"error":"transcript-unavailable"}`, true, ""},
		{"200 但沒有內容", 200, `{"lang":"en","content":[]}`, true, ""},
		{"429", 429, `{}`, false, errclass.ClassRetryable},
		{"503", 503, `{}`, false, errclass.ClassRetryable},
		{"401 key 無效", 401, `{}`, false, errclass.ClassNeedsAction},
		{"402 方案不支援", 402, `{}`, false, errclass.ClassNeedsAction},
		{"403 內容私人或受限", 403, `{"error":"forbidden"}`, true, ""},
		{"206 transcript-unavailable", 206, `{"error":"transcript-unavailable"}`, true, ""},
		{"400", 400, `{}`, false, errclass.ClassPermanent},
		{"422 其他 4xx", 422, `{}`, false, errclass.ClassPermanent},
		{"500", 500, `{}`, false, errclass.ClassRetryable},
		{"回應不是 JSON", 200, `<html>`, false, errclass.ClassRetryable},
		{"非同步任務", 202, `{"jobId":"j1"}`, false, errclass.ClassPermanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := serve(t, tt.status, tt.body)
			_, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeNative)
			if tt.none {
				if !errors.Is(err, ErrNoTranscript) {
					t.Fatalf("err = %v，預期 ErrNoTranscript", err)
				}
				return
			}
			got, ok := errclass.ClassOf(err)
			if !ok || got != tt.class {
				t.Fatalf("err = %v，類別 %q，預期 %q", err, got, tt.class)
			}
			if strings.Contains(err.Error(), "sk-secret-123") {
				t.Errorf("錯誤訊息含有 API key：%v", err)
			}
		})
	}
}

// 401 與 402 都是 needs_action，但呼叫端要能分辨：前者是 key 的問題，後者是方案不支援。
func TestFetchDistinguishesKeyAndPlanErrors(t *testing.T) {
	c, _ := serve(t, 401, `{}`)
	_, err := c.Fetch(context.Background(), "https://youtu.be/abc123xxxxx", ModeAuto)
	if !errors.Is(err, ErrKeyRejected) || errors.Is(err, ErrPlanUnsupported) {
		t.Errorf("401：err = %v", err)
	}
	c, _ = serve(t, 402, `{}`)
	_, err = c.Fetch(context.Background(), "https://youtu.be/abc123xxxxx", ModeAuto)
	if !errors.Is(err, ErrPlanUnsupported) || errors.Is(err, ErrKeyRejected) {
		t.Errorf("402：err = %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret-123") {
		t.Errorf("錯誤訊息含有 API key：%v", err)
	}
}

func TestFetchNetworkErrorIsRetryable(t *testing.T) {
	c, _ := serve(t, 200, okBody)
	c.BaseURL = "http://127.0.0.1:1" // 沒有人在聽
	_, err := c.Fetch(context.Background(), "https://youtu.be/abc123xxxxx", ModeAuto)
	if got, ok := errclass.ClassOf(err); !ok || got != errclass.ClassRetryable {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret-123") {
		t.Errorf("錯誤訊息含有 API key：%v", err)
	}
}

// 跨網域轉址時，x-api-key 不可以帶到另一個 host（NFR-SEC-3）；3xx 視為 permanent。
func TestFetchDoesNotFollowRedirectWithKey(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(okBody))
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/transcript", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	// 呼叫端提供的 client 預設會跟隨轉址；Fetch 必須自己關掉。
	hc := &http.Client{}
	c := &Client{HTTP: hc, BaseURL: origin.URL, APIKey: "sk-secret-123"}
	_, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeAuto)
	if leaked.Load() {
		t.Fatal("轉址後 API key 被帶到另一個 host")
	}
	if got, ok := errclass.ClassOf(err); !ok || got != errclass.ClassPermanent {
		t.Fatalf("err = %v，預期 permanent", err)
	}
	if hc.CheckRedirect != nil {
		t.Error("不可以修改呼叫端提供的 client")
	}
	if err != nil && strings.Contains(err.Error(), "sk-secret-123") {
		t.Errorf("錯誤訊息含有 API key：%v", err)
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"native", "generate", "auto"} {
		if !ValidMode(m) {
			t.Errorf("%q 應該有效", m)
		}
	}
	if ValidMode("") || ValidMode("fast") {
		t.Error("不認識的 mode 不該有效")
	}
}

// 超過上限的回應不能被截斷後接受或重試：完整的 JSON 後面帶著超量的內容也一樣是 permanent。
func TestFetchRejectsOversizedResponse(t *testing.T) {
	for name, body := range map[string]string{
		"截斷的 JSON":     `{"lang":"en","content":[` + strings.Repeat(" ", maxBodySize),
		"完整 JSON 後的填充": okBody + strings.Repeat(" ", maxBodySize),
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := serve(t, 200, body)
			_, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeNative)
			if got, ok := errclass.ClassOf(err); !ok || got != errclass.ClassPermanent {
				t.Fatalf("err = %v，類別 %q，預期 permanent", err, got)
			}
		})
	}
	// 剛好在上限內的回應仍然正常解析。
	c, _ := serve(t, 200, okBody+strings.Repeat(" ", maxBodySize-len(okBody)))
	if _, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123xxxxx", ModeNative); err != nil {
		t.Fatalf("上限內的回應應該成功：%v", err)
	}
}
