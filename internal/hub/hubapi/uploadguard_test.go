package hubapi

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/websession"
)

// 安全修補 B：失敗的分享也計入每小時的次數（否則壞掉的 bundle 可以無限重送，每次都讓 Hub 驗證一遍）。
func TestFailedSharesCountTowardsTheHourlyLimit(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.RateLimits.SharesPerHour = 2 })
	bad := makeBundle(t, bundleOpts{item: map[string]any{"availability": "private"}}, src("a"))
	expect(t, "第 1 次失敗", e.share("alice", bad, nil), 422, "not_shareable")
	e.clk.Advance(10 * time.Minute)
	expect(t, "第 2 次失敗", e.share("alice", []byte("not a zip"), nil), 422, "bundle_invalid")
	rec := e.share("alice", makeBundle(t, bundleOpts{}, src("b")), nil)
	expect(t, "兩次失敗之後", rec, 429, CodeRateLimited)
	if rec.Header().Get("Retry-After") != "3000" {
		t.Errorf("Retry-After = %q（最早的失敗滿一小時的剩餘秒數）", rec.Header().Get("Retry-After"))
	}
	// 被限流的請求本身不計入：最早的失敗滿一小時後可以再分享。
	e.clk.Advance(50*time.Minute + time.Second)
	e.mustShare("alice", makeBundle(t, bundleOpts{}, src("b")), nil)
	// 別的帳號不受影響。
	e.grant("bob", hubconfig.GroupContributors)
	e.mustShare("bob", makeBundle(t, bundleOpts{}, src("c")), nil)
}

func TestCountsAsFailure(t *testing.T) {
	for status, want := range map[int]bool{400: true, 409: true, 413: true, 422: true, 401: false, 403: false, 429: false, 500: false, 503: false} {
		if got := countsAsFailure(apierr.New(status, "x", "x")); got != want {
			t.Errorf("%d = %v", status, got)
		}
	}
}

// 並行名額：兩個還在上傳的分享（兩個帳號各一個）佔住名額時，再一個直接回 429，不讀本體。
// 全域名額滿了而帳號還有名額的情況見 TestUploadGuardReleaseIsIdempotent（測試環境只有兩個帳號）。
func TestShareConcurrencySlots(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	var wg sync.WaitGroup
	var pipes []*io.PipeWriter
	for _, who := range []string{"alice", "bob"}[:maxConcurrentShares] {
		pr, pw := io.Pipe()
		pipes = append(pipes, pw)
		var head bytes.Buffer
		mw := multipart.NewWriter(&head)
		_ = mw.WriteField("attest", "true")
		r := httptest.NewRequest("POST", "/api/v1/shares", pr)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		tok := e.toks[who]
		r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: tok})
		r.AddCookie(&http.Cookie{Name: hubauth.CSRFCookie, Value: websession.CSRFFor(tok)})
		r.Header.Set(hubauth.CSRFHeader, websession.CSRFFor(tok))
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.h.ServeHTTP(httptest.NewRecorder(), r)
		}()
		// Write 在 handler 開始讀本體時才返回：此時名額已經取得。
		if _, err := pw.Write(head.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.share("bob", makeBundle(t, bundleOpts{}, src("a")), nil)
	expect(t, "名額已滿", rec, 429, CodeRateLimited)
	if rec.Header().Get("Retry-After") != "5" || decode(t, rec)["message"] != "Hub 正在處理其他上傳，請在 5 秒後再試。" {
		t.Errorf("回應 = %q %s", rec.Header().Get("Retry-After"), rec.Body)
	}
	for _, pw := range pipes {
		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	}
	wg.Wait()
	// 名額歸還後可以分享。
	e.mustShare("alice", makeBundle(t, bundleOpts{}, src("a")), nil)
}

func TestUploadGuardPrunesOldFailures(t *testing.T) {
	e := newRepoEnv(t, nil)
	g := newUploadGuard(e.clk)
	g.recordFailure("u")
	e.clk.Advance(30 * time.Minute)
	g.recordFailure("u")
	if n, _ := g.recentFailures("u"); n != 2 {
		t.Fatalf("n = %d", n)
	}
	e.clk.Advance(31 * time.Minute)
	if n, _ := g.recentFailures("u"); n != 1 {
		t.Fatalf("一小時之後 n = %d", n)
	}
	e.clk.Advance(time.Hour)
	if n, _ := g.recentFailures("u"); n != 0 || len(g.failures) != 0 {
		t.Fatalf("全部過期之後 n = %d, map = %d", n, len(g.failures))
	}
}

// 每個帳號同時只有 1 個名額：一個帳號的慢速上傳不能佔住所有名額，別的帳號仍然可以分享。
func TestShareSlotsPerAccount(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	pr, pw := io.Pipe()
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	_ = mw.WriteField("attest", "true")
	r := httptest.NewRequest("POST", "/api/v1/shares", pr)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	tok := e.toks["alice"]
	r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: tok})
	r.AddCookie(&http.Cookie{Name: hubauth.CSRFCookie, Value: websession.CSRFFor(tok)})
	r.Header.Set(hubauth.CSRFHeader, websession.CSRFFor(tok))
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.h.ServeHTTP(httptest.NewRecorder(), r)
	}()
	if _, err := pw.Write(head.Bytes()); err != nil {
		t.Fatal(err)
	}
	expect(t, "同一個帳號的第二個分享", e.share("alice", makeBundle(t, bundleOpts{}, src("a")), nil), 429, CodeRateLimited)
	e.mustShare("bob", makeBundle(t, bundleOpts{}, src("b")), nil)
	_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	<-done
	e.mustShare("alice", makeBundle(t, bundleOpts{}, src("a")), nil)
}

func TestUploadGuardReleaseIsIdempotent(t *testing.T) {
	g := newUploadGuard(nil)
	rel, ok := g.acquire("u")
	if !ok {
		t.Fatal("第一個名額")
	}
	if _, ok := g.acquire("u"); ok {
		t.Fatal("同一個帳號的第二個名額")
	}
	rel()
	rel()
	if len(g.slots) != 0 || len(g.active) != 0 {
		t.Fatalf("slots = %d, active = %v", len(g.slots), g.active)
	}
	a, _ := g.acquire("a")
	b, _ := g.acquire("b")
	if _, ok := g.acquire("c"); ok {
		t.Fatal("全域名額滿了")
	}
	a()
	b()
}

func grayJPEG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, side, side))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 1}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// 安全修補 B 的第 1 項（Hub 端）：分享時圖片只做結構檢查，不完整解碼；超過 16M 像素與 progressive 一律拒絕。
// 原本 8192×8192（約 1 KB 的 deflate）會被完整解碼（0.3 秒、64 MiB）並接受。
func TestShareRejectsJPEGBombsWithoutDecoding(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.Bundles.MaxRatio = 1 << 20 })
	big := grayJPEG(t, 8192)
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	rec := e.share("alice", makeBundle(t, bundleOpts{extra: map[string][]byte{"artifacts/bomb.jpg": big}}, src("a")), nil)
	el := time.Since(start)
	runtime.ReadMemStats(&m1)
	expect(t, "8192×8192", rec, 422, "bundle_invalid")
	if msg, _ := decode(t, rec)["message"].(string); !strings.Contains(msg, "JPEG") {
		t.Errorf("message = %q", msg)
	}
	alloc := m1.TotalAlloc - m0.TotalAlloc
	t.Logf("8192×8192 的分享：%v，配置 %d KiB", el, alloc>>10)
	if alloc > 32<<20 {
		t.Errorf("配置了 %d bytes：不應完整解碼（一張就要 64 MiB）", alloc)
	}

	prog := grayJPEG(t, 64)
	i := bytes.Index(prog, []byte{0xFF, 0xC0})
	prog[i+1] = 0xC2
	expect(t, "progressive", e.share("alice", makeBundle(t, bundleOpts{extra: map[string][]byte{"artifacts/p.jpg": prog}}, src("b")), nil), 422, "bundle_invalid")

	// 一般的圖片照常接受。
	e.mustShare("alice", makeBundle(t, bundleOpts{extra: map[string][]byte{"artifacts/ok.jpg": grayJPEG(t, 640)}}, src("c")), nil)
}
