package fakenotify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// post 對假服務送出請求；host 寫進 Host 標頭，模擬 fakenet 依主機分派到同一個 handler。
func post(t *testing.T, srv *httptest.Server, host, path string, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDefaultResponsesPerKind(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cases := []struct {
		host, path string
		status     int
	}{
		{"ntfy.sh", "/", http.StatusOK},
		{"discord.com", "/api/webhooks/1234/abcd", http.StatusNoContent},
		{"api.telegram.org", "/bot123:ABC/sendMessage", http.StatusOK},
		{"hooks.example.com", "/notesteme", http.StatusNoContent},
	}
	for _, c := range cases {
		resp := post(t, srv, c.host, c.path, []byte(`{"topic":"t","message":"hi"}`), nil)
		if resp.StatusCode != c.status {
			t.Errorf("%s%s：狀態碼 %d，預期 %d", c.host, c.path, resp.StatusCode, c.status)
		}
	}
	if n := len(fake.RequestsTo("https://discord.com/api/webhooks/1234/abcd?wait=true")); n != 1 {
		t.Errorf("Discord 端點收到 %d 個請求，預期 1（query 不算在端點內）", n)
	}
	if n := len(fake.Requests()); n != 4 {
		t.Errorf("總共 %d 個請求，預期 4", n)
	}
}

func TestFailNextThenRecover(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	const ep = "https://hooks.example.com/notesteme"
	fake.FailNext(ep, http.StatusServiceUnavailable, 2)

	var got []int
	for range 3 {
		got = append(got, post(t, srv, "hooks.example.com", "/notesteme", []byte(`{}`), nil).StatusCode)
	}
	want := []int{503, 503, 204}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("狀態碼 %v，預期 %v", got, want)
		}
	}
	// 其他端點不受影響
	if s := post(t, srv, "hooks.example.com", "/other", []byte(`{}`), nil).StatusCode; s != 204 {
		t.Errorf("其他端點回 %d，預期 204", s)
	}
}

func TestFailAlwaysWinsAndSetsRetryAfter(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	const ep = "https://discord.com/api/webhooks/1/x"
	fake.FailAlways(ep, http.StatusTooManyRequests)
	fake.FailNext(ep, http.StatusInternalServerError, 1) // 不能蓋掉「一律」

	for range 3 {
		resp := post(t, srv, "discord.com", "/api/webhooks/1/x", []byte(`{}`), nil)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("狀態碼 %d，預期 429", resp.StatusCode)
		}
		if ra := resp.Header.Get("Retry-After"); ra != strconv.Itoa(RetryAfterSeconds) {
			t.Fatalf("Retry-After = %q", ra)
		}
	}
}

func TestSignatureVerification(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	body := []byte(`{"event":"item.done"}`)
	ts := "1795435200"
	post(t, srv, "hooks.example.com", "/sig", body, map[string]string{
		"X-Notesteme-Timestamp": ts,
		"X-Notesteme-Signature": Signature("s3cret-hmac-key", ts, body),
	})
	r, ok := fake.Last("https://hooks.example.com/sig")
	if !ok {
		t.Fatal("沒有收到請求")
	}
	if got, ok := VerifySignature(r, "s3cret-hmac-key"); !ok || got != 1795435200 {
		t.Errorf("簽章驗證失敗：ts=%d ok=%v", got, ok)
	}
	if _, ok := VerifySignature(r, "wrong-key"); ok {
		t.Error("錯誤的 secret 不應該驗證通過")
	}
	// 竄改 body 後驗證失敗
	r.Body = []byte(`{"event":"item.failed"}`)
	if _, ok := VerifySignature(r, "s3cret-hmac-key"); ok {
		t.Error("竄改的 body 不應該驗證通過")
	}
}

func TestTextIncludesDecodedJSONAndHeaders(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	// token 是 "token" 的跳脫寫法：原始 body 看不到，解碼後要看得到
	post(t, srv, "ntfy.sh", "/", []byte(`{"topic":"t","message":"secret token-abc"}`), map[string]string{"Authorization": "Bearer tk_ntfy_1234"})
	r, _ := fake.Last("https://ntfy.sh/")
	text := r.Text()
	for _, want := range []string{"token-abc", "Bearer tk_ntfy_1234"} {
		if !strings.Contains(text, want) {
			t.Errorf("Text() 不含 %q", want)
		}
	}
	var v map[string]any
	if err := json.Unmarshal(r.Body, &v); err != nil || v["topic"] != "t" {
		t.Errorf("body 沒有原樣記錄：%s", r.Body)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	fake := New()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	resp := post(t, srv, "hooks.example.com", "/big", bytes.Repeat([]byte("a"), MaxBody+10), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("狀態碼 %d，預期 413", resp.StatusCode)
	}
}

func TestEndpointOfIgnoresPortAndCase(t *testing.T) {
	if a, b := EndpointOf("https://Hooks.Example.com:8443/x?y=1"), "hooks.example.com/x"; a != b {
		t.Errorf("EndpointOf = %q，預期 %q", a, b)
	}
	if EndpointOf("not a url") != "" {
		t.Error("無法解析的網址應該回空字串")
	}
}
