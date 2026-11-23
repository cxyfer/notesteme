package hubclient

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

const testToken = "nsh_test"

func staticToken(tok string) TokenFunc {
	return func(context.Context) (string, bool, error) { return tok, tok != "", nil }
}

func newTest(t *testing.T, h http.HandlerFunc) (*Client, *clock.Fake) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	return New(srv.URL+"/", staticToken(testToken), srv.Client(), clk), clk
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %T %v，預期 *Error", err, err)
	}
	return e
}

func TestNotConfigured(t *testing.T) {
	var c *Client
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nil client：%v", err)
	}
	c = New("", staticToken(testToken), http.DefaultClient, clock.Real{})
	if ok, _ := c.Configured(context.Background()); ok {
		t.Error("沒有網址時不算有設定")
	}
	c = New("https://hub.invalid", staticToken(""), http.DefaultClient, clock.Real{})
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("沒有 token：%v", err)
	}
	if c.URL() != "https://hub.invalid" {
		t.Error(c.URL())
	}
}

func TestMeSendsBearerAndFiltersPermissions(t *testing.T) {
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.URL.Path != "/api/v1/me" {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"id":"u1","display_name":"a\u0007b","permissions":["read","root","upload"]}`)
	})
	m, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.DisplayName != "ab" || strings.Join(m.Permissions, ",") != "read,upload" {
		t.Errorf("me = %+v", m)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		header string
		code   string
		http   int
		msg    string
		reason string
	}{
		{503, ``, "", CodeUnavailable, 502, "無法連線到 Hub（HTTP 503）。請稍後再試。", "HTTP 503"},
		{401, ``, "", CodeUnauthorized, 502, "Hub token 無效或已過期。請在 Hub 建立新的 token，再到設定頁更新。", "token 無效或權限不足"},
		{403, `{"required_permission":"upload"}`, "", CodeForbidden, 502, "Hub token 沒有「upload」權限。請在 Hub 建立有這個權限的 token。", "token 無效或權限不足"},
		{403, `{"required_permission":"<script>"}`, "", CodeForbidden, 502, "Hub 拒絕了這個請求。請確認 Hub 帳號沒有被停用，以及 token 的權限。", "token 無效或權限不足"},
		{429, `{"retry_after_s":30}`, "", CodeRateLimited, 502, "Hub 暫時拒絕請求（太頻繁），請在 30 秒後再試。", "HTTP 429"},
		{429, `{"retry_after_s":-5}`, "12", CodeRateLimited, 502, "Hub 暫時拒絕請求（太頻繁），請在 12 秒後再試。", "HTTP 429"},
		{429, `{"retry_after_s":99999999}`, "x", CodeRateLimited, 502, "Hub 暫時拒絕請求（太頻繁），請在 60 秒後再試。", "HTTP 429"},
		{404, ``, "", CodeNotFound, 404, "Hub 上找不到這個版本，可能已被移除或你沒有權限查看。", "HTTP 404"},
		{410, ``, "", CodeNotFound, 404, "Hub 上找不到這個版本，可能已被移除或你沒有權限查看。", "HTTP 410"},
		{422, `{"code":"license_invalid","message":"不支援\u0000的授權。","errors":[{"path":"license","message":"x"}]}`, "", "license_invalid", 422, "不支援的授權。", "HTTP 422"},
		{422, `{"code":"Bad Code!","message":""}`, "", "invalid_request", 422, "Hub 拒絕了這個請求。", "HTTP 422"},
		{418, ``, "", CodeUnavailable, 502, "無法連線到 Hub（HTTP 418）。請稍後再試。", "HTTP 418"},
	}
	for _, tc := range cases {
		c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
			if tc.header != "" {
				w.Header().Set("Retry-After", tc.header)
			}
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := c.Me(context.Background())
		e := asErr(t, err)
		if e.Code() != tc.code || e.HTTPStatus() != tc.http || e.Error() != tc.msg || e.PrecheckReason() != tc.reason {
			t.Errorf("%d %s：code=%s http=%d msg=%q reason=%q", tc.status, tc.body, e.Code(), e.HTTPStatus(), e.Error(), e.PrecheckReason())
		}
		if strings.Contains(e.Error(), testToken) {
			t.Error("訊息不得含 token")
		}
	}
}

func TestDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "https://evil.invalid/steal", http.StatusFound)
	})
	_, err := c.Me(context.Background())
	if e := asErr(t, err); e.Reason != "HTTP 302" || hits.Load() != 1 {
		t.Errorf("err = %v hits=%d", e, hits.Load())
	}
}

func TestConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	c := New(u, staticToken(testToken), http.DefaultClient, clock.NewFake(time.Unix(0, 0)))
	e := asErr(t, func() error { _, err := c.Me(context.Background()); return err }())
	if e.Kind != KindUnavailable || e.Reason != "連線失敗" || e.PrecheckReason() != "連線失敗" {
		t.Errorf("e = %+v", e)
	}
}

// 逾時以注入的 clock 計算：Hub 不回應時，fake clock 推進到逾時之後請求就會結束。
func TestLookupTimesOutOnTheInjectedClock(t *testing.T) {
	release := make(chan struct{})
	c, clk := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	errc := make(chan error, 1)
	go func() { _, err := c.Lookup(context.Background(), "youtube", "abcd"); errc <- err }()
	for clk.PendingTimers() == 0 {
		time.Sleep(time.Millisecond)
	}
	clk.Advance(LookupTimeout - time.Millisecond)
	select {
	case err := <-errc:
		t.Fatalf("還沒到逾時就結束：%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(time.Millisecond)
	e := asErr(t, <-errc)
	if !e.Timeout || e.Reason != "連線逾時" || e.Code() != CodeUnavailable {
		t.Errorf("e = %+v", e)
	}
}

func TestCallerCancelIsNotAHubError(t *testing.T) {
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, err := c.Me(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestLookupFiltersUntrustedEntries(t *testing.T) {
	good := strings.Repeat("a", 64)
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "platform=youtube&prefix=aaaa" || r.Method != http.MethodGet {
			w.WriteHeader(400)
			return
		}
		_, _ = io.WriteString(w, `{"videos":[
			{"video_key_sha256":"`+good+`","versions":[
				{"id":"v1","variant":"source","lang":"en","sha256":"`+good+`","score":1},
				{"id":"../x","variant":"source","lang":"en","sha256":"`+good+`"},
				{"id":"v2","variant":"corrected","lang":"en","sha256":"`+good+`"},
				{"id":"v3","variant":"source","lang":"en","sha256":"short"},
				{"id":"v4","variant":"normalized","lang":"en us","sha256":"`+good+`"}]},
			{"video_key_sha256":"`+strings.Repeat("b", 64)+`","versions":[]},
			{"video_key_sha256":"nothex","versions":[]}]}`)
	})
	vs, err := c.Lookup(context.Background(), "youtube", "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || len(vs[0].Versions) != 1 || vs[0].Versions[0].ID != "v1" {
		t.Errorf("videos = %+v", vs)
	}
}

func TestLookupRejectsNonJSONAndOversize(t *testing.T) {
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "<html>") })
	if e := asErr(t, func() error { _, err := c.Lookup(context.Background(), "youtube", "aaaa"); return err }()); e.Kind != KindContentInvalid {
		t.Errorf("e = %+v", e)
	}
	c, _ = newTest(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"videos":"`+strings.Repeat("x", maxJSONBytes)+`"}`)
	})
	if e := asErr(t, func() error { _, err := c.Lookup(context.Background(), "youtube", "aaaa"); return err }()); e.Kind != KindContentInvalid || !strings.Contains(e.Detail, "上限") {
		t.Errorf("e = %+v", e)
	}
}

func versionJSON(over map[string]string) string {
	f := map[string]string{"id": `"v1"`, "platform": `"youtube"`, "external_id": `"abcdefghijk"`, "part": `null`,
		"sha256": `"` + strings.Repeat("c", 64) + `"`, "lang": `"en"`}
	for k, v := range over {
		f[k] = v
	}
	return `{"id":` + f["id"] + `,"video":{"platform":` + f["platform"] + `,"external_id":` + f["external_id"] + `,"part":` + f["part"] +
		`},"kind":"transcript","variant":"source","lang":` + f["lang"] + `,"sha256":` + f["sha256"] +
		`,"transcribe_model":"groq/whisper","model_verified":true,"uploaders":[{"display_name":"alice‮"}]}`
}

func TestVersionValidatesFields(t *testing.T) {
	body := versionJSON(nil)
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/versions/v1" {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, body)
	})
	v, err := c.Version(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Uploader != "alice" || v.TranscribeModel != "groq/whisper" || v.Video.ExternalID != "abcdefghijk" {
		t.Errorf("v = %+v", v)
	}
	for name, over := range map[string]map[string]string{
		"id 不同":        {"id": `"v2"`},
		"本機檔案":         {"platform": `"local"`},
		"網址當 ID":       {"external_id": `"https://evil/x"`},
		"YouTube 有分P":  {"part": `2`},
		"sha256 不合法":   {"sha256": `"zz"`},
		"lang 不合法":     {"lang": `"../.."`},
		"Bilibili 的分P": {"platform": `"bilibili"`, "external_id": `"BV1hs4y1c7mD"`, "part": `0`},
	} {
		body = versionJSON(over)
		if e := asErr(t, func() error { _, err := c.Version(context.Background(), "v1"); return err }()); e.Kind != KindContentInvalid {
			t.Errorf("%s：e = %+v", name, e)
		}
	}
	body = versionJSON(map[string]string{"platform": `"bilibili"`, "external_id": `"BV1hs4y1c7mD"`, "part": `1`})
	if v, err := c.Version(context.Background(), "v1"); err != nil || *v.Video.Part != 1 {
		t.Errorf("Bilibili 分P：%v %+v", err, v)
	}
	if e := asErr(t, func() error { _, err := c.Version(context.Background(), "../me"); return err }()); e.Kind != KindNotFound {
		t.Errorf("路徑注入：%+v", e)
	}
}

func TestContentChecksTheHash(t *testing.T) {
	content := []byte(`{"lang":"en","variant":"source","speakers":[],"cues":[]}`)
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) })
	if _, err := c.Content(context.Background(), "v1", SHA256Hex(content), 0); err != nil {
		t.Fatal(err)
	}
	e := asErr(t, func() error { _, err := c.Content(context.Background(), "v1", strings.Repeat("0", 64), 0); return err }())
	if e.Code() != CodeIntegrity || e.HTTPStatus() != 502 || e.PrecheckReason() != "內容驗證失敗" {
		t.Errorf("e = %+v", e)
	}
	if e := asErr(t, func() error { _, err := c.Content(context.Background(), "a/b", "", 0); return err }()); e.Kind != KindNotFound {
		t.Errorf("e = %+v", e)
	}
}

func TestShareSendsMultipart(t *testing.T) {
	var gotAttest, gotVis string
	var gotBundle []byte
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "bundle":
				gotBundle = b
			case "attest":
				gotAttest = string(b)
			case "visibility":
				gotVis = string(b)
			}
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"share_id":"s1","versions":[{"id":"v1","kind":"transcript","variant":"corrected","lang":"en","deduplicated":true},{"id":"../bad"}]}`)
	})
	r, err := c.Share(context.Background(), []byte("zip"), "private")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBundle) != "zip" || gotAttest != "true" || gotVis != "private" {
		t.Errorf("multipart：%q %q %q", gotBundle, gotAttest, gotVis)
	}
	if r.ShareID != "s1" || len(r.Versions) != 1 || !r.Versions[0].Deduplicated {
		t.Errorf("r = %+v", r)
	}
	c, _ = newTest(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); _, _ = io.WriteString(w, `{"share_id":""}`) })
	if _, err := c.Share(context.Background(), nil, ""); err == nil {
		t.Error("沒有 share_id 要失敗")
	}
}
