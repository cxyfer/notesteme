package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
)

func bundleReq(r *itemsRig, path string, data []byte, field string) (*http.Response, []byte) {
	r.t.Helper()
	body, ct := multipartImport(r.t, field, data)
	resp := r.ownerMultipart(path, body, ct)
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// GET /api/items/:id/bundle 與 POST /api/bundles/import（BND-R1、R3、R9、R10）。
func TestBundleExportImport(t *testing.T) {
	r := newItemsRigAPI(t, func(d *Deps) { d.BundleImport = config.BundleImport{MaxUploadBytes: 64 << 10} })
	id := r.ingestOne(abcURL)

	rec := r.owner("GET", "/api/items/yt:abc123xxxxx/bundle", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="yt-abc123xxxxx.notesteme.zip"` {
		t.Fatalf("匯出 = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	data := rec.Body.Bytes()
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		t.Fatal("不是 zip")
	}

	resp, body := bundleReq(r, "/api/bundles/import", data, "file")
	if e := decodeError(t, body); resp.StatusCode != 409 || e.Code != "bundle_item_exists" || e.ItemID != id {
		t.Errorf("重複 = %d %s", resp.StatusCode, body)
	}
	resp, body = bundleReq(r, "/api/bundles/import?on_existing=add_versions", data, "file")
	var res BundleImportResult
	if resp.StatusCode != 201 || json.Unmarshal(body, &res) != nil || res.Item.ID != id || res.Item.Ref != "yt:abc123xxxxx" || res.Imported != 0 {
		t.Errorf("add_versions = %d %s", resp.StatusCode, body)
	}
	resp, body = bundleReq(r, "/api/bundles/import?on_existing=merge", data, "file")
	if e := decodeError(t, body); resp.StatusCode != 422 || e.Code != CodeInvalidInput || len(e.Errors) != 1 || e.Errors[0].Path != "on_existing" {
		t.Errorf("on_existing = %d %s", resp.StatusCode, body)
	}

	// 不通過驗證：422、error_class permanent、entry；manifest 的錯誤有 errors[].path。
	resp, body = bundleReq(r, "/api/bundles/import", []byte("not a zip"), "file")
	if e := decodeError(t, body); resp.StatusCode != 422 || e.Code != "bundle_invalid" || e.ErrorClass != "permanent" || e.Entry != "" ||
		e.Message != "這不是 Notesteme bundle（無法以 zip 讀取）。" {
		t.Errorf("不是 zip = %d %s", resp.StatusCode, body)
	}

	// 缺少 file、不是 multipart。
	resp, body = bundleReq(r, "/api/bundles/import", data, "other")
	if e := decodeError(t, body); resp.StatusCode != 422 || len(e.Errors) != 1 || e.Errors[0].Path != "file" {
		t.Errorf("沒有 file = %d %s", resp.StatusCode, body)
	}
	rec = r.owner("POST", "/api/bundles/import", `{}`)
	if rec.Code != 422 {
		t.Errorf("JSON 本體 = %d %s", rec.Code, rec.Body)
	}

	// 超過上傳上限：檔案略大（在 multipart 的餘裕內）與 Content-Length 宣告過大都是 413。
	resp, body = bundleReq(r, "/api/bundles/import", bytes.Repeat([]byte("a"), 64<<10+1), "file")
	if e := decodeError(t, body); resp.StatusCode != 413 || e.Code != CodeBundleTooLarge || e.Message != "bundle 超過 64 KiB 的上傳上限。" {
		t.Errorf("略大 = %d %s", resp.StatusCode, body)
	}
	rec = r.do("POST", "/api/bundles/import", "x", func(req *http.Request) {
		withCookies(r.sess, r.csrf, r.csrf)(req)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		req.ContentLength = 50 << 20
	})
	if rec.Code != 413 {
		t.Errorf("Content-Length = %d %s", rec.Code, rec.Body)
	}
	// 有 session 但沒有 CSRF 標頭：跨站上傳被擋下（驗證 middleware 在 intercept 之前）。
	csrfBody, csrfCT := multipartImport(t, "file", data)
	rec = r.do("POST", "/api/bundles/import", csrfBody, func(req *http.Request) {
		withCookies(r.sess, r.csrf, "")(req)
		req.Header.Set("Content-Type", csrfCT)
	})
	if e := errBody(t, rec); rec.Code != 403 || e.Code != CodeCSRF {
		t.Errorf("沒有 CSRF = %d %s", rec.Code, rec.Body)
	}
	// 沒有登入：不讀本體，回 401。
	rec = r.do("POST", "/api/bundles/import", "x", nil)
	if rec.Code != 401 {
		t.Errorf("未登入 = %d", rec.Code)
	}

	// 垃圾桶中的影音：匯出 404 in_trash。
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code/100 != 2 {
		t.Fatalf("刪除 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("GET", "/api/items/"+id+"/bundle", "")
	if e := errBody(t, rec); rec.Code != 404 || e.Code != CodeInTrash {
		t.Errorf("垃圾桶 = %d %s", rec.Code, rec.Body)
	}
	resp, body = bundleReq(r, "/api/bundles/import", data, "file")
	if e := decodeError(t, body); resp.StatusCode != 409 || e.Code != "bundle_item_in_trash" || !strings.Contains(e.Message, "垃圾桶") {
		t.Errorf("垃圾桶匯入 = %d %s", resp.StatusCode, body)
	}
	// 垃圾桶中的影音不回傳 ID（回應中完全沒有 item_id，也沒有影音的 ID），帶 add_versions 也一樣。
	for _, path := range []string{"/api/bundles/import", "/api/bundles/import?on_existing=add_versions"} {
		resp, body = bundleReq(r, path, data, "file")
		if resp.StatusCode != 409 || bytes.Contains(body, []byte("item_id")) || bytes.Contains(body, []byte(id)) {
			t.Errorf("垃圾桶匯入洩漏 ID：%s = %d %s", path, resp.StatusCode, body)
		}
	}
	rec = r.owner("GET", "/api/items/nope/bundle", "")
	if rec.Code != 404 {
		t.Errorf("不存在 = %d", rec.Code)
	}
}
