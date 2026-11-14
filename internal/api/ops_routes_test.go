package api

import (
	"encoding/json"
	"testing"
)

// OPS 切片的路由已註冊（不是 404），實作前一律回 501 not_implemented。
func TestOpsRoutesAnswer501(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	for _, c := range [][2]string{
		{"GET", "/api/system"},
		{"POST", "/api/providers"},
		{"GET", "/api/providers/whisperx"},
		{"POST", "/api/tools/yt-dlp/update"},
		{"POST", "/api/tools/yt-dlp/rollback"},
		{"POST", "/api/backup"},
	} {
		rec := s.do(c[0], c[1], "", withCookies(sess, csrf, csrf))
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 501 || e.Code != CodeNotImplemented {
			t.Errorf("%s %s = %d %s，預期 501 not_implemented", c[0], c[1], rec.Code, rec.Body)
		}
	}
}
