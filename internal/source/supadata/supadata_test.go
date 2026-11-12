package supadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	res, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123", ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lang != "en" || len(res.Cues) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if c := res.Cues[1]; c.Index != 2 || c.StartMS != 2000 || c.EndMS != 4500 || c.Text != "Second line." || c.Timing != subtitle.TimingSource {
		t.Errorf("cue 2 = %+v", c)
	}
	if req.URL.Path != "/v1/transcript" || req.URL.Query().Get("url") != "https://www.youtube.com/watch?v=abc123" || req.URL.Query().Get("mode") != "auto" {
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
		{"401", 401, `{}`, false, errclass.ClassNeedsAction},
		{"403", 403, `{}`, false, errclass.ClassNeedsAction},
		{"400", 400, `{}`, false, errclass.ClassPermanent},
		{"回應不是 JSON", 200, `<html>`, false, errclass.ClassRetryable},
		{"非同步任務", 202, `{"jobId":"j1"}`, false, errclass.ClassPermanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := serve(t, tt.status, tt.body)
			_, err := c.Fetch(context.Background(), "https://www.youtube.com/watch?v=abc123", ModeNative)
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

func TestFetchNetworkErrorIsRetryable(t *testing.T) {
	c, _ := serve(t, 200, okBody)
	c.BaseURL = "http://127.0.0.1:1" // 沒有人在聽
	_, err := c.Fetch(context.Background(), "https://youtu.be/abc123", ModeAuto)
	if got, ok := errclass.ClassOf(err); !ok || got != errclass.ClassRetryable {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret-123") {
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
