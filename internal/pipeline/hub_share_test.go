package pipeline

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/inbox/fakemedia"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// 分享（HSHR-R2～R6）：請求檢查、可分享的影音、衍生鏈上的 cookie_required、bundle 的內容，以及 Hub 的各種失敗回應。

var shareReq = HubShareRequest{License: "CC-BY-4.0", Attest: true}

// processedItem 跑完整條管線：有 source 與 normalized 兩個字幕（來源 youtube-manual）。
func (r *hubRig) processedItem() store.Item {
	r.t.Helper()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	return r.item(it.ID)
}

func (r *hubRig) artifact(it store.Item, variant string) store.Artifact {
	r.t.Helper()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	if err != nil {
		r.t.Fatal(err)
	}
	for _, a := range arts {
		if a.Variant == variant {
			return a
		}
	}
	r.t.Fatalf("沒有 %s 字幕", variant)
	return store.Artifact{}
}

func (r *hubRig) setProvenance(a store.Artifact, prov string) {
	r.t.Helper()
	if _, err := r.rawDB().Exec(`UPDATE artifacts SET provenance_json = ? WHERE id = ?`, prov, a.ID); err != nil {
		r.t.Fatal(err)
	}
}

// bundleOf 解出分享出去的 bundle 的 manifest 與檔案。
func bundleOf(t *testing.T, raw []byte) (map[string]any, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("bundle 不是 zip：%v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name], _ = io.ReadAll(rc)
		_ = rc.Close()
	}
	var m map[string]any
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		t.Fatal(err)
	}
	return m, files
}

// manifestArtifacts 回傳 manifest 中每個產出的「kind/variant」。
func manifestArtifacts(m map[string]any) []string {
	var out []string
	for _, a := range m["artifacts"].([]any) {
		am := a.(map[string]any)
		s, _ := am["kind"].(string)
		if v, ok := am["variant"].(string); ok {
			s += "/" + v
		}
		out = append(out, s)
	}
	return out
}

func TestShareToHubSendsWhitelistedBundleAndRecordsIt(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	// Hub 回傳的版本清單是不可信的輸入：不合法的 ID 被略過，文字清掉控制字元。
	stub.shareResp = `{"share_id":"share-9","versions":[
		{"id":"ver1","kind":"transcript","variant":"source","lang":"en","deduplicated":true},
		{"id":"../../etc/passwd","kind":"transcript","variant":"x","lang":"en"},
		{"id":"ver2","kind":"trans\u0007cript","variant":"normalized","lang":"en"}]}`
	r := newHubRig(t, stub)
	it := r.processedItem()
	req := shareReq
	req.Visibility = "private"

	got, err := r.p.ShareToHub(ctx, it.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.ShareID != "share-9" || len(got.Versions) != 2 || !got.Versions[0].Deduplicated || got.Versions[0].VersionID != "ver1" ||
		got.Versions[1].Kind != "transcript" || got.Versions[1].VersionID != "ver2" {
		t.Errorf("result = %+v", got)
	}

	reqs := stub.requests()
	if len(reqs) != 1 || reqs[0].Method != "POST" || reqs[0].Path != "/api/v1/shares" || reqs[0].Auth != "Bearer "+hubTestToken {
		t.Fatalf("requests = %+v", reqs)
	}
	if f := stub.lastForm(); f.Get("attest") != "true" || f.Get("visibility") != "private" {
		t.Errorf("form = %v", f)
	}
	m, files := bundleOf(t, stub.lastShare())
	// 預設：每個字幕變體（管線順序）；sponsor_segments 不在分享範圍。
	if want := []string{"transcript/source", "transcript/normalized"}; !slices.Equal(manifestArtifacts(m), want) {
		t.Errorf("產出 = %v，預期 %v", manifestArtifacts(m), want)
	}
	if m["license"] != "CC-BY-4.0" || m["created_by"].(map[string]any)["app_version"] != "test" {
		t.Errorf("manifest = %v", m)
	}
	if len(m["created_by"].(map[string]any)) != 1 {
		t.Errorf("created_by 只能有 app_version：%v", m["created_by"])
	}
	item := m["item"].(map[string]any)
	if item["platform"] != "youtube" || item["external_id"] != "abc123xxxxx" || item["title"] != "T" || item["availability"] != "public" || item["original_lang"] != "en" {
		t.Errorf("item = %v", item)
	}
	// 白名單：不含本機的 ID、網址、路徑。
	allowed := []string{"platform", "external_id", "part", "title", "channel", "published_at", "duration_s", "original_lang", "availability"}
	for k := range item {
		if !slices.Contains(allowed, k) {
			t.Errorf("item 含白名單以外的欄位 %q", k)
		}
	}
	for name, data := range files {
		if strings.Contains(string(data), r.dir) || strings.Contains(string(data), it.ID) {
			t.Errorf("%s 洩漏本機路徑或影音 ID", name)
		}
	}
	// 衍生鏈：normalized 的 parents_sha256 是 source 的內容雜湊。
	var normProv map[string]any
	for _, a := range m["artifacts"].([]any) {
		am := a.(map[string]any)
		if am["variant"] == "normalized" {
			normProv = am["provenance"].(map[string]any)
		}
	}
	srcSHA := r.artifact(it, "source").SHA256
	if ps, _ := normProv["parents_sha256"].([]any); len(ps) != 1 || ps[0] != srcSHA {
		t.Errorf("parents_sha256 = %v，預期 [%s]", normProv["parents_sha256"], srcSHA)
	}

	// 成功之後記入 hub_shares，網址是設定的 Hub。
	rows, err := r.p.ItemHubShares(ctx, it.ID)
	if err != nil || len(rows) != 1 || rows[0].ShareID != "share-9" || rows[0].HubURL != stub.srv.URL {
		t.Fatalf("hub_shares = %+v, %v", rows, err)
	}
	var vs []HubSharedVersion
	if err := json.Unmarshal([]byte(rows[0].VersionsJSON), &vs); err != nil || len(vs) != 2 {
		t.Errorf("versions_json = %q, %v", rows[0].VersionsJSON, err)
	}
}

func TestShareToHubWithoutVisibilityOmitsTheField(t *testing.T) {
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	if _, err := r.p.ShareToHub(context.Background(), it.ID, shareReq); err != nil {
		t.Fatal(err)
	}
	if f := stub.lastForm(); f.Has("visibility") {
		t.Errorf("沒有指定 visibility 時不送出：%v", f)
	}
}

func TestShareArtifactSelection(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	share := func(arts ...HubArtifactRef) ([]string, error) {
		req := shareReq
		req.Artifacts = arts
		before := stub.count("/api/v1/shares")
		_, err := r.p.ShareToHub(ctx, it.ID, req)
		if err != nil {
			if stub.count("/api/v1/shares") != before {
				t.Errorf("失敗時不該送出 bundle")
			}
			return nil, err
		}
		m, _ := bundleOf(t, stub.lastShare())
		return manifestArtifacts(m), nil
	}

	if got, err := share(HubArtifactRef{Kind: "transcript", Variant: "normalized", Lang: "en"}); err != nil || !slices.Equal(got, []string{"transcript/normalized"}) {
		t.Errorf("只分享 normalized：%v, %v", got, err)
	}
	// 重複與不同順序：依管線順序、去重。
	if got, err := share(
		HubArtifactRef{Kind: "transcript", Variant: "normalized"}, HubArtifactRef{Kind: "transcript", Variant: "source"},
		HubArtifactRef{Kind: "transcript", Variant: "normalized"}); err != nil || !slices.Equal(got, []string{"transcript/source", "transcript/normalized"}) {
		t.Errorf("順序與去重：%v, %v", got, err)
	}
	// 沒有指定變體與語言：第一個符合的字幕。
	if got, err := share(HubArtifactRef{Kind: "transcript"}); err != nil || len(got) != 1 {
		t.Errorf("只指定 kind：%v, %v", got, err)
	}

	for name, ref := range map[string]HubArtifactRef{
		"沒有的變體":  {Kind: "transcript", Variant: "translated", Lang: "en"},
		"沒有的語言":  {Kind: "transcript", Variant: "source", Lang: "ja"},
		"沒有的種類":  {Kind: "bogus"},
		"本機的產出":  {Kind: "sponsor_segments"},
		"沒有的章節":  {Kind: "chapters"},
		"含控制字元的": {Kind: "transcript", Variant: "evil\x1b[31m\n" + strings.Repeat("v", 200), Lang: "en"},
	} {
		_, err := share(ref)
		e := hubErrIs(t, err, 422, "artifact_not_found")
		if strings.ContainsAny(e.Msg, "\x1b\n") || len([]rune(e.Msg)) > 120 {
			t.Errorf("%s：訊息沒有清理或截斷：%q", name, e.Msg)
		}
	}
}

func TestShareToHubRejectsBeforeContactingTheHub(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()

	// R3 的請求檢查先於找影音：不存在的影音也回 422，而不是 not found。
	_, err := r.p.ShareToHub(ctx, "no-such-item", HubShareRequest{License: "CC0-1.0"})
	hubErrIs(t, err, 422, "attest_required")
	_, err = r.p.ShareToHub(ctx, "no-such-item", HubShareRequest{Attest: true})
	hubErrIs(t, err, 422, "invalid_request")
	_, err = r.p.ShareToHub(ctx, "no-such-item", HubShareRequest{Attest: true, License: "WTFPL"})
	hubErrIs(t, err, 422, "license_invalid")
	if _, err := r.p.ShareToHub(ctx, "no-such-item", shareReq); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}

	// 垃圾桶中的影音：InTrashError，不連線。
	r.trashDirect(it.ID, r.clk.Now())
	_, err = r.p.ShareToHub(ctx, it.ID, shareReq)
	var tr *InTrashError
	if !errors.As(err, &tr) || tr.ItemID != it.ID {
		t.Errorf("垃圾桶：%v", err)
	}
	if n := stub.total(); n != 0 {
		t.Errorf("被拒絕的請求不該連線：%d 個請求", n)
	}
}

func TestShareToHubNotConfigured(t *testing.T) {
	ctx := context.Background()
	for name, mk := range map[string]func(t *testing.T) *hubRig{
		"沒有 token": func(t *testing.T) *hubRig { return newHubRigOpts(t, newHubStub(t), hubRigOpts{}) },
		"沒有網址": func(t *testing.T) *hubRig {
			return newHubRigOpts(t, newHubStub(t), hubRigOpts{token: hubTestToken, noURL: true})
		},
	} {
		r := mk(t)
		it := r.processedItem()
		if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); !errors.Is(err, hubclient.ErrNotConfigured) {
			t.Errorf("%s：%v", name, err)
		}
		if r.stub.total() != 0 {
			t.Errorf("%s：不該連線", name)
		}
	}
	// Deps.Hub 為 nil。
	r := newRig(t)
	r.yt.info.Availability = "public"
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); !errors.Is(err, hubclient.ErrNotConfigured) {
		t.Errorf("Hub 為 nil：%v", err)
	}
	if _, err := r.p.ImportFromHub(ctx, HubImportRequest{Version: "v1"}); !errors.Is(err, hubclient.ErrNotConfigured) {
		t.Errorf("匯入時 Hub 為 nil：%v", err)
	}
}

func TestShareToHubRefusesNonPublicVideos(t *testing.T) {
	ctx := context.Background()
	for _, avail := range []string{"", "private", "needs_auth", "subscriber_only", "premium_only", "unlisted", "PUBLIC"} {
		stub := newHubStub(t)
		r := newHubRig(t, stub)
		r.yt.info.Availability = avail
		it := r.processedItem()
		_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
		e := hubErrIs(t, err, 422, "not_shareable")
		if !strings.Contains(e.Msg, "非公開") {
			t.Errorf("availability %q：訊息 = %q", avail, e.Msg)
		}
		if stub.total() != 0 {
			t.Errorf("availability %q：不該連線", avail)
		}
	}
}

func TestShareToHubRefusesLocalFiles(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stub := newHubStub(t)
	tool, _ := fakemedia.New(t)
	r := newHubRig(t, stub, func(d *Deps) {
		d.Media = tool
		d.Files = pathguard.New([]string{root}).With(filepath.Join(d.DataDir, "uploads"))
	})
	out, err := r.p.IngestLocal(ctx, LocalTarget{Key: "talk.mp4", SourcePath: filepath.Join(root, "talk.mp4"), SHA256: "aa", Origin: store.OriginInbox})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.p.ShareToHub(ctx, out.Item.ID, shareReq)
	e := hubErrIs(t, err, 422, "not_shareable")
	if !strings.Contains(e.Msg, "本機檔案") {
		t.Errorf("訊息 = %q", e.Msg)
	}
	if stub.total() != 0 {
		t.Error("本機檔案不該連線")
	}
}

func TestShareToHubNothingToShare(t *testing.T) {
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	_, err := r.p.ShareToHub(context.Background(), it.ID, shareReq)
	hubErrIs(t, err, 422, "nothing_to_share")
	if stub.total() != 0 {
		t.Error("沒有可分享的產出時不該連線")
	}
}

// 衍生鏈上有需要登入才拿得到的來源時不能分享（HSHR-R4、R7）。
func TestShareToHubRefusesCookieRequiredChain(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	src, norm := r.artifact(it, "source"), r.artifact(it, "normalized")

	// 來源紀錄寫著 cookie_required：預設分享、單獨分享 source 與只分享 normalized（要沿鏈往上看）都不行。
	r.setProvenance(src, `{"executor":"local","source":"youtube-manual","cookie_required":true}`)
	for name, arts := range map[string][]HubArtifactRef{
		"預設":            nil,
		"source":        {{Kind: "transcript", Variant: "source"}},
		"只有 normalized": {{Kind: "transcript", Variant: "normalized"}},
	} {
		req := shareReq
		req.Artifacts = arts
		_, err := r.p.ShareToHub(ctx, it.ID, req)
		e := hubErrIs(t, err, 422, "not_shareable")
		if !strings.Contains(e.Msg, "需要登入") {
			t.Errorf("%s：訊息 = %q", name, e.Msg)
		}
	}
	if stub.count("/api/v1/shares") != 0 {
		t.Fatal("需要登入的字幕不該送到 Hub")
	}

	// 缺 cookie_required 欄位的舊產出：依 source 推導。bilibili-ai 一定需要登入。
	r.setProvenance(src, `{"executor":"local","source":"bilibili-ai"}`)
	_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
	hubErrIs(t, err, 422, "not_shareable")
	// 明寫 cookie_required: false 也擋不住 bilibili-ai。
	r.setProvenance(src, `{"executor":"local","source":"bilibili-ai","cookie_required":false}`)
	_, err = r.p.ShareToHub(ctx, it.ID, shareReq)
	hubErrIs(t, err, 422, "not_shareable")
	if stub.count("/api/v1/shares") != 0 {
		t.Fatal("bilibili-ai 不該送到 Hub")
	}

	// 缺欄位、來源是公開影音的平台字幕：可以分享。
	r.setProvenance(src, `{"executor":"local","source":"youtube-manual"}`)
	if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); err != nil {
		t.Errorf("缺欄位的公開來源：%v", err)
	}
	_ = norm
}

// 來源是 Hub 的字幕：不再算需要登入，原始字幕不重複上傳，衍生版本帶上 hub_origin（HSHR-R5、R6）。
func TestShareToHubFromHubSourceCarriesOrigin(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()
	src := r.artifact(it, "source")
	sha := strings.Repeat("ab", 32)
	r.setProvenance(src, `{"executor":"hub","source":"hub","cookie_required":true,"hub_origin":{"url":"https://hub.example","version_id":"orig-1","sha256":"`+sha+`","uploader":"Alice"}}`)

	if _, err := r.p.ShareToHub(ctx, it.ID, shareReq); err != nil {
		t.Fatal(err)
	}
	m, _ := bundleOf(t, stub.lastShare())
	if want := []string{"transcript/normalized"}; !slices.Equal(manifestArtifacts(m), want) {
		t.Fatalf("產出 = %v，預期只有衍生版本 %v", manifestArtifacts(m), want)
	}
	prov := m["artifacts"].([]any)[0].(map[string]any)["provenance"].(map[string]any)
	if o, _ := prov["hub_origin"].(map[string]any); o["version_id"] != "orig-1" || o["sha256"] != sha {
		t.Errorf("hub_origin = %v", prov["hub_origin"])
	}

	// 明確要求 hub 來源的 source 也可以分享（不是 cookie_required）。
	req := shareReq
	req.Artifacts = []HubArtifactRef{{Kind: "transcript", Variant: "source"}}
	if _, err := r.p.ShareToHub(ctx, it.ID, req); err != nil {
		t.Errorf("明確指定 hub 來源的 source：%v", err)
	}
}

// Bilibili 與 Podcast 沒有 availability，空值視為公開（HSHR 待決問題 S10）：可以通過分享前的檢查。
func TestShareBilibiliTreatsEmptyAvailabilityAsPublic(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	fb := &fakeBili{}
	r := newHubRig(t, stub, func(d *Deps) { d.Bilibili = fb })
	fb.info.ID, fb.info.Title = "BV1xx411c7mD", "Part"
	fb.info.Tracks = []string{"zh-Hans"}
	it := r.ingest(biliP1, map[string]any{"language": map[string]any{"fallback": []any{}}}, StageTranscript)
	r.run()
	_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
	var he *HubError
	if errors.As(err, &he) {
		t.Fatalf("空的 availability 應該視為公開：%v", err)
	}
	if err != nil {
		t.Fatalf("Bilibili CC：%v", err)
	}
	m, _ := bundleOf(t, stub.lastShare())
	item := m["item"].(map[string]any)
	if item["platform"] != "bilibili" || item["external_id"] != "BV1xx411c7mD" || item["part"] != float64(1) || item["availability"] != "public" {
		t.Errorf("item = %v", item)
	}
}

// Bilibili 的 AI 字幕要 cookie 才拿得到：不能分享，也不連線。
func TestShareBilibiliAICaptionIsNotShareable(t *testing.T) {
	fb := &fakeBili{}
	r := newHubRig(t, newHubStub(t), func(d *Deps) { d.Bilibili = fb })
	fb.info.ID, fb.info.Title = "BV1xx411c7mD", "Part"
	fb.info.Tracks = []string{"ai-zh"}
	r.putCookie("bilibili")
	it := r.ingest(biliP1, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	_, err := r.p.ShareToHub(context.Background(), it.ID, shareReq)
	e := hubErrIs(t, err, 422, "not_shareable")
	if !strings.Contains(e.Msg, "需要登入") {
		t.Errorf("訊息 = %q", e.Msg)
	}
	if r.stub.total() != 0 {
		t.Error("bilibili-ai 不該連線")
	}
}

// Hub 的回應對應成 Client 的錯誤（ADR-0032 第 5 點）；失敗時不記錄分享，訊息不含 token。
func TestShareToHubMapsHubFailures(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		status    int
		body      string
		code      string
		http      int
		wantInMsg string
		retry     int
	}{
		{"401", 401, `{"code":"unauthorized","message":"bad token ` + hubTestToken + `"}`, "hub_unauthorized", 502, "token 無效", 0},
		{"403 帶缺少的權限", 403, `{"code":"forbidden","required_permission":"upload"}`, "hub_forbidden", 502, "「upload」權限", 0},
		{"403 的權限名稱不在白名單", 403, `{"required_permission":"<script>alert(1)</script>"}`, "hub_forbidden", 502, "拒絕了這個請求", 0},
		{"429 帶 retry_after_s", 429, `{"retry_after_s":42}`, "hub_rate_limited", 502, "42 秒", 42},
		{"429 的數字不合理時用預設", 429, `{"retry_after_s":99999999}`, "hub_rate_limited", 502, "60 秒", 60},
		{"404", 404, `{}`, "hub_version_not_found", 404, "找不到", 0},
		{"410", 410, `{}`, "hub_version_not_found", 404, "找不到", 0},
		{"422 原樣轉出", 422, `{"code":"license_conflict","message":"授權衝突\u0007"}`, "license_conflict", 422, "授權衝突", 0},
		{"422 的代碼不合法時用預設", 422, `{"code":"Bad Code!","message":"x"}`, "invalid_request", 422, "x", 0},
		{"500", 500, `{"message":"stack trace at /srv/hub/app.go:12"}`, "hub_unavailable", 502, "HTTP 500", 0},
		{"503", 503, ``, "hub_unavailable", 502, "HTTP 503", 0},
		{"418 等非預期的狀態", 418, ``, "hub_unavailable", 502, "HTTP 418", 0},
	}
	for _, tt := range tests {
		stub := newHubStub(t)
		r := newHubRig(t, stub)
		it := r.processedItem()
		stub.status, stub.errBody = tt.status, tt.body
		_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
		he := hubErrOf(t, err)
		msg := he.Error()
		if he.Code() != tt.code || he.HTTPStatus() != tt.http || !strings.Contains(msg, tt.wantInMsg) || he.RetryAfterS != tt.retry {
			t.Errorf("%s：code=%s http=%d retry=%d msg=%q", tt.name, he.Code(), he.HTTPStatus(), he.RetryAfterS, msg)
		}
		if tt.status != 422 && (strings.Contains(msg, hubTestToken) || strings.Contains(msg, "/srv/hub") || strings.Contains(msg, "<script>")) {
			t.Errorf("%s：訊息含 Hub 的原文：%q", tt.name, msg)
		}
		if strings.ContainsRune(msg, '\u0007') {
			t.Errorf("%s：訊息含控制字元", tt.name)
		}
		if rows, _ := r.p.ItemHubShares(ctx, it.ID); len(rows) != 0 {
			t.Errorf("%s：失敗的分享不該記錄：%+v", tt.name, rows)
		}
	}
}

func TestShareToHubRejectsMalformedShareResponse(t *testing.T) {
	ctx := context.Background()
	for name, body := range map[string]string{
		"不是 JSON":       `<html>`,
		"沒有 share_id":   `{"versions":[]}`,
		"不合法的 share_id": `{"share_id":"a b/../c","versions":[]}`,
		"空的 share_id":   `{"share_id":""}`,
	} {
		stub := newHubStub(t)
		stub.shareResp = body
		r := newHubRig(t, stub)
		it := r.processedItem()
		_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
		he := hubErrOf(t, err)
		if he.Code() != "hub_content_invalid" || he.HTTPStatus() != 422 {
			t.Errorf("%s：err = %+v", name, he)
		}
		if rows, _ := r.p.ItemHubShares(ctx, it.ID); len(rows) != 0 {
			t.Errorf("%s：不該記錄分享", name)
		}
	}
}

func TestShareToHubConnectionFailureAndTimeout(t *testing.T) {
	ctx := context.Background()
	stub := newHubStub(t)
	r := newHubRig(t, stub)
	it := r.processedItem()

	// 逾時（以注入的 clock 推進）。
	arrived := stub.waitHang()
	err := r.runWithTimeout(arrived, hubclient.DefaultTimeout, func() error {
		_, err := r.p.ShareToHub(ctx, it.ID, shareReq)
		return err
	})
	he := hubErrOf(t, err)
	if !he.Timeout || he.Code() != "hub_unavailable" || !strings.Contains(he.Error(), "連線逾時") {
		t.Errorf("逾時：%+v", he)
	}

	// 連線失敗。
	stub.srv.Close()
	_, err = r.p.ShareToHub(ctx, it.ID, shareReq)
	he = hubErrOf(t, err)
	if he.Timeout || he.Code() != "hub_unavailable" || !strings.Contains(he.Error(), "連線失敗") || strings.Contains(he.Error(), hubTestToken) {
		t.Errorf("連線失敗：%+v %q", he, he.Error())
	}
	if rows, _ := r.p.ItemHubShares(ctx, it.ID); len(rows) != 0 {
		t.Errorf("不該記錄分享：%+v", rows)
	}

	// 呼叫端取消：回傳 context 的錯誤，不是 hub_unavailable。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.p.ShareToHub(cctx, it.ID, shareReq); !errors.Is(err, context.Canceled) {
		t.Errorf("取消：%v", err)
	}
}

// ---- bundleArtifact ----

func TestBundleArtifactKinds(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	write := func(rel string, data []byte) {
		t.Helper()
		full := filepath.Join(r.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prov := json.RawMessage(`{"executor":"local"}`)

	// 字幕原樣；語言標記不合法時用 und。
	write("a/t.json", []byte(`{"cues":[]}`))
	got, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "t", Kind: "transcript", Variant: "source", Lang: "en_US", Path: "a/t.json", Provenance: prov})
	if err != nil || got.Lang != "und" || string(got.Data) != `{"cues":[]}` || got.Variant != "source" {
		t.Errorf("transcript = %+v, %v", got, err)
	}
	got, err = r.p.bundleArtifact(ctx, store.Artifact{ID: "t", Kind: "transcript", Variant: "source", Lang: "zh-Hant", Path: "a/t.json", Provenance: prov})
	if err != nil || got.Lang != "zh-Hant" {
		t.Errorf("lang = %+v, %v", got, err)
	}

	// 筆記只取 Markdown 本文。
	write("a/n.json", []byte(`{"lang":"en","markdown":"# Hi\n","frontmatter":{"k":"v"},"engine":"x"}`))
	got, err = r.p.bundleArtifact(ctx, store.Artifact{ID: "n", Kind: "note", Lang: "en", Path: "a/n.json", Provenance: prov})
	if err != nil || string(got.Data) != "# Hi\n" {
		t.Errorf("note = %q, %v", got.Data, err)
	}
	write("a/bad.json", []byte(`not json`))
	if _, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "n", Kind: "note", Path: "a/bad.json", Provenance: prov}); err == nil || !strings.Contains(err.Error(), "筆記") {
		t.Errorf("損壞的筆記：%v", err)
	}
	if _, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "f", Kind: "frames", Path: "a/bad.json", Provenance: prov}); err == nil || !strings.Contains(err.Error(), "畫面") {
		t.Errorf("損壞的畫面：%v", err)
	}

	// 章節：合法 JSON 去掉前後空白；不是 JSON 時拒絕。
	write("a/c.json", []byte("\n {\"chapters\":[]} \n"))
	got, err = r.p.bundleArtifact(ctx, store.Artifact{ID: "c", Kind: "chapters", Path: "a/c.json", Provenance: prov})
	if err != nil || string(got.Data) != `{"chapters":[]}` {
		t.Errorf("chapters = %q, %v", got.Data, err)
	}
	if _, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "c", Kind: "chapters", Path: "a/bad.json", Provenance: prov}); err == nil || !strings.Contains(err.Error(), "不是 JSON") {
		t.Errorf("不是 JSON 的章節：%v", err)
	}

	// 檔案不存在。
	if _, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "x", Kind: "chapters", Path: "a/missing.json", Provenance: prov}); err == nil {
		t.Error("檔案不存在時應該失敗")
	}

	// 畫面：只放選中的畫面與圖片，不帶候選與本機檔名。
	manifest := `{"candidates":[
		{"n":1,"at_ms":1000,"score":0.9,"file":"/home/u/secret/001.jpg","thumb":"001.thumb.jpg","selected":true,"caption":"cap","chapter":"ch"},
		{"n":2,"at_ms":2000,"score":0.5,"file":"002.jpg","thumb":"002.thumb.jpg","selected":false}]}`
	write("a/f.json", []byte(manifest))
	dir := framesDir(r.dir, "item1", "frm1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, frameFile(1)), []byte("JPEG1"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = r.p.bundleArtifact(ctx, store.Artifact{ID: "frm1", ItemID: "item1", Kind: "frames", Path: "a/f.json", Provenance: prov})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || got.Images[0].N != 1 || string(got.Images[0].Data) != "JPEG1" {
		t.Errorf("images = %+v", got.Images)
	}
	if s := string(got.Data); strings.Contains(s, "secret") || strings.Contains(s, "score") || strings.Contains(s, "thumb") || !strings.Contains(s, `"caption":"cap"`) || strings.Contains(s, `"n":2`) {
		t.Errorf("畫面內容 = %s", s)
	}
	// 選中的畫面缺圖片。
	write("a/f2.json", []byte(`{"candidates":[{"n":7,"selected":true}]}`))
	if _, err := r.p.bundleArtifact(ctx, store.Artifact{ID: "frm1", ItemID: "item1", Kind: "frames", Path: "a/f2.json", Provenance: prov}); err == nil || !strings.Contains(err.Error(), "畫面 7") {
		t.Errorf("缺圖片：%v", err)
	}
}
