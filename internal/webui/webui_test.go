package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	webclient "github.com/cxyfer/notesteme/web/client"
)

// mockDist 模擬 Vite 的建置產物：index.html 只用 <script type="module" src> 與 <link>。
var mockDist = fstest.MapFS{
	"index.html": {Data: []byte(`<!doctype html>
<html lang="zh-Hant">
  <head>
    <meta charset="UTF-8" />
    <script type="module" crossorigin src="/assets/index-Bx1.js"></script>
    <link rel="modulepreload" crossorigin href="/assets/vendor-C2.js">
    <link rel="stylesheet" crossorigin href="/assets/index-D3.css">
  </head>
  <body><div id="root"></div></body>
</html>`)},
	"assets/index-Bx1.js":  {Data: []byte(`export{}`)},
	"assets/index-D3.css":  {Data: []byte(`body{}`)},
	"favicon.svg":          {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
	"assets/sub/nested.js": {Data: []byte(`1`)},
	"assets/dir/.keep":     {Data: nil},
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestSecurityHeadersOnHTML(t *testing.T) {
	for name, h := range map[string]*Handler{"build": New(mockDist), "placeholder": New(fstest.MapFS{})} {
		for _, p := range []string{"/", "/library", "/items/abc", "/index.html"} {
			rec := get(h, "GET", p)
			if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Errorf("%s GET %s = %d %q", name, p, rec.Code, rec.Header().Get("Content-Type"))
			}
			hd := rec.Header()
			for k, want := range map[string]string{
				"Content-Security-Policy": CSP,
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "same-origin",
				"X-Frame-Options":         "DENY",
				"Permissions-Policy":      PermissionsPolicy,
				"Cache-Control":           "no-cache",
			} {
				if hd.Get(k) != want {
					t.Errorf("%s GET %s：%s = %q，預期 %q", name, p, k, hd.Get(k), want)
				}
			}
		}
	}
}

// CSP 的內容符合 NFR-SEC-1 的原文。
func TestCSPDirectives(t *testing.T) {
	d := map[string]string{}
	for _, part := range strings.Split(CSP, ";") {
		f := strings.Fields(part)
		d[f[0]] = strings.Join(f[1:], " ")
	}
	checks := map[string]string{
		"default-src":     "'self'",
		"script-src":      "'self'",
		"object-src":      "'none'",
		"frame-ancestors": "'none'",
		"base-uri":        "'self'",
		"connect-src":     "'self'",
	}
	for k, v := range checks {
		if d[k] != v {
			t.Errorf("%s = %q，預期 %q", k, d[k], v)
		}
	}
	if strings.Contains(d["script-src"], "unsafe") {
		t.Error("script-src 不可有 unsafe-inline／unsafe-eval")
	}
	for _, host := range strings.Fields(d["frame-src"]) {
		if !strings.Contains(host, "youtube") && !strings.Contains(host, "bilibili") {
			t.Errorf("frame-src 只能開放 YouTube 與 Bilibili：%s", host)
		}
	}
}

func TestPlaceholderWhenNotBuilt(t *testing.T) {
	h := New(fstest.MapFS{".gitkeep": {}})
	if h.Built() {
		t.Fatal("沒有 index.html 時 Built() 應為 false")
	}
	rec := get(h, "GET", "/library")
	if !strings.Contains(rec.Body.String(), "notesteme-ui-placeholder") {
		t.Errorf("沒有建置時應回 Placeholder：%s", rec.Body)
	}
	// .gitkeep 不是資源，也不應被當成檔案提供以外的東西；有副檔名的不存在路徑 404。
	if rec := get(h, "GET", "/assets/index-x.js"); rec.Code != 404 {
		t.Errorf("GET /assets/index-x.js = %d，預期 404", rec.Code)
	}
}

func TestAssetsCaching(t *testing.T) {
	h := New(mockDist)
	rec := get(h, "GET", "/assets/index-Bx1.js")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("hash 資源 = %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec := get(h, "GET", "/favicon.svg"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("根目錄的檔案 = %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "HEAD", "/assets/index-Bx1.js"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d，body %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestNotFoundNeverFallsBackForFiles(t *testing.T) {
	h := New(mockDist)
	for _, p := range []string{"/assets/missing.js", "/assets/sub", "/assets/dir", "/assets/missing", "/favicon.ico", "/robots.txt", "/x/y.png"} {
		rec := get(h, "GET", p)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("GET %s = %d，預期 404 且不是 index.html", p, rec.Code)
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("GET %s：404 也要帶安全標頭", p)
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := New(mockDist)
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		rec := get(h, m, "/library")
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s /library = %d Allow=%q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

// 不是正規形式的路徑轉到正規形式；讀檔不會離開 fsys。
func TestPathNormalization(t *testing.T) {
	h := New(mockDist)
	cases := map[string]string{
		"/library/":                 "/library",
		"//library":                 "/library",
		"/a/../library?x=1":         "/library?x=1",
		"/assets/../index.html":     "/index.html",
		"/%2e%2e/%2e%2e/etc/passwd": "/etc/passwd",
		"/assets//index-Bx1.js":     "/assets/index-Bx1.js",
	}
	for in, want := range cases {
		rec := get(h, "GET", in)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Errorf("GET %s = %d Location=%q，預期 301 %q", in, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	for _, p := range []string{"/a\\..\\..\\x", "/a%00b"} {
		if rec := get(h, "GET", p); rec.Code != 404 {
			t.Errorf("GET %q = %d，預期 404", p, rec.Code)
		}
	}
}

var inlineScript = regexp.MustCompile(`(?is)<script\b([^>]*)>`)

// assertNoInlineScript 確認 HTML 沒有不帶 src 的 <script>（CSP 的 script-src 'self' 會擋掉它）。
func assertNoInlineScript(t *testing.T, name string, html []byte) {
	t.Helper()
	for _, m := range inlineScript.FindAllSubmatch(html, -1) {
		if !regexp.MustCompile(`(?i)\ssrc\s*=`).Match(m[1]) {
			t.Errorf("%s 有 inline <script%s>：CSP 不允許", name, m[1])
		}
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).Match(html) {
		t.Errorf("%s 有 inline 事件處理器（on*=）：CSP 不允許", name)
	}
}

func TestNoInlineScript(t *testing.T) {
	assertNoInlineScript(t, "Placeholder", []byte(Placeholder))
	assertNoInlineScript(t, "模擬建置產物", mockDist["index.html"].Data)
	// 有真正的建置產物時（npm run build 之後）也檢查它。
	if b, err := fs.ReadFile(webclient.FS(), "index.html"); err == nil {
		assertNoInlineScript(t, "web/client/dist/index.html", b)
	} else {
		t.Log("web/client/dist 沒有建置產物，只檢查 Placeholder 與模擬產物")
	}
	// 偵測器本身要能抓到 inline script。
	if !inlineScript.Match([]byte(`<script>alert(1)</script>`)) {
		t.Fatal("偵測器失效")
	}
}
