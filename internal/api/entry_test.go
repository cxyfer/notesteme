package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ENTRY-R6：登入回應的 next 經過開放轉址檢查；沒有 next 時是 "/"。
func TestLoginNext(t *testing.T) {
	s := newTestServer(t)
	long := "/add?url=" + strings.Repeat("x", 2048)
	cases := []struct{ body, want string }{
		{`{"password":"pw"}`, "/"},
		{`{"password":"pw","next":"/add?url=https%3A%2F%2Fyoutu.be%2Fabc"}`, "/add?url=https%3A%2F%2Fyoutu.be%2Fabc"},
		{`{"password":"pw","next":"https://evil.example.com/"}`, "/"},
		{`{"password":"pw","next":"//evil.example.com/x"}`, "/"},
		{`{"password":"pw","next":"/\\evil.example.com/x"}`, "/"},
		{`{"password":"pw","next":"javascript:alert(1)"}`, "/"},
		{`{"password":"pw","next":"` + long + `"}`, "/"},
	}
	for _, c := range cases {
		rec := s.do("POST", "/api/auth/login", c.body, nil)
		if rec.Code != 200 {
			t.Fatalf("%s → %d %s", c.body, rec.Code, rec.Body)
		}
		var out struct{ Next *string }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Next == nil || *out.Next != c.want {
			t.Errorf("%s → %s，want next %q", c.body, rec.Body, c.want)
		}
	}
	// 密碼錯誤時不透露 next
	rec := s.do("POST", "/api/auth/login", `{"password":"nope","next":"/add?url=x"}`, nil)
	if rec.Code != 401 || strings.Contains(rec.Body.String(), "next") || strings.Contains(rec.Body.String(), "/add") {
		t.Errorf("密碼錯誤：%d %s", rec.Code, rec.Body)
	}
}

// 頁面路由掛在 TCP 的 handler 上：/add 只認 session cookie，不認 bearer token。
func TestEntryPagesWired(t *testing.T) {
	s := newUITestServer(t, false)
	rec := s.do("GET", "/add?url=x", "", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=%2Fadd%3Furl%3Dx" {
		t.Fatalf("未登入 /add = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	session, csrf := s.login(t)
	rec = s.do("GET", "/add?url=x", "", withCookies(session, csrf, ""))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("已登入 /add = %d %v", rec.Code, rec.Header())
	}
	for _, p := range []string{"/manifest.webmanifest", "/sw.js", "/login"} {
		if rec := s.do("GET", p, "", nil); rec.Code != 200 {
			t.Errorf("GET %s = %d，不需要登入", p, rec.Code)
		}
	}
	// 本機 socket（CLI）沒有 UI，也沒有頁面路由。
	local := newUITestServer(t, true)
	if rec := local.do("GET", "/manifest.webmanifest", "", nil); rec.Code == 200 {
		t.Errorf("本機 socket 不應提供 manifest")
	}
}
