package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/transcript"
)

const (
	videoStreams = `{"index":0,"codec_type":"video","codec_name":"h264"},{"index":1,"codec_type":"audio","codec_name":"aac"}`
	srtEN        = "1\n00:00:00,000 --> 00:00:02,000\nhello\n\n2\n00:00:02,000 --> 00:00:04,000\nworld\n"
	vttJA        = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nこんにちは\n"
)

// localRig 是本機影音的測試環境：inbox 目錄（受 Guard 限制的根目錄）、假 ffmpeg 與 ffprobe。
type localRig struct {
	*rig
	root string // 根目錄（Guard 允許的範圍）
	fm   *fakeMedia
}

func newLocalRig(t *testing.T, opts ...func(*Deps)) *localRig {
	t.Helper()
	root := t.TempDir()
	fm, tool := newFakeMedia(t)
	fm.streams = videoStreams
	r := newRig(t, append([]func(*Deps){func(d *Deps) {
		d.Media = tool
		// 與 app 的接線相同：根目錄加上 <data>/uploads。
		d.Files = pathguard.New([]string{root}).With(filepath.Join(d.DataDir, "uploads"))
	}}, opts...)...)
	r.fm = fm
	return &localRig{rig: r, root: root, fm: fm}
}

// file 在根目錄寫一個檔案，回傳絕對路徑。
func (l *localRig) file(name, content string) string {
	l.t.Helper()
	p := filepath.Join(l.root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		l.t.Fatal(err)
	}
	return p
}

// item 在資料庫建立（或取回）指向 sourcePath 的本機影音；runTranscript 寫產出檔時會確認影音存在。
func (l *localRig) item(sourcePath string) store.Item {
	l.t.Helper()
	it, _, err := l.st.Write().InsertItemIfAbsent(context.Background(), store.NewItem{
		Platform: "local", ExternalID: filepath.Base(sourcePath), SourcePath: sourcePath, Origin: store.OriginManual,
	})
	if err != nil {
		l.t.Fatal(err)
	}
	return it
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func trackLangs(ts []transcript.Track) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Kind+":"+t.Lang)
	}
	slices.Sort(out)
	return out
}

func TestLocalProbeFindsSidecars(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	l.file("talk.en.srt", srtEN)
	l.file("talk.ja.vtt", vttJA)
	l.file("talk.srt", srtEN)          // 沒有語言代碼：不採用
	l.file("other.en.srt", srtEN)      // 別的影音的字幕
	l.file("talk.notalang123.srt", "") // 語言代碼不合法
	l.file("talk.en.txt", "")          // 副檔名不對
	l.file("talk.zh-Hant.srt", srtEN)

	ad, err := l.p.adapterFor("local")
	if err != nil {
		t.Fatal(err)
	}
	info, err := ad.probe(context.Background(), l.item(media))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sidecar:en", "sidecar:ja", "sidecar:zh-Hant"}
	if got := trackLangs(info.Tracks); !reflect.DeepEqual(got, want) {
		t.Errorf("tracks = %v，預期 %v", got, want)
	}
	if info.Title != "talk" || info.MediaKind != "video" || info.Live || info.Availability != "" {
		t.Errorf("probed = %+v", info)
	}
	if info.ContentSHA256 != sha("MEDIA") || info.ComputedSHA256 != sha("MEDIA") {
		t.Errorf("sha = %q / %q，預期 %q", info.ContentSHA256, info.ComputedSHA256, sha("MEDIA"))
	}
	if info.DurationS == nil || *info.DurationS != 600 {
		t.Errorf("duration = %v", info.DurationS)
	}
	// ffprobe 收到的是已解析的絕對路徑。
	last := l.fm.calls[0]
	if last.Tool != "ffprobe" || last.Args[len(last.Args)-1] != resolved(t, media) {
		t.Errorf("ffprobe 呼叫 = %+v", last)
	}

	// 讀取 sidecar：.srt 與 .vtt 各自解析。
	for _, tr := range info.Tracks {
		cues, err := ad.fetchCues(context.Background(), l.item(media), tr, t.TempDir())
		if err != nil || len(cues) == 0 {
			t.Errorf("fetchCues(%+v) = %v, %v", tr, cues, err)
		}
	}
	for _, tr := range info.Tracks {
		if tr.Lang == "en" {
			cues, _ := ad.fetchCues(context.Background(), l.item(media), tr, t.TempDir())
			if len(cues) != 2 || cues[1].Text != "world" {
				t.Errorf("en cues = %+v", cues)
			}
		}
	}
}

// 外掛字幕檔名的語言碼要正規化成 BCP-47：eng → en、EN → en、zh-hant → zh-Hant。
func TestLocalSidecarLangIsNormalized(t *testing.T) {
	for name, want := range map[string]string{
		"movie.eng.srt":        "en",
		"movie.EN.srt":         "en",
		"movie.zh-hant.vtt":    "zh-Hant",
		"movie.zh-hant-tw.srt": "zh-Hant-TW",
		"movie.pt-br.srt":      "pt-BR",
		"movie.JPN.SRT":        "ja",
	} {
		t.Run(name, func(t *testing.T) {
			l := newLocalRig(t)
			media := l.file("movie.mp4", "MEDIA")
			l.file(name, srtEN)
			ad, _ := l.p.adapterFor("local")
			info, err := ad.probe(context.Background(), l.item(media))
			if err != nil {
				t.Fatal(err)
			}
			if len(info.Tracks) != 1 || info.Tracks[0].Lang != want {
				t.Errorf("tracks = %+v，預期語言 %q", info.Tracks, want)
			}
		})
	}
}

// 內嵌字幕與外掛字幕走同一個正規化：大小寫與三字母碼。
func TestLocalEmbeddedLangIsNormalized(t *testing.T) {
	l := newLocalRig(t)
	l.fm.streams = videoStreams + `,{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"ZHO"}},
		{"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"zh-hant-tw"}}`
	media := l.file("movie.mkv", "MKV")
	ad, _ := l.p.adapterFor("local")
	info, err := ad.probe(context.Background(), l.item(media))
	if err != nil {
		t.Fatal(err)
	}
	if got := trackLangs(info.Tracks); !reflect.DeepEqual(got, []string{"embedded:zh", "embedded:zh-Hant-TW"}) {
		t.Errorf("tracks = %v", got)
	}
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLocalProbeListsEmbeddedTextSubtitles(t *testing.T) {
	l := newLocalRig(t)
	l.fm.streams = videoStreams + `,
		{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"},"disposition":{"default":1}},
		{"index":3,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"jpn"}},
		{"index":4,"codec_type":"subtitle","codec_name":"mov_text","tags":{"language":"und"},"disposition":{"forced":1}}`
	l.fm.subtitles = map[int]string{2: srtEN}
	media := l.file("movie.mkv", "MKV")

	ad, _ := l.p.adapterFor("local")
	info, err := ad.probe(context.Background(), l.item(media))
	if err != nil {
		t.Fatal(err)
	}
	want := []transcript.Track{
		{Lang: "en", Kind: transcript.KindEmbedded, Handle: "2", Default: true},
		{Lang: "", Kind: transcript.KindEmbedded, Handle: "4", Forced: true},
	}
	if !reflect.DeepEqual(info.Tracks, want) {
		t.Fatalf("tracks = %+v，預期 %+v", info.Tracks, want)
	}

	dir := t.TempDir()
	cues, err := ad.fetchCues(context.Background(), l.item(media), want[0], dir)
	if err != nil || len(cues) != 2 {
		t.Fatalf("fetchCues = %v, %v", cues, err)
	}
	if n := l.fm.countKind("subtitle"); n != 1 {
		t.Fatalf("ExtractSubtitle 呼叫 %d 次", n)
	}
	args := l.fm.ffmpegCalls()[0]
	if argAfter(args, "-map") != "0:2" || args[len(args)-1] != filepath.Join(dir, "embedded.srt") {
		t.Errorf("argv = %q", args)
	}
}

func TestLocalMissingFileIsPermanentNotFound(t *testing.T) {
	l := newLocalRig(t)
	it := l.item(filepath.Join(l.root, "gone.mp4"))
	ad, _ := l.p.adapterFor("local")
	_, err := ad.probe(context.Background(), it)
	assertLocalPermanent(t, l, err, msgLocalNotFound)
	if len(l.fm.calls) != 0 {
		t.Errorf("檔案不存在不該呼叫 ffprobe：%+v", l.fm.calls)
	}
}

func TestLocalOutsideRootsIsPermanent(t *testing.T) {
	l := newLocalRig(t)
	other := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(other, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	ad, _ := l.p.adapterFor("local")
	_, err := ad.probe(context.Background(), l.item(other))
	assertLocalPermanent(t, l, err, msgLocalOutside)
	if len(l.fm.calls) != 0 {
		t.Errorf("根目錄外的檔案不該呼叫 ffprobe：%+v", l.fm.calls)
	}

	// 根目錄內指向根目錄外的 symlink 也被擋下。
	link := filepath.Join(l.root, "link.mp4")
	if err := os.Symlink(other, link); err != nil {
		t.Skip("無法建立 symlink：", err)
	}
	_, err = ad.probe(context.Background(), l.item(link))
	assertLocalPermanent(t, l, err, msgLocalOutside)
}

func TestLocalDirectoryIsPermanentNotRegular(t *testing.T) {
	l := newLocalRig(t)
	dir := filepath.Join(l.root, "folder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ad, _ := l.p.adapterFor("local")
	_, err := ad.probe(context.Background(), l.item(dir))
	assertLocalPermanent(t, l, err, msgLocalNotRegular)
}

// assertLocalPermanent 確認 err 是 permanent，而且 classify 之後步驟狀態的訊息是 msg。
func assertLocalPermanent(t *testing.T, l *localRig, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatal("預期錯誤")
	}
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassPermanent {
		t.Fatalf("err = %v，應為 permanent", err)
	}
	res := l.p.classify(store.Task{MaxAttempts: 3}, err)
	if res.task != taskFailed || res.state.Status != store.StatusFailed || res.state.Message != msg {
		t.Errorf("classify = %+v，預期 failed 與訊息 %q", res, msg)
	}
}

func TestLocalUploadUsesSourcePathRelativeToDataDir(t *testing.T) {
	l := newLocalRig(t)
	rel := "uploads/item-local/talk.m4a"
	full := filepath.Join(l.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.fm.streams = `{"index":0,"codec_type":"audio","codec_name":"aac"}`
	it := store.Item{ID: "item-local", Platform: "local", ExternalID: "talk.m4a", SourcePath: rel, Origin: store.OriginUpload}

	res, err := l.p.runMetadata(context.Background(), it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	m := res.meta.item
	if m.Title != "talk" || m.MediaKind != "audio" || m.ContentSHA256 != sha("AUDIO") {
		t.Errorf("meta = %+v", m)
	}
	if !res.advance || res.state.Status != store.StatusDone {
		t.Errorf("result = %+v", res)
	}
	if got := l.fm.calls[0].Args; got[len(got)-1] != resolved(t, full) {
		t.Errorf("ffprobe 路徑 = %q", got[len(got)-1])
	}
}

// DEL-R11：metadata 步驟補算 content_sha256 並寫入；影音已有 sha256 時不重算、不覆蓋。
func TestLocalMetadataWritesContentSHA(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	ctx := context.Background()

	res, err := l.p.runMetadata(ctx, l.item(media), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.meta.item.ContentSHA256 != sha("MEDIA") || res.meta.item.MediaKind != "video" {
		t.Errorf("meta = %+v", res.meta.item)
	}
	if res.meta.item.Title != "talk" || res.meta.item.OriginalLang != "" {
		t.Errorf("meta = %+v", res.meta.item)
	}

	// 已有 sha256（inbox 新增時算好的）：不重算，也不覆蓋。
	it := l.item(media)
	it.ContentSHA256 = "abc"
	res, err = l.p.runMetadata(ctx, it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.meta.item.ContentSHA256 != "" {
		t.Errorf("不該覆蓋既有的 sha256：%+v", res.meta.item)
	}

	// 沒有 sha256 的影音：寫回資料庫之後 tombstone 才有 sha（apply 的路徑）。
	created := l.item(media)
	res, err = l.p.runMetadata(ctx, created, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.st.Write().UpdateItemMeta(ctx, created.ID, res.meta.item); err != nil {
		t.Fatal(err)
	}
	got, err := l.st.Read().ItemByID(ctx, created.ID)
	if err != nil || got.ContentSHA256 != sha("MEDIA") || got.MediaKind != "video" {
		t.Errorf("item = %+v, %v", got, err)
	}
}

func TestLocalMetadataFailsWhenFileMissing(t *testing.T) {
	l := newLocalRig(t)
	_, err := l.p.runMetadata(context.Background(), l.item(filepath.Join(l.root, "nope.mp4")), DefaultLanguage())
	assertLocalPermanent(t, l, err, "找不到這個檔案。")
}

func TestLocalTranscriptUsesSidecarAndInfersLanguage(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	l.file("talk.ja.srt", srtEN)
	it := l.item(media)
	it.Metadata = json.RawMessage(`{"description":"keep me"}`)

	res, err := l.p.runTranscript(context.Background(), it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.state.Status != store.StatusDone || res.artifact == nil || res.artifact.Lang != "ja" {
		t.Fatalf("result = %+v", res)
	}
	// 原文語言由選中的本機字幕推斷，lang_source 為 metadata，並保留 metadata_json。
	if res.meta == nil || res.meta.item.OriginalLang != "ja" || res.meta.item.LangSource != "metadata" ||
		string(res.meta.item.Metadata) != `{"description":"keep me"}` {
		t.Errorf("meta = %+v", res.meta)
	}
	var prov struct {
		Source string `json:"source"`
		Timing string `json:"timing"`
	}
	if err := json.Unmarshal(res.artifact.Provenance, &prov); err != nil || prov.Source != "local-sidecar" || prov.Timing != "cue" {
		t.Errorf("provenance = %s, %v", res.artifact.Provenance, err)
	}
	if len(res.probes) != 1 || res.probes[0].SourceID != "local-sidecar" || res.probes[0].State != "used" {
		t.Errorf("probes = %+v", res.probes)
	}
}

func TestLocalTranscriptUsesEmbeddedWhenNoSidecar(t *testing.T) {
	l := newLocalRig(t)
	l.fm.streams = videoStreams + `,{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"},"disposition":{"default":1}}`
	l.fm.subtitles = map[int]string{2: srtEN}
	media := l.file("lecture.mp4", "MEDIA")

	res, err := l.p.runTranscript(context.Background(), l.item(media), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.artifact == nil || res.artifact.Lang != "en" || res.meta == nil || res.meta.item.OriginalLang != "en" {
		t.Fatalf("result = %+v", res)
	}
	var sources []string
	for _, p := range res.probes {
		sources = append(sources, p.SourceID+":"+p.State+":"+p.Reason)
	}
	want := []string{"local-sidecar:unavailable:no_track", "local-embedded:used:"}
	if !reflect.DeepEqual(sources, want) {
		t.Errorf("probes = %v，預期 %v", sources, want)
	}
}

// 本機影音的快取鍵：Provider 為 local，內容（sha256）改變時不能命中。
func TestLocalTranscriptCacheKeyDependsOnContent(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	l.file("talk.en.srt", srtEN)
	ctx := context.Background()

	key := func(content string) string {
		if err := os.WriteFile(media, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := l.p.runTranscript(ctx, l.item(media), DefaultLanguage())
		if err != nil {
			t.Fatal(err)
		}
		return res.stage.CacheKey
	}
	a, a2, b := key("MEDIA"), key("MEDIA"), key("OTHER")
	if a != a2 || a == b {
		t.Errorf("快取鍵：同內容 %s / %s，不同內容 %s", a, a2, b)
	}
}

// Supadata 不處理本機檔案：記為 unavailable／unsupported，不送出任何請求，也不要求使用者設定 API key。
func TestSupadataIsUnsupportedForLocal(t *testing.T) {
	sd := &fakeSupadata{lang: "en", cues: 2}
	l := newLocalRig(t, func(d *Deps) { d.Supadata = sd })
	media := l.file("talk.mp4", "MEDIA")
	lang := DefaultLanguage()
	lang.Fallback = []string{"supadata"}

	res, err := l.p.runTranscript(context.Background(), l.item(media), lang)
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.urls) != 0 {
		t.Fatalf("不該送出請求：%v", sd.urls)
	}
	if res.state.Status != store.StatusFailed || res.state.Message != msgNoSource {
		t.Errorf("state = %+v，預期失敗（SRC-R5）而不是 blocked", res.state)
	}
	var got []string
	for _, p := range res.probes {
		got = append(got, p.SourceID+":"+p.State+":"+p.Reason)
	}
	want := []string{
		"local-sidecar:unavailable:no_track", "local-embedded:unavailable:no_track", "supadata:unavailable:unsupported",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("probes = %v，預期 %v", got, want)
	}
}

// 預設的 fallback [supadata, groq]：本機影音略過 Supadata，用 Groq 轉錄原檔（先前處理成 opus）。
func TestLocalFallsBackToGroqAfterUnsupportedSupadata(t *testing.T) {
	sd := &fakeSupadata{lang: "en", cues: 2}
	g := &fakeGroq{lang: "ja", baseURL: "u", model: "m"}
	l := newLocalRig(t, func(d *Deps) { d.Supadata = sd; d.Groq = g })
	media := l.file("talk.mp4", "MEDIA")

	if !transcriptNeedsASR("local", DefaultLanguage(), nil, "") {
		t.Error("本機影音略過 Supadata 之後第一個來源是 ASR：資源類別應為 asr")
	}
	res, err := l.p.runTranscript(context.Background(), l.item(media), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.urls) != 0 || g.calls() != 1 {
		t.Fatalf("supadata = %v，groq = %d", sd.urls, g.calls())
	}
	prep := l.fm.ffmpegCalls()[0]
	if ffmpegKind(prep) != "prepare" || argAfter(prep, "-i") != resolved(t, media) {
		t.Errorf("前處理 argv = %q", prep)
	}
	var prov struct {
		Source       string            `json:"source"`
		ToolVersions map[string]string `json:"tool_versions"`
	}
	if err := json.Unmarshal(res.artifact.Provenance, &prov); err != nil || prov.Source != "groq" || prov.ToolVersions["ffmpeg"] != "6.1.1" {
		t.Errorf("provenance = %s, %v", res.artifact.Provenance, err)
	}
	var got []string
	for _, p := range res.probes {
		got = append(got, p.SourceID+":"+p.State+":"+p.Reason)
	}
	if got[len(got)-2] != "supadata:unavailable:unsupported" || got[len(got)-1] != "groq:used:" {
		t.Errorf("probes = %v", got)
	}
	// SRC-R11：沒有字幕可以推斷原文語言，ASR 回報的語言寫回（lang_source 為 asr）。
	if res.meta == nil || res.meta.item.OriginalLang != "ja" || res.meta.item.LangSource != "asr" {
		t.Errorf("meta = %+v", res.meta)
	}
}

func TestLocalAdapterRequiresGuard(t *testing.T) {
	r := newRig(t)
	if _, err := r.p.adapterFor("local"); err == nil {
		t.Error("沒有 Files 時不支援本機影音")
	}
}
