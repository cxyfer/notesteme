package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/fakebatch"
	"github.com/cxyfer/notesteme/features/internal/fakefeed"
	"github.com/cxyfer/notesteme/features/internal/fakenotify"
	"github.com/cxyfer/notesteme/features/internal/fakeoidc"
)

func registered(w *World, host string) bool {
	_, err := w.Net.LookupNetIP(context.Background(), "ip", host)
	return err == nil
}

// fakefeed、fakenotify 不預設登記（README 的「M4 的假服務」），閘門句型呼叫之後才有。
func TestM4FakesAreOptIn(t *testing.T) {
	w := NewT(t)
	if w.Feed == nil || w.Notify == nil || w.Batch() != nil {
		t.Fatalf("M4 的假服務：feed=%v notify=%v batch=%v", w.Feed, w.Notify, w.Batch())
	}
	for _, h := range append([]string{fakefeed.Host}, fakenotify.Hosts...) {
		if registered(w, h) {
			t.Errorf("%s 不該預設登記", h)
		}
	}
	if w.Env[OIDCClientSecretEnv] != TestOIDCClientSecret {
		t.Errorf("NOTESTEME_OIDC_CLIENT_SECRET = %q", w.Env[OIDCClientSecretEnv])
	}
	if w.ServeFeeds() != w.Feed || !registered(w, fakefeed.Host) {
		t.Error("ServeFeeds 之後 www.youtube.com 應該有 fake origin")
	}
	if n, err := w.ReachNotifyEndpoints(); err != nil || n != w.Notify {
		t.Errorf("ReachNotifyEndpoints 應回傳 World.Notify：%v", err)
	}
	for i, h := range fakenotify.Hosts {
		if !registered(w, h) {
			t.Errorf("%s 沒有登記", h)
		}
		if ips, err := w.Net.LookupNetIP(t.Context(), "ip", h); err != nil || len(ips) != 1 || ips[0].String() != NotifyIPs[i] {
			t.Errorf("%s 應解析到 %s：%v %v", h, NotifyIPs[i], ips, err)
		}
	}
	if _, ok := w.NotifyReceivedAt(0); ok {
		t.Error("還沒有請求")
	}
}

// LLM 主機上的 batch 路徑：沒有啟用時 404 並記違規；啟用之後交給 fakebatch；其他路徑原封不動交給 fakellm。
func TestLLMHostDispatchesBatchPaths(t *testing.T) {
	w := NewT(t)
	var llmCalls int
	llm := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { llmCalls++; _, _ = io.WriteString(rw, "llm") })
	h := w.llmHandler(fakebatch.Anthropic, llm)
	oh := w.llmHandler(fakebatch.OpenAI, llm)

	serve := func(h http.Handler, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "https://api.anthropic.com"+path, strings.NewReader("{}")))
		return rec
	}
	if rec := serve(h, "POST", "/v1/messages"); rec.Body.String() != "llm" || llmCalls != 1 {
		t.Errorf("一般請求要交給 fakellm：%d %q", rec.Code, rec.Body)
	}
	if rec := serve(oh, "POST", "/v1/chat/completions"); rec.Body.String() != "llm" || llmCalls != 2 {
		t.Errorf("OpenAI 的一般請求：%d %q", rec.Code, rec.Body)
	}
	if err := w.Check(); err != nil {
		t.Fatalf("一般請求不是違規：%v", err)
	}
	if rec := serve(h, "POST", "/v1/messages/batches"); rec.Code != http.StatusNotFound {
		t.Errorf("沒有啟用 batch：%d", rec.Code)
	}
	if rec := serve(oh, "POST", "/v1/files"); rec.Code != http.StatusNotFound {
		t.Errorf("沒有啟用 batch（OpenAI）：%d", rec.Code)
	}
	if err := w.Check(); err == nil || !strings.Contains(err.Error(), "fakebatch") {
		t.Errorf("batch 路徑應記違規：%v", err)
	}
	if err := w.Check(); err != nil {
		t.Errorf("違規取出後清空：%v", err)
	}

	b := w.EnableBatch(&fakebatch.FixtureAnswerer{Dir: w.FixturesDir})
	if w.EnableBatch(nil) != b || w.Batch() != b {
		t.Error("EnableBatch 只建立一次")
	}
	// 沒有帶 API key：fakebatch 自己回 401，不是 harness 的 404，也不記違規。
	if rec := serve(h, "GET", "/v1/messages/batches/msgbatch_x"); rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "404 page not found") {
		t.Errorf("啟用之後應交給 fakebatch：%d %q", rec.Code, rec.Body)
	}
	if err := w.Check(); err != nil {
		t.Errorf("啟用之後不是違規：%v", err)
	}
	if llmCalls != 2 {
		t.Errorf("batch 路徑不該交給 fakellm：%d", llmCalls)
	}
}

func TestStartOIDC(t *testing.T) {
	w := NewT(t)
	if _, err := w.StartOIDC(fakeoidc.Config{Issuer: "http://id.example.com", ClientID: "c"}); err == nil {
		t.Error("issuer 必須是 https")
	}
	s, err := w.StartOIDC(fakeoidc.Config{Issuer: "https://ID.example.com", ClientID: "notesteme", Subject: "user-7f3a"})
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := w.StartOIDC(fakeoidc.Config{Issuer: "https://id.example.com/", ClientID: "x"}); again != s {
		t.Error("同一個 issuer 只建立一次")
	}
	if !registered(w, "id.example.com") {
		t.Error("issuer 的主機沒有登記")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "https://id.example.com/.well-known/openid-configuration", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issuer"`) {
		t.Errorf("discovery = %d %s", rec.Code, rec.Body)
	}
}
