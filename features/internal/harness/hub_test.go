package harness

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/features/internal/fakeoauth"
)

func newWorld(t *testing.T) *World {
	t.Helper()
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func get(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s：%v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// 真 Hub 在程序內啟動，經 fakenet 以 https://hub.example.org 連得到；資料在場景目錄的 hub/。
func TestStartHub(t *testing.T) {
	w := newWorld(t)
	// 啟動之前不登記任何 Hub 與 OAuth 的主機（其他場景撞上 trap 的行為不變）。
	if w.OAuth(fakeoauth.GitHub) != nil || w.HubKind() != HubNone {
		t.Fatal("啟動之前不應該有 Hub 或假供應商")
	}
	if err := w.StartHub(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := w.HubHTTPClient()
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/api/v1/openapi.json": 200, "/api/v1/me": 401, "/auth/github/start": 302, "/mcp": 501} {
		if code, body := get(t, c, HubPublicURL+path); code != want {
			t.Errorf("%s = %d %s，預期 %d", path, code, body, want)
		}
	}
	// 假供應商以真實主機名稱掛在 fakenet 上。
	if code, _ := get(t, c, "https://api.github.com/user"); code != http.StatusUnauthorized {
		t.Errorf("api.github.com/user（沒有 token）= %d，預期假供應商的 401", code)
	}
	if _, err := os.Stat(w.HubDir()); err != nil {
		t.Errorf("Hub 資料目錄：%v", err)
	}
	st, err := w.HubStore()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.CountUsers(context.Background()); err != nil || n != 0 {
		t.Errorf("CountUsers = %d %v", n, err)
	}
	// 再呼叫一次不會重複啟動。
	if err := w.StartHub(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.StartFakeHub(); !errors.Is(err, ErrHubKindConflict) {
		t.Errorf("真 Hub 之後啟動 fakehub：err = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close：%v", err)
	}
	if w.HubServer() != nil {
		t.Error("Close 之後真 Hub 應該已經關閉")
	}
}

func TestStartFakeHub(t *testing.T) {
	w := newWorld(t)
	if err := w.StartFakeHub(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", HubPublicURL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+TestHubToken)
	resp, err := w.HubHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("fakehub /api/v1/me = %d", resp.StatusCode)
	}
	if w.OAuth(fakeoauth.Google) != nil {
		t.Error("fakehub 的場景不登記假供應商")
	}
	if err := w.StartHub(context.Background()); !errors.Is(err, ErrHubKindConflict) {
		t.Errorf("fakehub 之後啟動真 Hub：err = %v", err)
	}
}

// 預設設定與 `the Hub config contains:` 合併：物件逐鍵合併，陣列整個取代。
func TestHubConfigMerge(t *testing.T) {
	w := newWorld(t)
	for _, d := range []string{
		"oauth:\n  google: { client_id: \"\" }\ngroups:\n  contributors:\n    rules: [ { provider: linuxdo, trust_level_gte: 2 } ]\n",
		"groups:\n  contributors:\n    rules: [ { provider: github, org_member: [x] } ]\nsite: { visibility: public }\n",
	} {
		if err := w.AddHubConfig(d); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := w.HubConfigYAML()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	oauth := m["oauth"].(map[string]any)
	if oauth["google"].(map[string]any)["client_id"] != "" || oauth["github"].(map[string]any)["client_id"] != testOAuthClientID(fakeoauth.GitHub) {
		t.Errorf("oauth = %v", oauth)
	}
	rules := m["groups"].(map[string]any)["contributors"].(map[string]any)["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["provider"] != "github" {
		t.Errorf("陣列應該整個取代：%v", rules)
	}
	if m["site"].(map[string]any)["visibility"] != "public" || m["public_url"] != HubPublicURL {
		t.Errorf("合併結果 = %v", m)
	}
	if err := w.AddHubConfig("- not\n- an object\n"); err == nil {
		t.Error("不是 YAML 物件時應該失敗")
	}
}

// 啟動之後改設定：下一個請求之前以新設定重新啟動，資料保留。
func TestHubRestartKeepsData(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if err := w.StartHub(ctx); err != nil {
		t.Fatal(err)
	}
	first := w.HubServer()
	if err := w.RestartHubIfNeeded(ctx); err != nil || w.HubServer() != first {
		t.Fatalf("設定沒有變時不重新啟動：%v", err)
	}
	if err := w.AddHubConfig("rate_limits: { requests_per_minute: 3 }\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.RestartHubIfNeeded(ctx); err != nil {
		t.Fatal(err)
	}
	if w.HubServer() == first || w.HubServer().Config().RateLimits.RequestsPerMinute != 3 {
		t.Error("設定改變後應該以新設定重新啟動")
	}
	if code, _ := get(t, w.HubHTTPClient(), HubPublicURL+"/readyz"); code != 200 {
		t.Errorf("重新啟動後 /readyz = %d", code)
	}
	// 設定錯誤時重新啟動失敗，並指出原因。
	if err := w.AddHubConfig("site: { visibility: private }\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.RestartHubIfNeeded(ctx); err == nil || !strings.Contains(err.Error(), "site.visibility") {
		t.Errorf("錯誤的設定：err = %v", err)
	}
}

// M5 的參照只在請求端代入；預期結果的 JSON 用 Subst，不展開（steps.md 4.30）。
func TestSubstRequest(t *testing.T) {
	w := newWorld(t)
	w.Refs["token:reader"] = "nst_abc"
	w.SetHubRef("version", "raw", "v-123")
	w.SetHubRef("user", "alice", "u-1")
	w.SetHubRef("share", "last", "s-9")
	w.SetHubRef("takedown", "last", "t-2")
	w.SetHubRef("report", "last", "r-3")
	in := "GET /api/v1/versions/{version:raw}?u={user:alice}&s={share:last}&t={takedown:last}&r={report:last}&k={token:reader}&x={version:missing}"
	want := "GET /api/v1/versions/v-123?u=u-1&s=s-9&t=t-2&r=r-3&k=nst_abc&x={version:missing}"
	if got := w.SubstRequest(in); got != want {
		t.Errorf("SubstRequest = %q\n預期 %q", got, want)
	}
	if got := w.Subst(`{"id": "{version:raw}", "t": "{token:reader}"}`); got != `{"id": "{version:raw}", "t": "nst_abc"}` {
		t.Errorf("Subst（預期端）= %q", got)
	}
}
