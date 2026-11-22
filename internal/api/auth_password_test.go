package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestChangePassword(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	other, otherCSRF := s.login(t)

	// 沒有 CSRF 標頭 → 403
	if rec := s.do("POST", "/api/auth/password", `{"current_password":"pw","new_password":"n"}`, withCookies(sess, csrf, "")); rec.Code != 403 {
		t.Errorf("no csrf status = %d", rec.Code)
	}
	// 目前的密碼錯誤 → 422 invalid_password（不是 401，前端不會導到登入頁）
	rec := s.do("POST", "/api/auth/password", `{"current_password":"bad","new_password":"n"}`, withCookies(sess, csrf, csrf))
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 422 || e.Code != CodeInvalidPass {
		t.Fatalf("wrong current = %d %s", rec.Code, rec.Body)
	}
	// 新密碼是空的 → 422 invalid_request
	rec = s.do("POST", "/api/auth/password", `{"current_password":"pw","new_password":""}`, withCookies(sess, csrf, csrf))
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 422 || e.Code != CodeInvalidInput {
		t.Fatalf("empty new = %d %s", rec.Code, rec.Body)
	}
	// 成功
	rec = s.do("POST", "/api/auth/password", `{"current_password":"pw","new_password":"pw2"}`, withCookies(sess, csrf, csrf))
	if rec.Code != 200 {
		t.Fatalf("change = %d %s", rec.Code, rec.Body)
	}
	// 目前的 session 仍有效，其他的被撤銷
	if rec := s.do("GET", "/api/items", "", withCookies(sess, csrf, "")); rec.Code == 401 {
		t.Errorf("current session revoked")
	}
	if rec := s.do("GET", "/api/items", "", withCookies(other, otherCSRF, "")); rec.Code != 401 {
		t.Errorf("other session status = %d, want 401", rec.Code)
	}
	if rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil); rec.Code != 401 {
		t.Errorf("old password login = %d", rec.Code)
	}
	if rec := s.do("POST", "/api/auth/login", `{"password":"pw2"}`, nil); rec.Code != 200 {
		t.Errorf("new password login = %d", rec.Code)
	}
}

func TestChangePasswordLockout(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	for i := 0; i < 5; i++ {
		s.do("POST", "/api/auth/password", `{"current_password":"bad","new_password":"n"}`, withCookies(sess, csrf, csrf))
	}
	rec := s.do("POST", "/api/auth/password", `{"current_password":"pw","new_password":"n"}`, withCookies(sess, csrf, csrf))
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 429 || e.Code != CodeLoginLocked || e.RetryAfterS == nil {
		t.Fatalf("locked = %d %s", rec.Code, rec.Body)
	}
	// 登入也被同一個鎖擋下
	if rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil); rec.Code != 429 {
		t.Errorf("login while locked = %d", rec.Code)
	}
}

func TestChangePasswordRefusesToken(t *testing.T) {
	s := newTestServer(t)
	tok, err := s.deps.Auth.CreateToken(context.Background(), "admin-bot", []string{"admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := s.do("POST", "/api/auth/password", `{"current_password":"pw","new_password":"n"}`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok.Token)
	})
	if rec.Code != 403 {
		t.Fatalf("token status = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil); rec.Code != 200 {
		t.Errorf("password changed by token: login = %d", rec.Code)
	}
}
