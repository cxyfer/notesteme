package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 請求本體超過上限時回 422 cookie_too_large（不是 huma 預設的 413），而且不儲存、不恢復。
func TestPostCookiesOversizedBodyIs422(t *testing.T) {
	r := newCookieRig(t)
	for name, size := range map[string]int{
		"超過內容上限但在請求上限內": cookies64KiBPlus,
		"超過請求上限":        2 << 20,
		"剛好超過請求上限":      maxCookieBodyBytes + 1,
	} {
		body := `{"platform":"youtube","content":"SID=` + strings.Repeat("x", size) + `"}`
		rec := r.post(t, body)
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != CodeCookieTooLarge {
			t.Errorf("%s：status = %d, body = %.200s", name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "xxxx") {
			t.Errorf("%s：回應含 cookie 的值", name)
		}
	}
	if len(r.resumer.tokens) != 0 {
		t.Errorf("不該恢復步驟：%v", r.resumer.tokens)
	}
}

// 沒有 Content-Length（chunked）的超大請求也要擋下，而且是在讀到上限就停止，不把整個本體讀進記憶體。
func TestPostCookiesOversizedChunkedBodyIs422(t *testing.T) {
	r := newCookieRig(t)
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, `{"platform":"youtube","content":"SID=`)
		chunk := strings.Repeat("x", 64<<10)
		for i := 0; i < 64; i++ { // 最多 4 MiB；讀端在上限就會停止
			if _, err := io.WriteString(pw, chunk); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	defer pr.Close()
	req := httptest.NewRequest("POST", "/api/cookies", pr)
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.local.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != CodeCookieTooLarge {
		t.Fatalf("status = %d, body = %.200s", rec.Code, rec.Body)
	}
}

// 正常大小的請求不受影響（64 KiB 的內容經 JSON 跳脫後仍在請求上限內）。
func TestPostCookiesBodyLimitLeavesRoomForMaxContent(t *testing.T) {
	r := newCookieRig(t)
	rec := r.post(t, `{"platform":"youtube","content":"SID=`+strings.Repeat("\\u0041", 10<<10)+`"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %.200s", rec.Code, rec.Body)
	}
}

// 超過請求上限時，其他路由仍沿用 huma 的預設（不受 cookie 上限影響）。
func TestOtherRoutesKeepDefaultBodyLimit(t *testing.T) {
	r := newCookieRig(t)
	session, csrf := r.login(t)
	rec := r.do("POST", "/api/items", `{"url":"`+strings.Repeat("x", 2<<20)+`"}`, withCookies(session, csrf, csrf))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d", rec.Code)
	}
}

// cookies64KiBPlus 是略大於內容上限（64 KiB）的大小。
const cookies64KiBPlus = 65<<10 + 1
