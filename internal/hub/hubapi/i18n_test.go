package hubapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
)

// Hub 的錯誤訊息依 Accept-Language 選擇語言，code 與其他欄位不變（NFR-I18N-2、AUTH-R23）：Huma 的路由、
// middleware 寫出的錯誤與 Huma 之外的回應都一樣。
func TestErrorsFollowAcceptLanguage(t *testing.T) {
	st := &fakeStore{writableErr: errors.New("locked")}
	h := New(Deps{Store: st, Clock: clock.Real{}, Version: "test"})
	longID := "/api/v1/takedowns/" + strings.Repeat("a", 65)
	get := func(path, lang string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		h.ServeHTTP(rec, r)
		return rec
	}
	for _, c := range []struct {
		path, lang string
		status     int
		code, msg  string
	}{
		{"/readyz", "", 503, CodeNotReady, "服務尚未就緒：database 無法使用。"},
		{"/readyz", "en", 503, CodeNotReady, "The service is not ready: database unavailable."},
		{"/readyz", "zh-CN", 503, CodeNotReady, "服务尚未就绪：database 无法使用。"},
		// Huma 的驗證錯誤：message 帶第一個錯誤的位置與原因
		{longID, "en", 422, "invalid_request", "The request content is invalid: path.id must have a length of at most 64."},
		{longID, "", 422, "invalid_request", "請求內容不正確：path.id 長度最多是 64。"},
		{"/api/v1/me", "en", 401, "unauthorized", "Sign in to Hub first."},
	} {
		rec := get(c.path, c.lang)
		m := decode(t, rec)
		if rec.Code != c.status || m["code"] != c.code || m["message"] != c.msg {
			t.Errorf("GET %s（%q）= %d %s，預期 %d %s %q", c.path, c.lang, rec.Code, rec.Body, c.status, c.code, c.msg)
		}
	}
	// 其他欄位不隨語言改變
	zh, en := decode(t, get(longID, "")), decode(t, get(longID, "en"))
	if zh["details"] == nil || !reflect.DeepEqual(zh["details"], en["details"]) {
		t.Errorf("details 隨語言改變：%v / %v", zh, en)
	}
	if rd := decode(t, get("/readyz", "en")); !reflect.DeepEqual(rd["failed"], []any{"database"}) {
		t.Errorf("failed = %v", rd["failed"])
	}
	// Huma 之外：panic 轉成的 500
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Accept-Language", "en")
	recoverMiddleware(nilLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })).ServeHTTP(rec, r)
	if m := decode(t, rec); m["code"] != "internal_error" || strings.ContainsFunc(m["message"].(string), isHan) {
		t.Errorf("panic（en）= %s", rec.Body)
	}
}

func isHan(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }
