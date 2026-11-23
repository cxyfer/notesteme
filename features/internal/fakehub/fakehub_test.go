package fakehub

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "nsh_test"

func do(t *testing.T, srv *httptest.Server, method, path string, body io.Reader, contentType, tok string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func TestRejectsMissingOrWrongToken(t *testing.T) {
	h := New(token)
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, tok := range []string{"", "nsh_other"} {
		if code, _, _ := do(t, srv, "GET", "/api/v1/me", nil, "", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: got %d, want 401", tok, code)
		}
	}
	if code, _, _ := do(t, srv, "GET", "/api/v1/me", nil, "", token); code != http.StatusOK {
		t.Fatalf("valid token: got %d", code)
	}
}

func TestMissingPermissionNamesIt(t *testing.T) {
	h := New(token)
	h.SetPermissions("read")
	srv := httptest.NewServer(h)
	defer srv.Close()
	code, body, _ := do(t, srv, "GET", "/api/v1/lookup?platform=youtube&prefix=abcd", nil, "", token)
	if code != http.StatusForbidden || !strings.Contains(string(body), `"required_permission":"regenerate"`) {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestLookupMatchesPrefixAndOnlyRawVersions(t *testing.T) {
	h := New(token)
	h.AddVersion(Version{Platform: "youtube", ExternalID: "hshr05xxxxx", Kind: "transcript", Variant: "source", Lang: "en", Content: []byte(`{"cues":[]}`)})
	h.AddVersion(Version{Platform: "youtube", ExternalID: "hshr05xxxxx", Kind: "transcript", Variant: "corrected", Lang: "en", Content: []byte(`{"x":1}`)})
	srv := httptest.NewServer(h)
	defer srv.Close()

	key := VideoKeySHA256("youtube", "hshr05xxxxx", "")
	if key[:8] != "b44dc4ea" {
		t.Fatalf("video key hash changed: %s", key)
	}
	code, body, _ := do(t, srv, "GET", "/api/v1/lookup?platform=youtube&prefix="+key[:4], nil, "", token)
	if code != http.StatusOK {
		t.Fatalf("got %d %s", code, body)
	}
	var got struct {
		Videos []struct {
			Key      string `json:"video_key_sha256"`
			Versions []struct{ Variant string }
		}
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Videos) != 1 || got.Videos[0].Key != key || len(got.Videos[0].Versions) != 1 || got.Videos[0].Versions[0].Variant != "source" {
		t.Fatalf("unexpected lookup result: %s", body)
	}
	if strings.Contains(string(body), "hshr05xxxxx") {
		t.Fatalf("lookup leaked the video ID: %s", body)
	}
	if h.Lookups() != 1 {
		t.Fatalf("Lookups = %d", h.Lookups())
	}
	if h.SawString("hshr05xxxxx") {
		t.Fatal("request should not contain the video ID")
	}

	if code, _, _ := do(t, srv, "GET", "/api/v1/lookup?platform=youtube&prefix="+key[:8], nil, "", token); code != http.StatusUnprocessableEntity {
		t.Fatalf("long prefix: got %d", code)
	}
}

func TestContentMatchesHashUnlessTampered(t *testing.T) {
	h := New(token)
	content := []byte(`{"cues":[{"start":"00:00:01.000","end":"00:00:02.000","text":"hi"}]}`)
	id := h.AddVersion(Version{Platform: "youtube", ExternalID: "a", Kind: "transcript", Variant: "source", Lang: "en", Content: content})
	srv := httptest.NewServer(h)
	defer srv.Close()

	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	code, body, hdr := do(t, srv, "GET", "/api/v1/versions/"+id+"/content", nil, "", token)
	if code != http.StatusOK || sum(body) != hdr.Get("X-Notesteme-SHA256") {
		t.Fatalf("content should match its hash: %d", code)
	}
	h.Tamper(id)
	_, body, hdr = do(t, srv, "GET", "/api/v1/versions/"+id+"/content", nil, "", token)
	if sum(body) == hdr.Get("X-Notesteme-SHA256") {
		t.Fatal("tampered content should not match its hash")
	}
	if !json.Valid(body) {
		t.Fatalf("tampered content should stay valid JSON: %s", body)
	}
	if code, _, _ := do(t, srv, "GET", "/api/v1/versions/missing", nil, "", token); code != http.StatusNotFound {
		t.Fatalf("missing version: got %d", code)
	}
}

func bundle(t *testing.T, manifest string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{"manifest.json": manifest}
	for k, v := range extra {
		files[k] = v
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartBody(t *testing.T, b []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("bundle", "talk.notesteme.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestShareKeepsTheBundleOutOfTheRequestLog(t *testing.T) {
	h := New(token)
	srv := httptest.NewServer(h)
	defer srv.Close()
	manifest := `{"bundle_version":0,"item":{"platform":"youtube","external_id":"hshr01xxxxx"},"license":"CC-BY-4.0",
		"artifacts":[{"kind":"transcript","variant":"corrected","lang":"en","sha256":"abc","path":"artifacts/a.json"}]}`
	body, ct := multipartBody(t, bundle(t, manifest, map[string]string{"artifacts/a.json": `{"cues":[]}`}), map[string]string{"attest": "true"})
	code, resp, _ := do(t, srv, "POST", "/api/v1/shares", body, ct, token)
	if code != http.StatusCreated || !strings.Contains(string(resp), `"variant":"corrected"`) {
		t.Fatalf("got %d %s", code, resp)
	}
	shares := h.Shares()
	if len(shares) != 1 || shares[0].Fields["attest"] != "true" || shares[0].Files["artifacts/a.json"] == nil {
		t.Fatalf("share not recorded: %+v", shares)
	}
	if item, _ := shares[0].Manifest["item"].(map[string]any); item["external_id"] != "hshr01xxxxx" {
		t.Fatalf("manifest not parsed: %v", shares[0].Manifest)
	}
	// bundle 本來就含影片 ID，不算洩漏；其他請求的紀錄才算。
	if h.SawString("hshr01xxxxx") {
		t.Fatal("the bundle should not be part of the request log")
	}

	body, ct = multipartBody(t, bundle(t, manifest, nil), nil)
	if code, _, _ := do(t, srv, "POST", "/api/v1/shares", body, ct, token); code != http.StatusUnprocessableEntity {
		t.Fatalf("share without attest: got %d", code)
	}
}

func TestFailAndHang(t *testing.T) {
	h := New(token)
	srv := httptest.NewServer(h)
	defer srv.Close()
	h.Fail(http.StatusTooManyRequests)
	code, body, hdr := do(t, srv, "GET", "/api/v1/me", nil, "", token)
	if code != http.StatusTooManyRequests || hdr.Get("Retry-After") != "30" || !strings.Contains(string(body), `"retry_after_s":30`) {
		t.Fatalf("got %d %s", code, body)
	}
	h.Fail(0)
	h.Hang()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if _, err := srv.Client().Do(req); err == nil {
		t.Fatal("a hanging Hub should make the request time out")
	}
	if len(h.Requests()) != 2 {
		t.Fatalf("requests = %d", len(h.Requests()))
	}
}

func TestRoutesAreServed(t *testing.T) {
	h := New(token)
	id := h.AddVersion(Version{Platform: "youtube", ExternalID: "a", Kind: "transcript", Variant: "source", Content: []byte(`{}`)})
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, route := range Routes {
		method, path, _ := strings.Cut(route, " ")
		if method != http.MethodGet {
			continue
		}
		path = strings.ReplaceAll(path, "{id}", id)
		if path == "/api/v1/lookup" {
			path += "?platform=youtube&prefix=0000"
		}
		if code, body, _ := do(t, srv, method, path, nil, "", token); code != http.StatusOK {
			t.Fatalf("%s: got %d %s", route, code, body)
		}
	}
}

func TestOnHangAndReplaceContent(t *testing.T) {
	h := New(token)
	id := h.AddVersion(Version{Platform: "bilibili", ExternalID: "BV1hs4y1c7mD", Part: "1", Kind: "transcript", Variant: "source", Content: []byte(`{}`)})
	if err := h.ReplaceContent(id, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := h.ReplaceContent("missing", nil); err == nil {
		t.Fatal("不存在的版本要失敗")
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	code, body, _ := do(t, srv, "GET", "/api/v1/versions/"+id, nil, "", token)
	if code != http.StatusOK || !strings.Contains(string(body), `"part":1`) || !strings.Contains(string(body), VideoKeySHA256("x", "", "")[:0]) {
		t.Fatalf("got %d %s", code, body)
	}
	v, _ := h.version(id)
	if string(v.Content) != `{"a":1}` || !strings.Contains(string(body), v.SHA256) {
		t.Fatalf("sha256 要依新內容重算：%s", body)
	}
	called := make(chan struct{}, 1)
	h.OnHang(func(ctx context.Context) { called <- struct{}{} })
	h.Hang()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	_, _ = srv.Client().Do(req)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("OnHang 要被呼叫")
	}
}
