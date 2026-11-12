package api

import "testing"

// Pipeline 沒有設定是設定錯誤（500），不是「功能還沒做」（501）。
func TestResolveWithoutPipelineIsServerError(t *testing.T) {
	s := newTestServer(t)
	session, csrf := s.login(t)
	rec := s.do("POST", "/api/resolve", `{"url":"https://youtu.be/abc123"}`, withCookies(session, csrf, csrf))
	if rec.Code != 500 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if e := decode[Error](t, rec); e.Code != CodeInternal {
		t.Errorf("code = %q", e.Code)
	}
}
