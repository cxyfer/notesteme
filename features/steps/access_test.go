package steps

import (
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func TestBearerHeaderAndTokenSetupPreservesState(t *testing.T) {
	s := newTestScenario(t)
	if _, err := s.bearerHeader("missing"); err == nil {
		t.Fatal("未建立 token 時應該失敗")
	}
	if err := s.clientSendsWithToken("GET /api/items", "missing"); err == nil {
		t.Fatal("未建立 token 時應該失敗")
	}
	if err := s.clientSendsWithTokenAnd("POST /api/items", "missing", &godog.DocString{Content: `{}`}); err == nil {
		t.Fatal("未建立 token 時應該失敗")
	}

	s.World.Refs["token:agent"] = "nst_testtoken123"
	h, err := s.bearerHeader("agent")
	if err != nil {
		t.Fatalf("bearerHeader：%v", err)
	}
	if got := h["Authorization"]; got != "Bearer nst_testtoken123" {
		t.Errorf("Authorization = %q，預期 Bearer nst_testtoken123", got)
	}

	// aTokenWithScopes 應成功建立 token，且不覆寫 s.last 與 s.cmd。
	sentinelLast := &response{status: 200}
	sentinelCmd := &cliRun{out: "keep"}
	s.last = sentinelLast
	s.cmd = sentinelCmd
	if err := s.aTokenWithScopes("ci", "read"); err != nil {
		t.Fatalf("aTokenWithScopes：%v", err)
	}
	if s.World.Refs["token:ci"] == "" || s.lastCreatedToken != s.World.Refs["token:ci"] {
		t.Errorf("建立成功時應記錄 Refs 與 lastCreatedToken：ref=%q last=%q", s.World.Refs["token:ci"], s.lastCreatedToken)
	}
	if s.last != sentinelLast || s.cmd != sentinelCmd {
		t.Error("aTokenWithScopes 不應該覆寫 s.last 或 s.cmd")
	}
}

func TestClientSendsWithTokenSendsBearerWithoutSession(t *testing.T) {
	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	s.World.Refs["token:bad"] = "nst_invalid_token_for_test"
	if err := s.clientSendsWithToken("GET /api/items", "bad"); err != nil {
		t.Fatalf("clientSendsWithToken：%v", err)
	}
	if s.last == nil || s.last.status != 401 {
		t.Fatalf("無效 token 應回 401，得到 %+v", s.last)
	}
	if err := s.clientSendsWithTokenAnd("PUT /api/settings/services", "bad", &godog.DocString{
		Content: `{"providers":{"groq":{"api_key":"gsk_from_bearer_test"}}}`,
	}); err != nil {
		t.Fatalf("clientSendsWithTokenAnd：%v", err)
	}
	if s.last == nil || s.last.status != 401 {
		t.Fatalf("無效 token 應回 401，得到 %+v", s.last)
	}
	if !s.apiKeySecrets["gsk_from_bearer_test"] {
		t.Errorf("noteRequest 應該記錄 PUT /api/settings/services 的 api_key：%v", s.apiKeySecrets)
	}
	if _, err := s.World.Log.Write([]byte("ok\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.checkLogs(); err != nil {
		t.Fatalf("沒有外洩時 checkLogs 不應失敗：%v", err)
	}
	_, _ = s.World.Log.Write([]byte("leaked gsk_from_bearer_test\n"))
	if err := s.checkLogs(); err == nil || !strings.Contains(err.Error(), "gsk_from_bearer_test") {
		t.Errorf("log 外洩 api_key 應該被 checkLogs 攔截：%v", err)
	}
}
