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
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// bundleRig 準備一部做到 metadata 的影音，並寫入每一種可以匯出的產出。
func bundleRig(t *testing.T) (*rig, store.Item) {
	t.Helper()
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	cues := []subtitle.Cue{{StartMS: 1000, EndMS: 3000, Text: "Hello one."}, {StartMS: 3000, EndMS: 6000, Text: "Hello two."}}
	for _, v := range []subtitle.Variant{subtitle.VariantSource, subtitle.VariantCorrected} {
		if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: v, Lang: "en", Cues: cues}); err != nil {
			t.Fatal(err)
		}
	}
	// 被取代的版本不匯出。
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantCorrected, Lang: "en",
		Cues: []subtitle.Cue{{StartMS: 0, EndMS: 1000, Text: "Newer."}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantTranslated, Lang: "zh-Hant", Cues: cues}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "Intro", StartMS: 0}, {Title: "Body", StartMS: 3000}}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedFrames(ctx, it.ID, []SeedFrame{{AtMS: 1000, Selected: true, Caption: "first"}, {AtMS: 2000}, {AtMS: 4000, Selected: true, Color: "#ff0000"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedNote(ctx, it.ID, "---\ntitle: T\n---\n# Note\n\nBody <script>x</script>\n"); err != nil {
		t.Fatal(err)
	}
	// sponsor_segments：來源紀錄帶著要被清掉的欄位。
	body, _ := json.Marshal(sponsorSegmentsDoc{Segments: []sponsorblock.Segment{{Category: "sponsor", StartMS: 10, EndMS: 20}}})
	prov := json.RawMessage(`{"stage":"sponsorblock","executor":"local","api_key":"sk-zzz","cookie_file":"/home/u/c.txt",` +
		`"tool":"/usr/local/bin/yt-dlp","base_url":"http://10.0.0.5:8080/x","public":"https://user:pw@sponsor.ajay.app/api?k=1",` +
		`"max_tokens":5,"nested":{"path":"C:\\Users\\me\\a.mp4","list":["/etc/x","ok"]}}`)
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(r.dir, it.ID, id, func(w io.Writer) error { _, e := w.Write(body); return e })
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Tx(ctx, func(tx *store.Repo) error {
		_, err := tx.InsertArtifact(ctx, store.NewArtifact{ID: id, ItemID: it.ID, Kind: KindSponsorSegments, Variant: KindSponsorSegments,
			Format: sponsorSegmentsFmt, Path: rel, SHA256: sha, Size: size, Provenance: prov})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return r, it
}

func TestExportBundleRoundTrip(t *testing.T) {
	src, _ := bundleRig(t)
	ctx := context.Background()
	if err := src.p.SetInstanceID(ctx, "0190A1B2-0000-7000-8000-00000000BEEF"); err != nil {
		t.Fatal(err)
	}
	ex, err := src.p.ExportBundle(ctx, "yt:abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Name != "yt-abc123xxxxx.notesteme.zip" {
		t.Errorf("name = %q", ex.Name)
	}
	b, err := bundle.Read(ex.Data, bundle.Options{})
	if err != nil {
		t.Fatalf("匯出的 bundle 不通過自己的驗證：%v", err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatalf("內容：%v", err)
	}
	m := b.Manifest
	if m.CreatedBy.InstanceID != "0190a1b2-0000-7000-8000-00000000beef" || m.CreatedBy.AppVersion != "test" || m.Item.Platform != "youtube" || m.Item.ExternalID != "abc123xxxxx" {
		t.Errorf("manifest = %+v", m)
	}
	kinds := map[string]int{}
	for _, a := range m.Artifacts {
		kinds[a.Kind+"/"+a.Variant]++
		if a.Kind == KindSponsorSegments {
			var p map[string]any
			_ = json.Unmarshal(a.Provenance, &p)
			raw := string(a.Provenance)
			for _, bad := range []string{"sk-zzz", "/home/u", "/usr/local", "10.0.0.5", "user:pw", "k=1", `C:\\`, "/etc/x"} {
				if strings.Contains(raw, bad) {
					t.Errorf("provenance 仍含 %q：%s", bad, raw)
				}
			}
			if p["cookies_used"] != true || p["tool"] != "yt-dlp" || p["max_tokens"] == nil || p["public"] != "https://sponsor.ajay.app/api" {
				t.Errorf("provenance 清理結果不對：%s", raw)
			}
		}
	}
	want := map[string]int{"transcript/source": 1, "transcript/corrected": 1, "transcript/translated": 1, "note/": 1, "chapters/": 1, "sponsor_segments/": 1, "frames/": 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s 有 %d 個，預期 %d（%v）", k, kinds[k], n, kinds)
		}
	}
	// 被取代的 corrected 版本不在 bundle 中；frames 只有選中的兩張。
	jpgs := 0
	for _, p := range b.Paths() {
		if strings.HasSuffix(p, ".jpg") {
			jpgs++
		}
		d, _ := b.File(p)
		if bytes.Contains(d, []byte("Hello one.")) && strings.Contains(p, "corrected") {
			t.Error("不該依路徑找得到變體")
		}
	}
	if jpgs != 2 {
		t.Errorf("畫面有 %d 張，預期 2", jpgs)
	}

	// 匯入到另一個實例。
	dst := newRig(t)
	res, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 7 || res.Skipped != 0 || res.Ref != "yt:abc123xxxxx" || res.Item.Title != "T" {
		t.Fatalf("res = %+v", res)
	}
	runs, err := dst.st.Read().StageRunsByItem(ctx, res.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	for _, run := range runs {
		if run.Status != store.StatusDone || run.Executor != ExecutorImport || run.ArtifactID == "" {
			t.Errorf("run = %+v", run)
		}
		stages[run.Stage] = true
	}
	for _, st := range []string{StageTranscript, StageCorrect, StageTranslate, StageChapters, StageFrames, StageNote, StageSponsorBlock} {
		if !stages[st] {
			t.Errorf("步驟 %s 沒有記為 done", st)
		}
	}
	if stages[StageMetadata] || stages[StageNormalize] {
		t.Errorf("沒有產出的步驟不該有紀錄：%v", stages)
	}
	if n, err := dst.st.Read().CountTasks(ctx, store.TaskPending); err != nil || n != 0 {
		t.Errorf("匯入不建立任務：%d", n)
	}
	// 匯入的字幕進全文搜尋的索引（SRCH-R2：InsertArtifact 的 hook）。
	if n, err := dst.st.SearchIndexCheck(ctx, res.Item.ID); err != nil || n == 0 {
		t.Errorf("索引 = %d %v", n, err)
	}
	// 影音詳情、筆記、frames、字幕都讀得到。
	if _, err := dst.p.Item(ctx, res.Item.ID); err != nil {
		t.Fatalf("Item：%v", err)
	}
	nv, err := dst.p.ItemNote(ctx, res.Item.ID, "")
	if err != nil || !strings.Contains(nv.Markdown, "<script>x</script>") || nv.Frontmatter["title"] != "T" {
		t.Fatalf("note = %+v %v", nv, err)
	}
	fv, err := dst.p.Frames(ctx, res.Item.ID)
	if err != nil || len(fv.Frames) != 2 || fv.Frames[0].Caption != "first" {
		t.Fatalf("frames = %+v %v", fv, err)
	}
	f, _, _, err := dst.p.FrameImage(ctx, res.Item.ID, fv.Frames[1].N, true)
	if err != nil {
		t.Fatalf("縮圖：%v", err)
	}
	_ = f.Close()
	tr, err := dst.p.Cues(ctx, res.Item.ID, CuesQuery{Variant: "corrected", Lang: "en", ToMS: -1})
	if err != nil || len(tr.Cues) != 1 || tr.Cues[0].Text != "Newer." {
		t.Fatalf("corrected = %+v %v", tr, err)
	}
	a, err := dst.st.Read().CurrentArtifacts(ctx, res.Item.ID, KindNote)
	if err != nil || len(a) != 1 {
		t.Fatal(a, err)
	}
	var prov map[string]any
	_ = json.Unmarshal(a[0].Provenance, &prov)
	from, _ := prov["imported_from"].(map[string]any)
	if prov["verified"] != false || from["instance_id"] != "0190a1b2-0000-7000-8000-00000000beef" || from["app_version"] != "test" || from["bundle_sha256"] == "" {
		t.Errorf("provenance = %v", prov)
	}
	// parents 重新對應到新的 ID；指到 bundle 外的丟掉。
	withParents := rezip(t, ex.Data, func(m *bundle.OutManifest, _ map[string][]byte) {
		var tid string
		for _, x := range m.Artifacts {
			if x.Kind == "transcript" {
				tid = x.ID
			}
		}
		for i := range m.Artifacts {
			if m.Artifacts[i].Kind == KindNote {
				m.Artifacts[i].Parents = []string{strings.ToUpper(tid), "0190a1b2-0000-7000-8000-00000000dead"}
			}
		}
	})
	other := newRig(t)
	resP, err := other.p.ImportBundle(ctx, BundleImportRequest{Data: withParents})
	if err != nil {
		t.Fatal(err)
	}
	na, _ := other.st.Read().CurrentArtifacts(ctx, resP.Item.ID, KindNote)
	if len(na) != 1 || len(na[0].Parents) != 1 {
		t.Fatalf("note 的 parents = %+v", na)
	}
	if pa, err := other.st.Read().Artifact(ctx, na[0].Parents[0]); err != nil || pa.Kind != "transcript" || pa.ItemID != resP.Item.ID {
		t.Errorf("parent = %+v %v", pa, err)
	}

	// 再匯入：預設 409；add_versions 全部略過；從 metadata 重跑不出錯。
	var ce *BundleConflictError
	if _, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data}); !errors.As(err, &ce) || ce.Code != CodeBundleItemExists ||
		!strings.Contains(ce.Message, "yt:abc123xxxxx") {
		t.Fatalf("重複匯入 = %v", err)
	}
	res2, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data, AddVersions: true})
	if err != nil || res2.Imported != 0 || res2.Skipped != 7 {
		t.Fatalf("add_versions = %+v %v", res2, err)
	}
	if _, err := dst.p.Rerun(ctx, res.Item.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatalf("重跑：%v", err)
	}
	if _, err := dst.p.Trash(ctx, res.Item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data, AddVersions: true}); !errors.As(err, &ce) || ce.Code != CodeBundleItemInTrash {
		t.Fatalf("垃圾桶 = %v", err)
	}
	if _, err := dst.p.ExportBundle(ctx, res.Item.ID); !errors.Is(err, ErrInTrash) {
		t.Errorf("垃圾桶中的匯出 = %v", err)
	}
	// 永久刪除之後視同手動新增。
	if err := dst.p.Purge(ctx, res.Item.ID); err != nil {
		t.Fatal(err)
	}
	if res3, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data}); err != nil || res3.Imported != 7 {
		t.Fatalf("永久刪除後 = %+v %v", res3, err)
	}
	// 暫存目錄沒有留下東西。
	if ents, _ := os.ReadDir(filepath.Join(dst.dir, "tmp")); len(ents) != 0 {
		t.Errorf("tmp 留下 %d 個項目", len(ents))
	}
}

// rezip 讀出 bundle、套用 edit，再以 bundle.Write 寫回（files 依內容重新計算）。
func rezip(t *testing.T, data []byte, edit func(m *bundle.OutManifest, files map[string][]byte)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	var m bundle.OutManifest
	for _, f := range zr.File {
		rc, _ := f.Open()
		d, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == bundle.ManifestName {
			if err := json.Unmarshal(d, &m); err != nil {
				t.Fatal(err)
			}
			continue
		}
		files[f.Name] = d
	}
	edit(&m, files)
	var entries []bundle.OutEntry
	for p, d := range files {
		entries = append(entries, bundle.OutEntry{Path: p, Data: d})
	}
	out, err := bundle.Write(m, entries)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestImportBundleRejectsBadContent(t *testing.T) {
	src, _ := bundleRig(t)
	ctx := context.Background()
	ex, err := src.p.ExportBundle(ctx, "yt:abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}
	find := func(m *bundle.OutManifest, kind string) *bundle.OutArtifact {
		for i := range m.Artifacts {
			if m.Artifacts[i].Kind == kind {
				return &m.Artifacts[i]
			}
		}
		t.Fatalf("沒有 %s", kind)
		return nil
	}
	cases := map[string]struct {
		edit func(m *bundle.OutManifest, files map[string][]byte)
		want string
	}{
		"沒有被引用的圖片": {func(m *bundle.OutManifest, files map[string][]byte) {
			for p, d := range files {
				if strings.HasSuffix(p, ".jpg") {
					files["artifacts/orphan.jpg"] = d
					return
				}
			}
		}, "沒有被任何產出引用"},
		"選中的畫面沒有圖片": {func(m *bundle.OutManifest, files map[string][]byte) {
			for p := range files {
				if strings.HasSuffix(p, "-001.jpg") {
					delete(files, p)
				}
			}
		}, "沒有圖片"},
		"章節有未定義的欄位": {func(m *bundle.OutManifest, files map[string][]byte) {
			files[find(m, KindChapters).Path] = []byte(`{"origin":"platform","chapters":[],"x":1}`)
		}, "章節 JSON"},
		"章節的時間倒退": {func(m *bundle.OutManifest, files map[string][]byte) {
			files[find(m, KindChapters).Path] = []byte(`{"origin":"platform","chapters":[{"title":"a","start_ms":5,"end_ms":1}]}`)
		}, "章節 JSON"},
		"贊助片段不正確": {func(m *bundle.OutManifest, files map[string][]byte) {
			files[find(m, KindSponsorSegments).Path] = []byte(`{"segments":[{"category":"s","start_ms":-1,"end_ms":2}]}`)
		}, "贊助片段 JSON"},
		"frames 的畫面編號超出範圍": {func(m *bundle.OutManifest, files map[string][]byte) {
			a := find(m, KindFrames)
			files[a.Path] = []byte(`{"candidates":[{"n":1000,"at_ms":0,"score":0,"file":"../../x","thumb":"","selected":true}]}`)
		}, "frames JSON"},
		"兩個相同的產出": {func(m *bundle.OutManifest, files map[string][]byte) {
			a := *find(m, KindChapters)
			a.ID, a.Path = "0190a1b2-0000-7000-8000-0000000000ff", "artifacts/0190a1b2-0000-7000-8000-0000000000ff.json"
			files[a.Path] = files[find(m, KindChapters).Path]
			m.Artifacts = append(m.Artifacts, a)
		}, "同一種產出"},
		"筆記含 NUL": {func(m *bundle.OutManifest, files map[string][]byte) {
			files[find(m, KindNote).Path] = []byte("a\x00b")
		}, "UTF-8 筆記"},
		"Bilibili 沒有分P": {func(m *bundle.OutManifest, files map[string][]byte) {
			m.Item.Platform = "bilibili"
		}, "item.part"},
		"本機影音的 external_id 不是 sha256": {func(m *bundle.OutManifest, files map[string][]byte) {
			m.Item.Platform = "local"
		}, "item.external_id"},
	}
	dst := newRig(t)
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data := rezip(t, ex.Data, c.edit)
			_, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: data})
			var be *bundle.Error
			if !errors.As(err, &be) || be.Code != bundle.CodeInvalid || !strings.Contains(be.Message, c.want) {
				t.Fatalf("err = %v，預期含 %q", err, c.want)
			}
		})
	}
	if items, _ := dst.st.Read().ListItems(ctx); len(items) != 0 {
		t.Errorf("被拒絕的 bundle 不該留下影音：%d", len(items))
	}
	if ents, _ := os.ReadDir(filepath.Join(dst.dir, "artifacts")); len(ents) != 0 {
		t.Errorf("被拒絕的 bundle 不該留下檔案：%d", len(ents))
	}
	// 不是 zip、版本不符直接由 internal/bundle 拒絕。
	if _, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: []byte("nope")}); err == nil {
		t.Error("不是 zip 應該失敗")
	}
}

func TestImportBundleLocalItem(t *testing.T) {
	src, _ := bundleRig(t)
	ctx := context.Background()
	ex, err := src.p.ExportBundle(ctx, "yt:abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("ab", 32)
	data := rezip(t, ex.Data, func(m *bundle.OutManifest, _ map[string][]byte) {
		m.Item.Platform, m.Item.ExternalID, m.Item.Channel = "local", sha, nil
		name := "../lecture.mp4"
		m.Item.SourceName = &name
	})
	dst := newRig(t)
	res, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "local:lecture.mp4" || res.Item.ContentSHA256 != sha || res.Item.SourcePath != "" {
		t.Fatalf("res = %+v", res)
	}
	// 同樣內容的檔案再匯入時以 sha256 比對到既有的影音。
	var ce *BundleConflictError
	if _, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: data}); !errors.As(err, &ce) || !strings.Contains(ce.Message, "local:lecture.mp4") {
		t.Fatalf("重複 = %v", err)
	}
	// 匯出本機影音：external_id 是 sha256，source_name 是檔名。
	ex2, err := dst.p.ExportBundle(ctx, res.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Read(ex2.Data, bundle.Options{})
	if err != nil || b.Manifest.Item.ExternalID != sha || b.Manifest.Item.SourceName != "lecture.mp4" {
		t.Fatalf("manifest = %+v %v", b.Manifest.Item, err)
	}
}

func TestExportBundleStopsOnLeak(t *testing.T) {
	r, it := bundleRig(t)
	ctx := context.Background()
	// 字幕提到資料目錄是使用者的內容，可以匯出。
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantEdited, Lang: "en",
		Cues: []subtitle.Cue{{StartMS: 0, EndMS: 1, Text: "see " + r.dir + "/secret"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.ExportBundle(ctx, it.ID); err != nil {
		t.Fatalf("字幕含路徑：%v", err)
	}
	// 標題（manifest 的欄位）含資料目錄：不送出。
	if err := r.st.Tx(ctx, func(tx *store.Repo) error {
		return tx.UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: "x " + r.dir + "/y"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.ExportBundle(ctx, it.ID); !errors.Is(err, ErrBundleLeak) {
		t.Fatalf("標題含路徑 = %v", err)
	}
	// secret 的值出現在任何項目都不送出。
	t.Setenv("NOTESTEME_TEST_API_KEY", "sk-leak-0123456789")
	if err := r.st.Tx(ctx, func(tx *store.Repo) error { return tx.UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: "T"}) }); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantEdited, Lang: "en",
		Cues: []subtitle.Cue{{StartMS: 0, EndMS: 1, Text: "key sk-leak-0123456789"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.ExportBundle(ctx, it.ID); !errors.Is(err, ErrBundleLeak) {
		t.Fatalf("secret = %v", err)
	}
	if _, err := r.p.ExportBundle(ctx, "yt:nope0000000"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在 = %v", err)
	}
}

func TestContainsPath(t *testing.T) {
	for _, c := range []struct {
		hay, needle string
		want        bool
	}{
		{`"/data/x"`, "/data", true},
		{`"/data"`, "/data", true},
		{`/database`, "/data", false},
		{`/home/u/data`, "/data", false},
		{`x /data\\y`, "/data", true},
		{`/datax /data.`, "/data", false},
		{`/datax /data `, "/data", true},
		{``, "/data", false},
	} {
		if got := containsPath([]byte(c.hay), c.needle); got != c.want {
			t.Errorf("containsPath(%q, %q) = %v", c.hay, c.needle, got)
		}
	}
}

func TestBundleFileName(t *testing.T) {
	for in, want := range map[string]string{
		"yt:abc":                "yt-abc.notesteme.zip",
		"bili:BV1x/p2":          "bili-BV1x-p2.notesteme.zip",
		"local:講座 1.mp4":        "local----1-mp4.notesteme.zip",
		"":                      "item.notesteme.zip",
		strings.Repeat("a", 90): strings.Repeat("a", 80) + ".notesteme.zip",
	} {
		if got := BundleFileName(in); got != want {
			t.Errorf("BundleFileName(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestSanitizeProvenance(t *testing.T) {
	cases := map[string]string{
		`not json`:                             `{}`,
		`[1]`:                                  `{}`,
		`{"cookies":"x","cookies_used":false}`: `{"cookies_used":true}`,
		`{"cookie_file":"","a":1}`:             `{"a":1,"cookies_used":false}`,
		`{"access_token":"t","client_secret":"s","password":"p","auth":"a","b":"ok"}`:                        `{"b":"ok"}`,
		`{"u":"http://localhost:1/x","v":"https://[::1]/x","w":"https://example.com/a?b=c#d","x":"ftp://h"}`: `{"w":"https://example.com/a"}`,
		`{"p":"~/x/y.txt","q":"\\\\server\\share\\f.txt","r":"relative/ok"}`:                                 `{"p":"y.txt","q":"f.txt","r":"relative/ok"}`,
		`{"n":[{"api_key":"k","x":"/a/b"}]}`:                                                                 `{"n":[{"x":"b"}]}`,
		`{"input_tokens":3,"max_tokens":4}`:                                                                  `{"input_tokens":3,"max_tokens":4}`,
	}
	for in, want := range cases {
		got := string(sanitizeProvenance(json.RawMessage(in)))
		if got != want {
			t.Errorf("sanitizeProvenance(%s) = %s，預期 %s", in, got, want)
		}
	}
}

func TestImportProvenanceOverridesBundleFields(t *testing.T) {
	raw := json.RawMessage(`{"executor":"agent","verified":true,"model_verified":true,"imported_from":{"instance_id":"evil"}}`)
	out, err := importProvenance(raw, bundle.CreatedBy{InstanceID: "i", AppVersion: "v"}, "s", bundleTestNow)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	from := m["imported_from"].(map[string]any)
	if m["verified"] != false || m["model_verified"] != false || from["instance_id"] != "i" || from["bundle_sha256"] != "s" {
		t.Errorf("provenance = %s", out)
	}
	out, _ = importProvenance(json.RawMessage(`{"executor":"api","model_verified":true}`), bundle.CreatedBy{}, "s", bundleTestNow)
	if strings.Contains(string(out), "model_verified") {
		t.Errorf("非 agent 的 model_verified 應移除：%s", out)
	}
}

var bundleTestNow = time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

func FuzzSanitizeProvenance(f *testing.F) {
	for _, s := range []string{`{"a":"/x/y"}`, `{"cookie":1}`, `{"u":"http://10.1.1.1/"}`, `{"n":[[{"token":1}]]}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := sanitizeProvenance(json.RawMessage(in))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("輸出不是 JSON 物件：%s", out)
		}
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, y := range x {
					if secretKey.MatchString(k) || (cookieKey.MatchString(k) && k != "cookies_used") {
						t.Fatalf("留下了 %q：%s", k, out)
					}
					walk(y)
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			case string:
				if strings.HasPrefix(x, "/") || windowsPath.MatchString(x) {
					t.Fatalf("留下了路徑 %q：%s", x, out)
				}
			}
		}
		walk(m)
	})
}

// FuzzImportBundle：任何輸入都不能 panic；失敗時一律是 *bundle.Error 或 *BundleConflictError，而且不留下暫存目錄與產出檔。
func FuzzImportBundle(f *testing.F) {
	var cues bytes.Buffer
	if err := subtitle.RenderCuesJSON(&cues, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantCorrected,
		Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "hi", Timing: subtitle.TimingSource}}}); err != nil {
		f.Fatal(err)
	}
	lang := "en"
	id := "0190a1b2-0000-7000-8000-0000000000a1"
	chap := []byte(`{"origin":"platform","chapters":[{"title":"a","start_ms":0,"end_ms":5}]}`)
	seed, err := bundle.Write(bundle.OutManifest{
		Item: bundle.OutItem{Platform: "youtube", ExternalID: "abc123xxxxx", Title: "T", Availability: "public"},
		Artifacts: []bundle.OutArtifact{
			{ID: id, Kind: "transcript", Variant: "corrected", Lang: &lang, Format: bundle.CuesFormat, Path: "artifacts/" + id + ".json", Provenance: json.RawMessage(`{"executor":"agent"}`)},
			{ID: "0190a1b2-0000-7000-8000-0000000000a2", Kind: "chapters", Format: chaptersFmt, Path: "artifacts/c.json", Parents: []string{id}, Provenance: json.RawMessage(`{"a":1}`)},
		},
		CreatedBy: bundle.OutCreatedBy{InstanceID: "0190a1b2-0000-7000-8000-000000000001", AppVersion: "0.4.0"},
	}, []bundle.OutEntry{{Path: "artifacts/" + id + ".json", Data: cues.Bytes()}, {Path: "artifacts/c.json", Data: chap}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dst := newRig(t)
		_, err := dst.p.ImportBundle(context.Background(), BundleImportRequest{Data: data})
		var be *bundle.Error
		var ce *BundleConflictError
		if err != nil && !errors.As(err, &be) && !errors.As(err, &ce) {
			t.Fatalf("非預期的錯誤：%v", err)
		}
		if bytes.Equal(data, seed) && err != nil {
			t.Fatalf("合法的種子被拒絕：%v", err)
		}
		if ents, _ := os.ReadDir(filepath.Join(dst.dir, "tmp")); len(ents) != 0 {
			t.Fatalf("tmp 留下 %d 個項目", len(ents))
		}
		if err != nil {
			if ents, _ := os.ReadDir(filepath.Join(dst.dir, "artifacts")); len(ents) != 0 {
				t.Fatalf("失敗時留下 %d 個產出目錄", len(ents))
			}
		}
	})
}

// 影音的 metadata（頻道、發布日期、片長、原文語言）匯入後寫進影音，再匯出時原樣出現在 manifest。
func TestBundleItemMetadataRoundTrip(t *testing.T) {
	src, _ := bundleRig(t)
	ctx := context.Background()
	ex, err := src.p.ExportBundle(ctx, "yt:abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}
	data := rezip(t, ex.Data, func(m *bundle.OutManifest, _ map[string][]byte) {
		title, pub, lang, dur := "頻道", "2026-11-01", "zh-Hant", 1935.0
		m.Item.Channel = &bundle.OutChannel{ExternalID: "UCxyz", Title: &title}
		m.Item.PublishedAt, m.Item.OriginalLang, m.Item.DurationS = &pub, &lang, &dur
		m.Item.Title = strings.Repeat("長", 500)
	})
	dst := newRig(t)
	res, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	it := res.Item
	if it.ChannelID == "" || it.PublishedAt == nil || it.PublishedAt.Format("2006-01-02") != "2026-11-01" ||
		it.DurationS == nil || *it.DurationS != 1935 || it.OriginalLang != "zh-Hant" || it.Availability != "unknown" {
		t.Fatalf("item = %+v", it)
	}
	ex2, err := dst.p.ExportBundle(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Read(ex2.Data, bundle.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mi := b.Manifest.Item
	if mi.Channel.ExternalID != "UCxyz" || mi.Channel.Title != "頻道" || mi.PublishedAt != "2026-11-01" || mi.DurationS == nil ||
		*mi.DurationS != 1935 || mi.OriginalLang != "zh-Hant" || len([]rune(mi.Title)) != 500 {
		t.Errorf("manifest item = %+v", mi)
	}
	if _, ok := parseBundleDate("2026-11-01T10:00:00Z"); !ok {
		t.Error("RFC 3339 的時間應該接受")
	}
	if _, ok := parseBundleDate("yesterday"); ok {
		t.Error("不正確的日期應該略過")
	}
	for s, want := range map[string]bool{"a.mp4": true, "": false, "..": false, "a/b": false, "a\x01": false} {
		if validLocalName(s) != want {
			t.Errorf("validLocalName(%q) != %v", s, want)
		}
	}
	if truncateRunes("ab\xffc", 2) != "ab" {
		t.Error("truncateRunes")
	}
}

// add_versions 匯入 transcript 等同從 transcript 重跑：結束既有的重新 Probe 並清除更好的來源（SRC-R18）；
// 只匯入較後面的步驟（筆記）時不動重新 Probe。
func TestImportBundleEndsReprobe(t *testing.T) {
	src, _ := bundleRig(t)
	ctx := context.Background()
	ex, err := src.p.ExportBundle(ctx, "yt:abc123xxxxx")
	if err != nil {
		t.Fatal(err)
	}
	dst := newRig(t)
	res, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: ex.Data})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Item.ID
	arm := func() {
		t.Helper()
		if err := dst.st.Tx(ctx, func(r *store.Repo) error {
			if err := r.SaveReprobe(ctx, store.Reprobe{ItemID: id, Until: time.Now().Add(time.Hour), Sources: true, SponsorBlock: true,
				Better: &store.BetterSource{Source: "youtube_manual", Lang: "en", Kind: "manual", FoundAt: time.Now().UTC()}}); err != nil {
				return err
			}
			return r.ScheduleJob(ctx, store.ReprobeKind, id, time.Now().Add(time.Hour))
		}); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(kind, from, to string) []byte {
		return rezip(t, ex.Data, func(m *bundle.OutManifest, files map[string][]byte) {
			for _, a := range m.Artifacts {
				if a.Kind != kind {
					continue
				}
				files[a.Path] = bytes.ReplaceAll(files[a.Path], []byte(from), []byte(to))
			}
		})
	}
	arm()
	// 只有筆記不同：重新 Probe 維持原狀。
	if r2, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: edit("note", "Body", "Other body"), AddVersions: true}); err != nil || r2.Imported != 1 {
		t.Fatalf("匯入筆記 = %+v %v", r2, err)
	}
	rp, err := dst.st.Read().Reprobe(ctx, id)
	if err != nil || !rp.Sources || !rp.SponsorBlock || rp.Better == nil {
		t.Fatalf("只匯入筆記時 reprobe = %+v %v", rp, err)
	}
	// transcript 不同：字幕與 SponsorBlock 的部分都結束，排程刪除。
	if r3, err := dst.p.ImportBundle(ctx, BundleImportRequest{Data: edit("transcript", "Hello one.", "Hello uno."), AddVersions: true}); err != nil || r3.Imported == 0 {
		t.Fatalf("匯入字幕 = %+v %v", r3, err)
	}
	rp, err = dst.st.Read().Reprobe(ctx, id)
	if err != nil || rp.Sources || rp.SponsorBlock || rp.Better != nil {
		t.Fatalf("匯入字幕後 reprobe = %+v %v", rp, err)
	}
	if _, err := dst.st.Read().ScheduledJob(ctx, store.ReprobeKind, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("排程仍在：%v", err)
	}
}
