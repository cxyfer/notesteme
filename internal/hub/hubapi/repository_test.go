package hubapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubblob"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/websession"
)

type repoEnv struct {
	*permEnv
	blobs hubblob.Store
}

func newRepoEnv(t *testing.T, mod func(*hubconfig.Config)) *repoEnv {
	t.Helper()
	pe := newPermEnv(t, func(c *hubconfig.Config) {
		c.Licenses = append([]string(nil), hubconfig.DefaultLicenses...)
		c.ContentDefaults = map[string]string{"frames": hubconfig.VisibilityPrivate}
		c.Bundles = hubconfig.Bundles{MaxUploadBytes: 1 << 20, MaxTotalBytes: 8 << 20, MaxFiles: 100, MaxFileBytes: 4 << 20, MaxRatio: 100}
		c.RateLimits.SharesPerHour = 100
		c.RateLimits.UploadBytesPerDay = 64 << 20
		c.Groups["translators"] = hubconfig.Group{Permissions: []string{"read"}}
		if mod != nil {
			mod(c)
		}
	})
	blobs, err := hubblob.NewFS(filepath.Join(pe.dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := hubauth.New(hubauth.Deps{Config: pe.cfg, Store: pe.store, Clock: pe.clk})
	if err != nil {
		t.Fatal(err)
	}
	pe.h = New(Deps{Config: pe.cfg, Store: pe.store, Blobs: blobs, Clock: pe.clk, Auth: auth})
	e := &repoEnv{permEnv: pe, blobs: blobs}
	e.grant("alice", hubconfig.GroupContributors)
	return e
}

// ---- bundle 產生器 ----

type art struct {
	kind, variant, lang, content string
	prov                         map[string]any
	data                         []byte // 有值時取代依 content 產生的內容
	id                           string
}

func (a *art) bytes(t *testing.T) []byte {
	if a.data != nil {
		return a.data
	}
	if a.kind != "transcript" {
		return []byte(`{"name":"` + a.content + `"}`)
	}
	var b bytes.Buffer
	err := subtitle.RenderCuesJSON(&b, subtitle.Transcript{Lang: a.lang, Variant: subtitle.Variant(a.variant),
		Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: a.content, Timing: subtitle.TimingSource}}})
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sum(d []byte) string {
	s := sha256.Sum256(d)
	return hex.EncodeToString(s[:])
}

type bundleOpts struct {
	item    map[string]any
	license any
	extra   map[string][]byte // 列在 files 中的額外檔案
}

func makeBundle(t *testing.T, o bundleOpts, arts ...*art) []byte {
	t.Helper()
	item := map[string]any{"platform": "youtube", "external_id": "vid00000001", "title": "T", "availability": "public"}
	for k, v := range o.item {
		item[k] = v
	}
	var artifacts, files []any
	var entries [][2]any
	for i, a := range arts {
		if a.id == "" {
			a.id = fmt.Sprintf("0193a3b4-0000-7000-8000-%012d", i+1)
		}
		path := "artifacts/" + a.id + ".json"
		d := a.bytes(t)
		prov := a.prov
		if prov == nil {
			prov = map[string]any{"executor": "api"}
		}
		m := map[string]any{"id": a.id, "kind": a.kind, "format": "json", "path": path, "provenance": prov}
		if a.kind == "transcript" {
			m["variant"], m["format"] = a.variant, "notesteme-cues+json"
		}
		if a.lang != "" {
			m["lang"] = a.lang
		}
		artifacts = append(artifacts, m)
		files = append(files, map[string]any{"path": path, "sha256": sum(d), "size": len(d)})
		entries = append(entries, [2]any{path, d})
	}
	for p, d := range o.extra {
		files = append(files, map[string]any{"path": p, "sha256": sum(d), "size": len(d)})
		entries = append(entries, [2]any{p, d})
	}
	lic := o.license
	if lic == nil {
		lic = "CC-BY-4.0"
	}
	man, _ := json.Marshal(map[string]any{"bundle_version": 0, "item": item, "artifacts": artifacts, "files": files,
		"created_by": map[string]any{"app_version": "0.5.0"}, "license": lic})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, d []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(d)
	}
	write("manifest.json", man)
	for _, e := range entries {
		write(e[0].(string), e[1].([]byte))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func src(content string) *art {
	return &art{kind: "transcript", variant: "source", lang: "en", content: content}
}

// share 以 session 送出 multipart；fields 的值為 "<absent>" 時不送。
func (e *repoEnv) share(who string, bundle []byte, fields map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if bundle != nil {
		w, _ := mw.CreateFormFile("bundle", "x.notesteme.zip")
		_, _ = w.Write(bundle)
	}
	f := map[string]string{"attest": "true"}
	for k, v := range fields {
		f[k] = v
	}
	for k, v := range f {
		if v != "<absent>" {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/v1/shares", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
	tok := e.toks[who]
	r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: tok})
	r.AddCookie(&http.Cookie{Name: hubauth.CSRFCookie, Value: websession.CSRFFor(tok)})
	r.Header.Set(hubauth.CSRFHeader, websession.CSRFFor(tok))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *repoEnv) mustShare(who string, b []byte, fields map[string]string) map[string]any {
	e.t.Helper()
	rec := e.share(who, b, fields)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("分享 = %d %s", rec.Code, rec.Body)
	}
	return decode(e.t, rec)
}

func versionsOf(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range m["versions"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func (e *repoEnv) blobCount() int {
	n := 0
	_ = filepath.Walk(filepath.Join(e.dir, "blobs"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.HasPrefix(info.Name(), ".") {
			n++
		}
		return nil
	})
	return n
}

func (e *repoEnv) videoVersions(ext string) []hubstore.Version {
	v, err := e.store.VideoByKey(context.Background(), "youtube", ext, 0)
	if err != nil {
		return nil
	}
	vs, _ := e.store.VideoVersions(context.Background(), v.ID)
	return vs
}

// ---- 測試 ----

func TestShareCreatesVersionsAndDedupes(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	s := src("raw")
	n := &art{kind: "transcript", variant: "normalized", lang: "en", content: "norm"}
	b := makeBundle(t, bundleOpts{}, s, n)
	n.prov = map[string]any{"executor": "api", "parents_sha256": []any{sum(s.bytes(t))}}
	b = makeBundle(t, bundleOpts{}, s, n)
	out := e.mustShare("alice", b, nil)
	vs := versionsOf(out)
	if len(vs) != 2 || vs[0]["variant"] != "source" || vs[1]["variant"] != "normalized" || vs[0]["deduplicated"] != false ||
		vs[0]["visibility"] != "authenticated" || out["video"].(map[string]any)["part"] != nil {
		t.Fatalf("分享結果 = %v", out)
	}
	// 同一個人再送一次相同的 bundle：同一個分享，什麼都不新增。
	again := e.mustShare("alice", b, nil)
	if again["share_id"] != out["share_id"] || len(e.videoVersions("vid00000001")) != 2 {
		t.Errorf("冪等的分享 = %v", again)
	}
	// 另一個人分享相同的內容（不同的 bundle）：去重，uploaders 多一位，blob 只有一份。
	before := e.blobCount()
	other := e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("raw")), nil)
	if v := versionsOf(other)[0]; v["deduplicated"] != true || v["id"] != vs[0]["id"] {
		t.Errorf("去重 = %v", other)
	}
	if e.blobCount() != before {
		t.Errorf("去重之後 blob 從 %d 變成 %d", before, e.blobCount())
	}
	rec := e.as("bob", "GET", "/api/v1/versions/"+vs[0]["id"].(string), "")
	m := decode(t, rec)
	ups, _ := json.Marshal(m["uploaders"])
	if rec.Code != 200 || string(ups) != `[{"display_name":"alice"},{"display_name":"bob"}]` {
		t.Errorf("uploaders = %d %s", rec.Code, rec.Body)
	}
	// 衍生關係：normalized 的 parent 是 source。
	rec = e.as("alice", "GET", "/api/v1/versions/"+vs[1]["id"].(string), "")
	if m := decode(t, rec); len(m["parents"].([]any)) != 1 || m["parents"].([]any)[0].(map[string]any)["relation"] != "parent" {
		t.Errorf("parents = %s", rec.Body)
	}
	rec = e.as("alice", "GET", "/api/v1/versions/"+vs[0]["id"].(string), "")
	if m := decode(t, rec); len(m["children"].([]any)) != 1 {
		t.Errorf("children = %s", rec.Body)
	}
}

func TestShareLinksParentsUploadedLater(t *testing.T) {
	e := newRepoEnv(t, nil)
	norm := &art{kind: "transcript", variant: "normalized", lang: "en", content: "n"}
	fixed := &art{kind: "transcript", variant: "corrected", lang: "en", content: "c",
		prov: map[string]any{"executor": "api", "parents_sha256": []any{sum(norm.bytes(t))}}}
	child := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, fixed), nil))[0]
	parent := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, norm), nil))[0]
	ps, _, err := e.store.VersionDerivations(context.Background(), child["id"].(string))
	if err != nil || len(ps) != 1 || ps[0].ID != parent["id"] || ps[0].Relation != hubstore.RelationParent {
		t.Errorf("補上的 parent = %+v %v", ps, err)
	}
}

func TestShareRejectsBeforeWritingAnything(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.Bundles.MaxUploadBytes = 4 << 10 })
	good := makeBundle(t, bundleOpts{}, src("raw"))
	cases := []struct {
		name   string
		bundle []byte
		fields map[string]string
		status int
		code   string
		msg    string
	}{
		{"沒有 attest", good, map[string]string{"attest": "<absent>"}, 422, "attest_required", "分享前要確認你有權分享這些內容。"},
		{"attest 不是 true", good, map[string]string{"attest": "yes"}, 422, "attest_required", ""},
		{"超過站台上限", good, map[string]string{"visibility": "public"}, 422, "invalid_request", "visibility：可見度不能超過站台上限（authenticated）。"},
		{"不認得的群組", good, map[string]string{"visibility": "group:nope"}, 422, "invalid_request", ""},
		{"沒有 bundle", nil, nil, 422, "invalid_request", ""},
		{"不是 zip", []byte("hello"), nil, 422, "bundle_invalid", "這不是 Notesteme bundle（無法以 zip 讀取）。"},
		{"太大", append(bytes.Clone(good), make([]byte, 5<<10)...), nil, 413, "bundle_too_large", "bundle 超過 4 KiB 的上傳上限。"},
		{"本機檔案", makeBundle(t, bundleOpts{item: map[string]any{"platform": "local"}}, src("x")), nil, 422, "not_shareable", "本機檔案不能分享到 Hub。"},
		{"非公開", makeBundle(t, bundleOpts{item: map[string]any{"availability": "unlisted"}}, src("x")), nil, 422, "not_shareable", "非公開影音不能分享到 Hub。"},
		{"cookie_required", makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", content: "x",
			prov: map[string]any{"cookie_required": true}}), nil, 422, "not_shareable", "需要登入才能取得的字幕不能分享到 Hub。"},
		{"bilibili-ai 沒有 cookie_required", makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", content: "x",
			prov: map[string]any{"source": "bilibili-ai"}}), nil, 422, "not_shareable", "需要登入才能取得的字幕不能分享到 Hub。"},
		{"媒體檔", makeBundle(t, bundleOpts{extra: map[string][]byte{"artifacts/clip.jpg": []byte("\x00\x00\x00\x18ftypisom")}}, src("x")),
			nil, 422, "not_shareable", "bundle 中不能包含媒體檔。"},
		{"sponsor_segments", makeBundle(t, bundleOpts{}, &art{kind: "sponsor_segments", content: "x"}), nil, 422, "not_shareable", "Hub 不接受這種產出：sponsor_segments。"},
		{"授權", makeBundle(t, bundleOpts{license: "proprietary"}, src("x")), nil, 422, "license_invalid", "不支援的授權：proprietary。"},
		{"沒有授權", makeBundle(t, bundleOpts{license: json.RawMessage("null")}, src("x")), nil, 422, "license_invalid", ""},
		{"來源紀錄太大", makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", content: "x",
			prov: map[string]any{"model": strings.Repeat("x", 2<<10)}}), nil, 0, "", ""}, // 4 KiB 的 bundle 放不下 16 KiB：另外測
		{"內容格式", makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", data: []byte(`{"variant":"bad"}`)}),
			nil, 422, "bundle_invalid", ""},
	}
	for _, c := range cases {
		if c.status == 0 {
			continue
		}
		rec := e.share("alice", c.bundle, c.fields)
		if rec.Code != c.status || decode(t, rec)["code"] != c.code {
			t.Errorf("%s = %d %s，預期 %d %s", c.name, rec.Code, rec.Body, c.status, c.code)
			continue
		}
		if c.msg != "" && decode(t, rec)["message"] != c.msg {
			t.Errorf("%s 的訊息 = %v", c.name, decode(t, rec)["message"])
		}
	}
	if n := e.blobCount(); n != 0 {
		t.Errorf("拒絕的分享留下了 %d 個 blob", n)
	}
	if n, _ := e.store.ShareUsage(context.Background(), e.users["alice"], time.Time{}); n.Count != 0 {
		t.Errorf("拒絕的分享留下了 %d 筆分享", n.Count)
	}
}

func TestShareProvenanceLimitAndAllowlist(t *testing.T) {
	e := newRepoEnv(t, nil)
	big := makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", content: "x",
		prov: map[string]any{"model": strings.Repeat("x", 17<<10)}})
	rec := e.share("alice", big, nil)
	if rec.Code != 422 || !strings.Contains(decode(t, rec)["message"].(string), "超過 16 KiB") {
		t.Errorf("來源紀錄太大 = %d %s", rec.Code, rec.Body)
	}
	ok := makeBundle(t, bundleOpts{}, &art{kind: "transcript", variant: "source", lang: "en", content: "x",
		prov: map[string]any{"executor": "api", "provider": "groq", "model": "w", "workdir": "/data/tmp/task-42", "api_key": "sk-123"}})
	v := versionsOf(e.mustShare("alice", ok, nil))[0]
	rec = e.as("alice", "GET", "/api/v1/versions/"+v["id"].(string), "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "/data/tmp") || strings.Contains(rec.Body.String(), "sk-123") ||
		decode(t, rec)["transcribe_model"] != "groq/w" {
		t.Errorf("詳情 = %d %s", rec.Code, rec.Body)
	}
}

func TestShareModelVerification(t *testing.T) {
	e := newRepoEnv(t, nil)
	s := src("raw")
	fix := &art{kind: "transcript", variant: "corrected", lang: "en", content: "fix",
		prov: map[string]any{"executor": "agent", "model_reported": "claude-x", "parents_sha256": []any{sum(s.bytes(t))}}}
	vs := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, s, fix), nil))
	src0, _ := e.store.Version(context.Background(), vs[0]["id"].(string))
	fixed, _ := e.store.Version(context.Background(), vs[1]["id"].(string))
	if !src0.ModelVerified || fixed.ModelVerified || fixed.CorrectModel != "claude-x" {
		t.Errorf("model_verified = %v %+v", src0.ModelVerified, fixed)
	}
	rec := e.as("alice", "GET", "/api/v1/versions/"+fixed.ID, "")
	if b, _ := json.Marshal(decode(t, rec)["badges"]); string(b) != `["unverified_model"]` {
		t.Errorf("badges = %s", rec.Body)
	}
}

func TestShareVisibilityAndFrames(t *testing.T) {
	e := newRepoEnv(t, nil)
	frames := &art{kind: "frames", content: "f"}
	vs := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("a"), frames), map[string]string{"visibility": "group:translators"}))
	if vs[0]["visibility"] != "group:translators" || vs[1]["visibility"] != "private" {
		t.Errorf("可見度 = %v", vs)
	}
	id := vs[0]["id"].(string)
	// 看不到的版本與不存在的版本回應完全相同。
	hidden := e.as("bob", "GET", "/api/v1/versions/"+id, "")
	missing := e.as("bob", "GET", "/api/v1/versions/0193a3b4-0000-7000-8000-000000000999", "")
	if hidden.Code != 404 || hidden.Body.String() != missing.Body.String() {
		t.Errorf("看不到 = %d %s；不存在 = %d %s", hidden.Code, hidden.Body, missing.Code, missing.Body)
	}
	e.grant("bob", "translators")
	expect(t, "群組成員", e.as("bob", "GET", "/api/v1/versions/"+id, ""), 200, "")
	expect(t, "frames 是 private", e.as("bob", "GET", "/api/v1/versions/"+vs[1]["id"].(string), ""), 404, "not_found")
	expect(t, "上傳者看得到 private", e.as("alice", "GET", "/api/v1/versions/"+vs[1]["id"].(string), ""), 200, "")
	e.grant("bob", hubconfig.GroupModerators)
	expect(t, "moderate 看得到 private", e.as("bob", "GET", "/api/v1/versions/"+vs[1]["id"].(string), ""), 200, "")
	// PAT 只帶 read：沒有 moderate，看不到 private。
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	expect(t, "PAT 沒有 moderate", e.bearer(tok, "GET", "/api/v1/versions/"+vs[1]["id"].(string), "", ""), 404, "not_found")
}

func TestHubOriginNeedsAVisibleVersion(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	secret := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("secret")), map[string]string{"visibility": "private"}))[0]
	open := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{item: map[string]any{"external_id": "vid00000002"}}, src("open")), nil))[0]
	redo := func(origin map[string]any, content string) string {
		a := &art{kind: "transcript", variant: "corrected", lang: "en", content: content,
			prov: map[string]any{"executor": "api", "hub_origin": map[string]any{"version_id": origin["id"], "sha256": origin["sha256"]}}}
		return versionsOf(e.mustShare("bob", makeBundle(t, bundleOpts{}, a), nil))[0]["id"].(string)
	}
	// 看不到的版本：不建立 regenerated_from（不能藉此確認私人版本存在）。
	ps, _, _ := e.store.VersionDerivations(context.Background(), redo(secret, "r1"))
	if len(ps) != 0 {
		t.Errorf("指到私人版本的 hub_origin 建立了 %+v", ps)
	}
	id := redo(open, "r2")
	rec := e.as("bob", "GET", "/api/v1/versions/"+id, "")
	p := decode(t, rec)["parents"].([]any)
	if len(p) != 1 || p[0].(map[string]any)["summary"] != "以 @alice 的原始字幕重新生成" {
		t.Errorf("parents = %s", rec.Body)
	}
}

func TestRemovedContentAndStates(t *testing.T) {
	e := newRepoEnv(t, nil)
	v := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("raw")), nil))[0]
	id := v["id"].(string)
	ver, _ := e.store.Version(context.Background(), id)
	ctx := context.Background()
	if err := e.store.SetVersionState(ctx, id, "hidden"); err != nil {
		t.Fatal(err)
	}
	expect(t, "hidden 對其他人", e.as("bob", "GET", "/api/v1/versions/"+id, ""), 404, "not_found")
	if rec := e.as("alice", "GET", "/api/v1/versions/"+id, ""); rec.Code != 200 || decode(t, rec)["state"] != "hidden" {
		t.Errorf("hidden 對上傳者 = %d %s", rec.Code, rec.Body)
	}
	_ = e.store.SetVersionState(ctx, id, "removed")
	_ = e.store.AddRemovedHash(ctx, ver.VideoID, ver.SHA256, "")
	expect(t, "removed", e.as("bob", "GET", "/api/v1/versions/"+id, ""), 410, "removed")
	// 同一份內容（加上一個新的產出）不能再上傳，整個 bundle 都不寫入。
	before := e.blobCount()
	rec := e.share("alice", makeBundle(t, bundleOpts{}, &art{kind: "note", lang: "en", data: []byte("# new note")}, src("raw")), nil)
	expect(t, "removed_content", rec, 409, "removed_content")
	if e.blobCount() != before || len(e.videoVersions("vid00000001")) != 1 {
		t.Errorf("removed_content 之後 blob %d → %d、版本 %d", before, e.blobCount(), len(e.videoVersions("vid00000001")))
	}
	if err := e.store.SetVersionState(ctx, id, "nope"); err == nil {
		t.Error("不認得的狀態要失敗")
	}
}

func TestWithdrawShare(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	first := e.mustShare("alice", makeBundle(t, bundleOpts{}, src("raw")), nil)
	share := first["share_id"].(string)
	id := versionsOf(first)[0]["id"].(string)
	expect(t, "撤回別人的分享", e.as("bob", "DELETE", "/api/v1/shares/"+share, ""), 404, "not_found")
	second := e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("raw")), nil)
	expect(t, "撤回", e.as("alice", "DELETE", "/api/v1/shares/"+share, ""), 204, "")
	if v, _ := e.store.Version(context.Background(), id); v.State != "visible" {
		t.Errorf("還有 bob 的分享，狀態 = %s", v.State)
	}
	expect(t, "撤回兩次", e.as("alice", "DELETE", "/api/v1/shares/"+share, ""), 404, "not_found")
	expect(t, "bob 撤回", e.as("bob", "DELETE", "/api/v1/shares/"+second["share_id"].(string), ""), 204, "")
	if v, _ := e.store.Version(context.Background(), id); v.State != "withdrawn" {
		t.Errorf("所有分享都撤回之後，狀態 = %s", v.State)
	}
	expect(t, "撤回後連上傳者也看不到", e.as("alice", "GET", "/api/v1/versions/"+id, ""), 404, "not_found")
	// 撤回後再分享相同的 bundle：建立新的分享（不是冪等地回傳撤回的那次）。
	again := e.mustShare("alice", makeBundle(t, bundleOpts{}, src("raw")), nil)
	if again["share_id"] == share {
		t.Error("撤回後再分享回傳了撤回的分享")
	}
}

func TestShareRateLimits(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.RateLimits.SharesPerHour = 1 })
	e.mustShare("alice", makeBundle(t, bundleOpts{}, src("a")), nil)
	e.clk.Advance(10 * time.Minute)
	rec := e.share("alice", makeBundle(t, bundleOpts{}, src("b")), nil)
	expect(t, "每小時", rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") != "3000" || decode(t, rec)["message"] != "上傳太頻繁，請在 3000 秒後再試。" {
		t.Errorf("Retry-After = %q %s", rec.Header().Get("Retry-After"), rec.Body)
	}
	e.clk.Advance(51 * time.Minute)
	e.mustShare("alice", makeBundle(t, bundleOpts{}, src("b")), nil)

	d := newRepoEnv(t, func(c *hubconfig.Config) { c.RateLimits.UploadBytesPerDay = 512 })
	b := makeBundle(t, bundleOpts{}, src("a"))
	if len(b) > 512 {
		expect(t, "每日上傳量", d.share("alice", b, nil), 429, "rate_limited")
	} else {
		t.Fatalf("測試的 bundle 太小（%d bytes）", len(b))
	}
}

func TestShareNeedsMultipart(t *testing.T) {
	e := newRepoEnv(t, nil)
	expect(t, "JSON 本體", e.as("alice", "POST", "/api/v1/shares", `{"bundle":"x"}`), 400, "invalid_request")
}

func TestReadVersionAnonymouslyOnAPublicSite(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.SiteVisibility = hubconfig.VisibilityPublic })
	pub := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("a")), map[string]string{"visibility": "public"}))[0]
	dflt := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("c")), nil))[0]
	if dflt["visibility"] != "public" {
		t.Errorf("公開站台的預設可見度 = %v", dflt["visibility"])
	}
	auth := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("b")), map[string]string{"visibility": "authenticated"}))[0]
	expect(t, "public", e.do("GET", "/api/v1/versions/"+pub["id"].(string)), 200, "")
	expect(t, "authenticated", e.do("GET", "/api/v1/versions/"+auth["id"].(string)), 404, "not_found")
}

// 超過 Huma 預設 1 MiB 的分享：multipart 要在 Huma 讀取本體之前由 rawOperations 接手。
func TestShareLargerThanHumaDefaultLimit(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.Bundles.MaxUploadBytes = 8 << 20 })
	// 每句的文字有上限（hubrepo.MaxCueTextBytes），所以分成很多句。
	var cues []subtitle.Cue
	size := 0
	seed := sha256.Sum256([]byte("seed"))
	for size < 3<<20 {
		var text strings.Builder
		for text.Len() < 4096 {
			seed = sha256.Sum256(seed[:])
			text.WriteString(hex.EncodeToString(seed[:]))
		}
		size += text.Len()
		cues = append(cues, subtitle.Cue{Index: len(cues) + 1, StartMS: int64(len(cues)) * 1000, EndMS: int64(len(cues))*1000 + 900, Text: text.String(), Timing: subtitle.TimingSource})
	}
	a := src("")
	a.data = cuesData(t, cues)
	b := makeBundle(t, bundleOpts{}, a)
	if len(b) < 3<<19 { // 1.5 MiB
		t.Fatalf("bundle 只有 %d bytes", len(b))
	}
	e.mustShare("alice", b, nil)
}

// 安全修補 A：相同內容的後到者不能藉去重成為別人 private／hidden 版本的上傳者，回應不洩漏既有版本的可見度。
func TestShareDedupDoesNotJoinInvisibleVersions(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	a := src("same-bytes")
	a.prov = map[string]any{"executor": "api", "provider": "groq", "model": "whisper-secret-ft", "created_at": "2026-11-01T00:00:00Z"}
	out := e.mustShare("alice", makeBundle(t, bundleOpts{}, a), map[string]string{"visibility": "private"})
	priv := versionsOf(out)[0]["id"].(string)
	other := e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("same-bytes")), map[string]string{"visibility": "authenticated"})
	v := versionsOf(other)[0]
	if v["id"] == priv || v["deduplicated"] != false || v["visibility"] != "authenticated" {
		t.Fatalf("bob 的分享 = %v，不能併入 alice 的 private 版本 %s", v, priv)
	}
	expect(t, "bob 看 alice 的 private", e.as("bob", "GET", "/api/v1/versions/"+priv, ""), 404, "")
	rec := e.as("bob", "GET", "/api/v1/versions/"+v["id"].(string), "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "whisper-secret-ft") || strings.Contains(rec.Body.String(), `"alice"`) {
		t.Fatalf("bob 自己的版本 = %d %s", rec.Code, rec.Body)
	}
	// hidden：同樣另建一列，bob 看不到被隱藏的版本。
	hid := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("hidden-bytes")), nil))[0]["id"].(string)
	if err := e.store.SetVersionState(context.Background(), hid, "hidden"); err != nil {
		t.Fatal(err)
	}
	hv := versionsOf(e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("hidden-bytes")), nil))[0]
	if hv["id"] == hid || hv["deduplicated"] != false {
		t.Fatalf("併入 hidden 版本：%v", hv)
	}
	expect(t, "bob 看 hidden", e.as("bob", "GET", "/api/v1/versions/"+hid, ""), 404, "")
}

// 安全修補 A：撤回的內容再分享時恢復成新分享者的版本（HREP-R8、R10），不會被吞掉。
func TestReshareAfterWithdrawRestores(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	first := e.mustShare("bob", makeBundle(t, bundleOpts{}, src("platform captions")), nil)
	id := versionsOf(first)[0]["id"].(string)
	expect(t, "bob 撤回", e.as("bob", "DELETE", "/api/v1/shares/"+first["share_id"].(string), ""), 204, "")
	again := e.mustShare("alice", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("platform captions")), map[string]string{"visibility": "authenticated"})
	v := versionsOf(again)[0]
	if v["deduplicated"] != false || v["visibility"] != "authenticated" {
		t.Fatalf("alice 的分享 = %v", v)
	}
	rec := e.as("alice", "GET", "/api/v1/versions/"+v["id"].(string), "")
	m := decode(t, rec)
	if rec.Code != 200 || m["state"] != "visible" || m["license"] != "CC0-1.0" || v["id"] != id {
		t.Fatalf("恢復後 = %d %v", rec.Code, m)
	}
	ups, _ := json.Marshal(m["uploaders"])
	if string(ups) != `[{"display_name":"alice"}]` {
		t.Fatalf("uploaders = %s", ups)
	}
}

// 安全修補 A：不在群組裡的人指定相同的 group:<name> 也不能併入群組的版本；群組成員可以。
func TestShareDedupGroupNeedsMembership(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	e.grant("alice", "translators")
	vis := map[string]string{"visibility": "group:translators"}
	g := versionsOf(e.mustShare("alice", makeBundle(t, bundleOpts{}, src("group-bytes")), vis))[0]["id"].(string)
	bv := versionsOf(e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC0-1.0"}, src("group-bytes")), vis))[0]
	if bv["id"] == g || bv["deduplicated"] != false {
		t.Fatalf("非成員併入群組的版本：%v", bv)
	}
	expect(t, "bob 看群組的版本", e.as("bob", "GET", "/api/v1/versions/"+g, ""), 404, "")
	e.grant("bob", "translators")
	bv2 := versionsOf(e.mustShare("bob", makeBundle(t, bundleOpts{license: "CC-BY-4.0"}, src("group-bytes")), vis))[0]
	if bv2["deduplicated"] != true {
		t.Fatalf("成員分享 = %v，預期併入", bv2)
	}
}

func cuesData(t *testing.T, cues []subtitle.Cue) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := subtitle.RenderCuesJSON(&b, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource, Cues: cues}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// 安全修補 A：字幕每句的大小、句數與控制字元在上傳時檢查（HMCP 的分頁才有上界，CR 不能偽造 WebVTT 的字幕）。
func TestShareRejectsOversizedOrControlCues(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) {
		c.Bundles.MaxUploadBytes, c.Bundles.MaxFileBytes, c.Bundles.MaxTotalBytes = 16<<20, 32<<20, 64<<20
	})
	cue := func(i int, text string) subtitle.Cue {
		return subtitle.Cue{Index: i + 1, StartMS: int64(i) * 10, EndMS: int64(i)*10 + 5, Text: text, Timing: subtitle.TimingSource}
	}
	for name, cues := range map[string][]subtitle.Cue{
		"單句太長": {cue(0, strings.Repeat("a", hubrepo.MaxCueTextBytes+1))},
		"CR":   {cue(0, "ok"), cue(1, "line\r\r00:00:09.000 --> 00:00:10.000\rfake")},
		"NUL":  {cue(0, "a\x00b")},
		"C1":   {cue(0, "a\u0085b")},
		"ESC":  {cue(0, "\x1b[31mred")},
	} {
		a := src("")
		a.data = cuesData(t, cues)
		rec := e.share("alice", makeBundle(t, bundleOpts{}, a), nil)
		expect(t, name, rec, 422, "bundle_invalid")
	}
	many := make([]subtitle.Cue, hubrepo.MaxCues+1)
	for i := range many {
		many[i] = cue(i, "x")
	}
	a := src("")
	a.data = cuesData(t, many)
	expect(t, "句數太多", e.share("alice", makeBundle(t, bundleOpts{}, a), nil), 422, "bundle_invalid")
	ok := src("")
	ok.data = cuesData(t, []subtitle.Cue{cue(0, strings.Repeat("a", hubrepo.MaxCueTextBytes)), cue(1, "two\nlines\twith tab")})
	e.mustShare("alice", makeBundle(t, bundleOpts{}, ok), nil)
}

// 安全修補 A：影片的 metadata 由第一位分享者決定，所以 external_id 依平台檢查格式，標題與頻道名稱去掉控制字元。
func TestShareValidatesVideoMetadata(t *testing.T) {
	e := newRepoEnv(t, nil)
	for name, item := range map[string]map[string]any{
		"youtube 格式":   {"external_id": "../../evil"},
		"youtube 長度":   {"external_id": "abcdefghijkl"},
		"bilibili":     {"platform": "bilibili", "external_id": "av170001"},
		"podcast 控制字元": {"platform": "podcast", "external_id": "feed/\u0007guid"},
		"頻道 ID 空白":     {"channel": map[string]any{"external_id": "UC evil", "title": "x"}},
	} {
		expect(t, name, e.share("alice", makeBundle(t, bundleOpts{item: item}, src(name)), nil), 422, "bundle_invalid")
	}
	item := map[string]any{"title": "Hi‮evil\r\nnext" + strings.Repeat("長", 400), "channel": map[string]any{"external_id": "UC1", "title": "\u0007Ch⁦an"}}
	out := e.mustShare("alice", makeBundle(t, bundleOpts{item: item}, src("ok")), nil)
	v, err := e.store.VideoByKey(context.Background(), "youtube", "vid00000001", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(v.Title, "‮\r\n") || !strings.HasPrefix(v.Title, "Hievil next") || len([]rune(v.Title)) != hubrepo.MaxVideoText || v.ChannelTitle != "Chan" {
		t.Fatalf("影片 = %q / %q（%v）", v.Title, v.ChannelTitle, out["share_id"])
	}
}
