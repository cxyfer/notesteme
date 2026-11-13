package fakesource

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// 使用 repo 內真實的最小 fixture。
func fixturesDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func upload(t *testing.T, h http.Handler, item string, duration int, padding int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", "whisper-large-v3-turbo")
	fw, _ := mw.CreateFormFile("file", "audio.m4a")
	_, _ = io.WriteString(fw, faketool.MediaMarker(item, duration))
	_, _ = io.WriteString(fw, strings.Repeat("x", padding))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOpenAIReturnsFixtureAndCountsCalls(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("groq", "yt:nocap1xxxxx", "groq-en.json")
	h := s.OpenAIHandler("groq")

	rec := upload(t, h, "nocap1xxxxx", 6, 0)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"language": "english"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	upload(t, h, "nocap1xxxxx", 6, 0)
	if s.Calls("groq") != 2 || s.Calls("supadata") != 0 {
		t.Fatalf("calls groq=%d supadata=%d", s.Calls("groq"), s.Calls("supadata"))
	}
}

func TestOpenAINothingAndUnscripted(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("groq", "yt:axxxxxxxxxx", "groq-en.json")
	s.Nothing("groq", "yt:axxxxxxxxxx") // 後設定的覆蓋先前的 Returns
	h := s.OpenAIHandler("groq")
	rec := upload(t, h, "axxxxxxxxxx", 1, 0)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"segments":[]`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := upload(t, h, "otherxxxxxx", 1, 0); rec.Code != http.StatusNotFound {
		t.Fatalf("unscripted code=%d", rec.Code)
	}
}

func TestOpenAISizeLimit(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("groq", "yt:axxxxxxxxxx", "groq-en.json")
	s.AcceptsUpTo("groq", 1000)
	h := s.OpenAIHandler("groq")
	if rec := upload(t, h, "axxxxxxxxxx", 1, 500); rec.Code != 200 {
		t.Fatalf("limit 內 code=%d", rec.Code)
	}
	if rec := upload(t, h, "axxxxxxxxxx", 1, 5000); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超過上限 code=%d", rec.Code)
	}
	if s.Calls("groq") != 2 {
		t.Fatalf("calls = %d", s.Calls("groq"))
	}
}

func TestOpenAIBadRequests(t *testing.T) {
	s := New(fixturesDir(t))
	h := s.OpenAIHandler("groq")

	for _, c := range []struct {
		method, path, ctype, body string
		want                      int
	}{
		{"GET", "/openai/v1/audio/transcriptions", "", "", http.StatusMethodNotAllowed},
		{"POST", "/other", "", "", http.StatusNotFound},
		{"POST", "/openai/v1/audio/transcriptions", "application/json", "{}", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		if c.ctype != "" {
			req.Header.Set("Content-Type", c.ctype)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s: code=%d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}

	// 上傳的不是假媒體檔。
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "a.m4a")
	_, _ = io.WriteString(fw, "not fake media")
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/openai/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}

	// 沒有 file 欄位。
	body.Reset()
	mw = multipart.NewWriter(&body)
	_ = mw.WriteField("model", "m")
	_ = mw.Close()
	req = httptest.NewRequest("POST", "/openai/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing file code=%d", rec.Code)
	}
}

func supadataGet(h http.Handler, videoURL string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/transcript?url="+url.QueryEscape(videoURL), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSupadataReturnsNothingAndStatus(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("supadata", "yt:abc123xxxxx", "supadata-en.json")
	s.Nothing("supadata", "yt:empty1xxxxx")
	s.Returns("supadata", "yt:flaky1xxxxx", "http-503")
	s.Returns("supadata", "bili:BV1ai411c7mD/p1", "supadata-en.json")
	h := s.SupadataHandler()

	rec := supadataGet(h, "https://www.youtube.com/watch?v=abc123xxxxx")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "PostgreSQL is a relational database.") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := supadataGet(h, "https://youtu.be/empty1xxxxx"); rec.Code != 404 {
		t.Fatalf("nothing code=%d", rec.Code)
	}
	if rec := supadataGet(h, "https://www.youtube.com/watch?v=flaky1xxxxx"); rec.Code != 503 {
		t.Fatalf("http-503 code=%d", rec.Code)
	}
	if rec := supadataGet(h, "https://www.bilibili.com/video/BV1ai411c7mD?p=1"); rec.Code != 200 {
		t.Fatalf("bili code=%d", rec.Code)
	}
	if rec := supadataGet(h, "https://www.youtube.com/watch?v=unknownxxxx"); rec.Code != 404 {
		t.Fatalf("unscripted code=%d", rec.Code)
	}
	if rec := supadataGet(h, "https://example.com/x"); rec.Code != 400 {
		t.Fatalf("bad url code=%d", rec.Code)
	}
	if s.Calls("supadata") != 6 {
		t.Fatalf("calls = %d", s.Calls("supadata"))
	}
	// 缺少的 fixture 檔案回 500 並說明原因。
	s.Returns("supadata", "yt:missingxxxx", "does-not-exist.json")
	if rec := supadataGet(h, "https://youtu.be/missingxxxx"); rec.Code != 500 || !strings.Contains(rec.Body.String(), "does-not-exist.json") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	// 非 GET 與其他路徑。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/transcript", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("path code=%d", rec.Code)
	}
}

func TestRequiresCookie(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("supadata", "yt:abc123xxxxx", "supadata-en.json")
	s.RequiresCookie("supadata", "session")
	if s.RequiredCookie("supadata") != "session" || s.RequiredCookie("groq") != "" {
		t.Fatal("RequiredCookie 不符")
	}
	h := s.SupadataHandler()
	if rec := supadataGet(h, "https://youtu.be/abc123xxxxx"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("沒有 cookie 應為 401: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/transcript?url="+url.QueryEscape("https://youtu.be/abc123xxxxx"), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "v"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("有 cookie 應為 200: %d", rec.Code)
	}
}

func TestItemKey(t *testing.T) {
	for ref, want := range map[string]string{
		"yt:abc123xxxxx":       "abc123xxxxx",
		"bili:BV1ai411c7mD/p1": "BV1ai411c7mD",
		"bili:BV1ai411c7mD/p2": "BV1ai411c7mD/p2",
		"local:meetup.mp4":     "local:meetup.mp4",
		"pod:show/ep-43":       "pod:show/ep-43",
	} {
		if got := ItemKey(ref); got != want {
			t.Errorf("ItemKey(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestRequiresAPIKey(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("supadata", "yt:abc123xxxxx", "supadata-en.json")
	s.RequiresAPIKey("supadata", "k-123")
	h := s.SupadataHandler()
	if rec := supadataGet(h, "https://youtu.be/abc123xxxxx"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("沒有 key 應為 401: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/transcript?url="+url.QueryEscape("https://youtu.be/abc123xxxxx"), nil)
	req.Header.Set("x-api-key", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("key 不符應為 401: %d", rec.Code)
	}
	// Supadata 只接受 x-api-key；Authorization: Bearer 不算數。
	req.Header.Del("x-api-key")
	req.Header.Set("Authorization", "Bearer k-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Bearer 不應被接受: %d", rec.Code)
	}
	req.Header.Del("Authorization")
	req.Header.Set("x-api-key", "k-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("key 正確應為 200: %d", rec.Code)
	}
	if s.Calls("supadata") != 4 {
		t.Fatalf("被拒絕的請求也要計入：%d", s.Calls("supadata"))
	}
}
