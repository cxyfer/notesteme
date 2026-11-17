package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestParseTokenDuration(t *testing.T) {
	valid := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"1.5d", 36 * time.Hour},
		{".5d", 12 * time.Hour},
		{"1d0h", 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"45s", 45 * time.Second},
	}
	for _, tc := range valid {
		got, err := parseTokenDuration(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parseTokenDuration(%q) = (%v, %v)，預期 (%v, nil)", tc.in, got, err, tc.want)
		}
	}

	invalid := []string{
		"", "   ", "0s", "0d", "-1h", "-30d", "d", "1d2d", "1d-2h", "1d+2h", "30dabc", "soon", "200000d", "106751d25h",
	}
	for _, in := range invalid {
		if got, err := parseTokenDuration(in); err == nil {
			t.Errorf("parseTokenDuration(%q) = (%v, nil)，預期失敗", in, got)
		}
	}
}

func TestTokensUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺少子命令":            {"tokens"},
		"不認得的子命令":          {"tokens", "bogus"},
		"create 缺少參數":      {"tokens", "create"},
		"create 缺 scopes":  {"tokens", "create", "--name", "agent"},
		"create 缺 name":    {"tokens", "create", "--scopes", "read"},
		"create 空白 name":   {"tokens", "create", "--name", "   ", "--scopes", "read"},
		"create 控制字元":      {"tokens", "create", "--name", "bad\nname", "--scopes", "read"},
		"create 空 scopes":  {"tokens", "create", "--name", "a", "--scopes", ""},
		"create 無效 scope":  {"tokens", "create", "--name", "a", "--scopes", "read,bogus"},
		"create 空 expires": {"tokens", "create", "--name", "a", "--scopes", "read", "--expires", ""},
		"create 0 expires": {"tokens", "create", "--name", "a", "--scopes", "read", "--expires", "0d"},
		"create 負 expires": {"tokens", "create", "--name", "a", "--scopes", "read", "--expires", "-5d"},
		"create 壞 expires": {"tokens", "create", "--name", "a", "--scopes", "read", "--expires", "soon"},
		"create 多餘位置參數":    {"tokens", "create", "--name", "a", "--scopes", "read", "extra"},
		"ls 多餘位置參數":        {"tokens", "ls", "extra"},
		"revoke 缺少目標":      {"tokens", "revoke"},
		"revoke 空白目標":      {"tokens", "revoke", "  "},
		"revoke 太多參數":      {"tokens", "revoke", "a", "b"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2 且無模式（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func TestTokensCreateClientMode(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tokens" {
			writeJSON(w, 404, `{"code":"not_found","message":"no"}`)
			return
		}
		writeJSON(w, 201, `{"id":"tok_1","name":"agent","prefix":"nst_abcd","scopes":["read","agent"],"expires_at":"2026-12-12T10:00:00Z","created_at":"2026-11-12T10:00:00Z","token":"nst_abcd1234567890"}`)
	})

	// --json：原樣輸出 API 回應，且送出正確的請求格式。
	res, out, errOut := run(t, app.Env{}, "tokens", "create", "--name", "agent", "--scopes", "read, agent, read", "--expires", "30d", "--json", "--server", url, "--token", "admin-tok")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if api.auth[0] != "Bearer admin-tok" {
		t.Errorf("Authorization = %q", api.auth[0])
	}
	var reqBody struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresIn string   `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(api.bodies[0]), &reqBody); err != nil {
		t.Fatalf("請求 JSON 解析失敗：%v（%s）", err, api.bodies[0])
	}
	if reqBody.Name != "agent" || strings.Join(reqBody.Scopes, ",") != "read,agent" || reqBody.ExpiresIn != "30d" {
		t.Errorf("請求內容 = %+v", reqBody)
	}
	var respObj map[string]any
	if err := json.Unmarshal([]byte(out), &respObj); err != nil || respObj["token"] != "nst_abcd1234567890" || respObj["name"] != "agent" {
		t.Errorf("--json 輸出 = %q（%v）", out, err)
	}

	// 文字模式：顯示 token、名稱、權限與到期時間；沒有 --expires 時不送 expires_in。
	res, out, errOut = run(t, app.Env{}, "tokens", "create", "--name", "agent", "--scopes", "read", "--server", url, "--token", "admin-tok")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut)
	}
	if strings.Contains(api.bodies[1], "expires_in") {
		t.Errorf("未指定 --expires 不應送出 expires_in：%s", api.bodies[1])
	}
	for _, want := range []string{"agent", "tok_1", "read,agent", "nst_abcd1234567890"} {
		if !strings.Contains(out, want) {
			t.Errorf("文字輸出缺少 %q：%q", want, out)
		}
	}
}

func TestTokensLsAndRevokeClientMode(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tokens":
			writeJSON(w, 200, `{"tokens":[`+
				`{"id":"t1","name":"active","prefix":"nst_1111","scopes":["read"],"created_at":"2026-11-12T09:00:00Z"},`+
				`{"id":"t2","name":"expired","prefix":"nst_2222","scopes":["ingest"],"expires_at":"2026-11-12T09:30:00Z","created_at":"2026-11-12T08:00:00Z"},`+
				`{"id":"t3","name":"revoked","prefix":"nst_3333","scopes":["admin"],"created_at":"2026-11-12T07:00:00Z","revoked_at":"2026-11-12T09:45:00Z"}`+
				`]}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/tokens/old":
			writeJSON(w, 200, `{"revoked":true}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/tokens/missing":
			writeJSON(w, 404, `{"code":"not_found","message":"找不到指定的 token。"}`)
		default:
			writeJSON(w, 404, `{"code":"not_found","message":"no"}`)
		}
	})

	env, _ := testEnv(t)
	res, out, errOut := run(t, env, "tokens", "ls", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || !strings.Contains(out, `"tokens"`) {
		t.Fatalf("ls --json：%+v %q %s", res, out, errOut)
	}

	res, out, errOut = run(t, env, "tokens", "ls", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("ls 文字：%+v %s", res, errOut)
	}
	for _, want := range []string{"active", "有效", "永不過期", "expired", "已到期", "revoked", "已撤銷"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls 輸出缺少 %q：%q", want, out)
		}
	}

	res, out, errOut = run(t, env, "tokens", "revoke", "old", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || strings.TrimSpace(out) != `{"revoked":true}` {
		t.Fatalf("revoke --json：%+v %q %s", res, out, errOut)
	}
	res, out, errOut = run(t, env, "tokens", "revoke", "old", "--server", url, "--token", "t")
	if res.Code != 0 || !strings.Contains(out, "old") {
		t.Fatalf("revoke 文字：%+v %q %s", res, out, errOut)
	}
	if api.count("DELETE /api/tokens/old") != 2 {
		t.Errorf("請求紀錄 = %v", api.requests)
	}

	res, _, _ = run(t, env, "tokens", "revoke", "missing", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" {
		t.Errorf("找不到時應回傳 1 / not_found：%+v", res)
	}
}
