package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// newRenderItem 建立一部做到 metadata 的影音（原文語言 en），之後的字幕靠 seedVariant 寫入。
func newRenderItem(t *testing.T, r *rig) store.Item {
	t.Helper()
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	it, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestRenderInputBilingual(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantSource, "en", "raw one", "raw two")
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We use Kafka.", "It is a log.")
	seedVariant(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", "我們用 Kafka。", "它是日誌。")

	for _, lang := range []string{"zh-Hant-TW", ""} { // 沒指定語言時，目標是最近的譯文
		in, err := r.p.RenderInput(ctx, it.ID, CuesQuery{Lang: lang, ToMS: -1})
		if err != nil {
			t.Fatal(err)
		}
		if in.Main.Variant != subtitle.VariantTranslated || in.Main.Lang != "zh-Hant-TW" || in.Orig == nil ||
			in.Orig.Variant != subtitle.VariantCorrected || in.Orig.Lang != "en" || in.Orig.Cues[0].Text != "We use Kafka." {
			t.Errorf("lang=%q：Main=%+v Orig=%+v", lang, in.Main, in.Orig)
		}
	}
	// 要原文語言本身：單語（譯文不能當原文）。
	in, err := r.p.RenderInput(ctx, it.ID, CuesQuery{Lang: "en", ToMS: -1})
	if err != nil || in.Orig != nil || in.Main.Variant != subtitle.VariantCorrected {
		t.Errorf("lang=en：%+v，%v", in, err)
	}
	// 時間範圍同時套用在原文。
	in, err = r.p.RenderInput(ctx, it.ID, CuesQuery{Lang: "zh-Hant-TW", FromMS: 2500, ToMS: -1})
	if err != nil || len(in.Main.Cues) != 1 || len(in.Orig.Cues) != 1 || in.Orig.Cues[0].Text != "It is a log." {
		t.Errorf("from：%+v，%v", in, err)
	}
	if _, err := r.p.RenderInput(ctx, it.ID, CuesQuery{Lang: "ja", ToMS: -1}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("沒有這個語言的字幕：%v", err)
	}
}

// 要輸出的是影音的原文語言時，匯入的外語版不會被當成原文配成雙語。
func TestRenderInputOriginalLanguageIsMonolingual(t *testing.T) {
	r := newRig(t)
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We use Kafka.")
	seedVariant(t, r, it, subtitle.VariantEdited, "zh-Hant-TW", "我們用 Kafka。")
	in, err := r.p.RenderInput(context.Background(), it.ID, CuesQuery{Lang: "en", ToMS: -1})
	if err != nil || in.Orig != nil || in.Main.Lang != "en" {
		t.Errorf("Orig 應為 nil：%+v，%v", in, err)
	}
	in, err = r.p.RenderInput(context.Background(), it.ID, CuesQuery{Lang: "zh-Hant-TW", ToMS: -1})
	if err != nil || in.Orig == nil || in.Orig.Lang != "en" {
		t.Errorf("匯入的譯文應與原文配成雙語：%+v，%v", in, err)
	}
}

// 原文與目標語言相同（包括只差字形）時只輸出單語；原文語言取自原文那一邊的產出，不看 items.original_lang（這裡是 en）。
func TestRenderInputSameLanguageIsMonolingual(t *testing.T) {
	r := newRig(t)
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "zh-Hans", "这是一部视频")
	seedVariant(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", "這是一部影片")
	in, err := r.p.RenderInput(context.Background(), it.ID, CuesQuery{Lang: "zh-Hant-TW", ToMS: -1})
	if err != nil || in.Orig != nil || in.Main.Lang != "zh-Hant-TW" {
		t.Errorf("Orig 應為 nil：%+v，%v", in, err)
	}
}

func TestRenderInputPrefersEditedOriginal(t *testing.T) {
	r := newRig(t)
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "corrected")
	seedVariant(t, r, it, subtitle.VariantEdited, "en", "edited by hand")
	seedVariant(t, r, it, subtitle.VariantTranslated, "ja", "翻訳")
	in, err := r.p.RenderInput(context.Background(), it.ID, CuesQuery{Lang: "ja", ToMS: -1})
	if err != nil || in.Orig == nil || in.Orig.Variant != subtitle.VariantEdited {
		t.Errorf("原文應是 edited：%+v，%v", in.Orig, err)
	}
}

func TestResolveItem(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := newRenderItem(t, r)
	for _, s := range []string{it.ID, "yt:abc123xxxxx"} {
		got, err := r.p.ResolveItem(ctx, s)
		if err != nil || got.ID != it.ID {
			t.Errorf("ResolveItem(%q) = %v，%v", s, got.ID, err)
		}
	}
	for _, s := range []string{"yt:zzzzzzzzzzz", "nope", "local:x.mp4"} {
		if _, err := r.p.ResolveItem(ctx, s); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ResolveItem(%q) err = %v，預期 ErrNotFound", s, err)
		}
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var tr *InTrashError
	if _, err := r.p.ResolveItem(ctx, "yt:abc123xxxxx"); !errors.As(err, &tr) {
		t.Errorf("垃圾桶中的影音 err = %v", err)
	}
}

const importedSRT = "1\n00:00:00,000 --> 00:00:02,000\nWe use Apache Kafka.\n\n2\n00:00:02,000 --> 00:00:04,000\nIt is a log.\n"

func TestImportSubtitle(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We use Kafka.", "It is a log.")
	seedVariant(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", "我們用 Kafka。", "它是日誌。")
	before := r.stage(it, StageTranslate)
	if before.Status != store.StatusDone || before.Message != "" {
		t.Fatalf("匯入前的 translate：%+v", before)
	}

	res, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(importedSRT), Lang: "en", Name: "/some/dir/fixed.srt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "srt" || res.Cues != 2 || !res.Flagged || res.Artifact.Variant != "edited" || res.Artifact.Lang != "en" {
		t.Fatalf("結果 = %+v", res)
	}
	var prov map[string]any
	if err := json.Unmarshal(res.Artifact.Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	from, _ := prov["imported_from"].(map[string]any)
	if prov["executor"] != "human" || prov["stage"] != "import" || from["format"] != "srt" || from["name"] != "fixed.srt" || len(from["sha256"].(string)) != 64 {
		t.Errorf("來源紀錄 = %v", prov)
	}
	if strings.Contains(string(res.Artifact.Provenance), "/some/dir") {
		t.Error("來源紀錄不應含檔案的目錄")
	}
	if len(res.Artifact.Parents) != 1 {
		t.Errorf("parents = %v，預期上游的 corrected", res.Artifact.Parents)
	} else if up, _ := r.st.Read().Artifact(ctx, res.Artifact.Parents[0]); up.Variant != "corrected" {
		t.Errorf("上游 = %+v", up)
	}

	// 內容是 edited 變體，預設取用時優先（OUT-R2）。
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Lang: "en", ToMS: -1})
	if err != nil || tr.Variant != subtitle.VariantEdited || tr.Cues[0].Text != "We use Apache Kafka." || tr.Cues[0].Timing != subtitle.TimingSource {
		t.Errorf("取用 = %+v，%v", tr, err)
	}
	// translate 標示過時，狀態與產出不動，其他步驟不動。
	after := r.stage(it, StageTranslate)
	if after.Status != store.StatusDone || after.ArtifactID != before.ArtifactID || after.Message != MsgSubtitlesUpdated ||
		len(after.Actions) != 1 || after.Actions[0] != ActionRetranslate || !after.FinishedAt.Equal(*before.FinishedAt) {
		t.Errorf("translate = %+v", after)
	}
	if c := r.stage(it, StageCorrect); c.Message != "" {
		t.Errorf("其他步驟不應被標示：%+v", c)
	}

	// 再匯入一次：新版本，舊版被取代。
	if _, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(importedSRT), Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "edited", "en"); n != 2 {
		t.Errorf("edited 版本數 = %d，預期 2", n)
	}
}

func TestImportSubtitleDoesNotFlagWithoutForeignTranslation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We use Kafka.")
	// 沒有譯文：translate 維持 pending，沒有訊息。
	res, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(importedSRT)}) // lang 沿用原文語言 en
	if err != nil || res.Flagged || res.Artifact.Lang != "en" {
		t.Fatalf("%+v，%v", res, err)
	}
	if s := r.stage(it, StageTranslate); s.Status != store.StatusPending || s.Message != "" || len(s.Actions) != 0 {
		t.Errorf("translate = %+v", s)
	}
	// 匯入的語言與譯文同一種語言（修正過的譯文）：譯文不算過時。
	seedVariant(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", "我們用 Kafka。")
	res, err = r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(importedSRT), Lang: "zh-Hans"})
	if err != nil || res.Flagged {
		t.Errorf("同語言：%+v，%v", res, err)
	}
	if s := r.stage(it, StageTranslate); s.Message != "" {
		t.Errorf("translate = %+v", s)
	}
}

// 無法解析時不寫入任何東西（OUT-R15）。
func TestImportSubtitleRejectsBrokenFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantTranslated, "ja", "翻訳")
	artifacts := func() int {
		entries, _ := os.ReadDir(filepath.Join(r.dir, "artifacts", it.ID))
		return len(entries)
	}
	n0 := artifacts()
	broken := "1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:01,000\nsecond\n"
	_, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(broken), Lang: "en"})
	var pe *subtitle.ParseError
	if !errors.As(err, &pe) || pe.Cue != 2 {
		t.Fatalf("err = %v，預期第 2 句的 ParseError", err)
	}
	if artifacts() != n0 {
		t.Error("失敗的匯入不應留下產出檔")
	}
	if arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript"); len(arts) != 1 {
		t.Errorf("失敗的匯入不應新增產出：%+v", arts)
	}
	if s := r.stage(it, StageTranslate); s.Message != "" {
		t.Errorf("失敗的匯入不應標示過時：%+v", s)
	}

	var inv *InvalidError
	for name, req := range map[string]ImportRequest{
		"語言不正確": {Data: []byte(importedSRT), Lang: "not a lang"},
		"格式不認得": {Data: []byte(importedSRT), Lang: "en", Format: "docx"},
		"檔案太大":  {Data: bytes.Repeat([]byte("x"), MaxImportBytes+1), Lang: "en"},
	} {
		req.ItemID = it.ID
		if _, err := r.p.ImportSubtitle(ctx, req); !errors.As(err, &inv) {
			t.Errorf("%s：err = %v，預期 InvalidError", name, err)
		}
	}
	if _, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: "nope", Data: []byte(importedSRT), Lang: "en"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var tr *InTrashError
	if _, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Data: []byte(importedSRT), Lang: "en"}); !errors.As(err, &tr) {
		t.Errorf("垃圾桶中的影音：%v", err)
	}
}

// previewRunner 是 ffmpeg 與 ffprobe 的替身：ffmpeg 的預覽呼叫在工作目錄寫出 out.png（內容可設定）。
type previewRunner struct {
	calls   []toolexec.Cmd
	png     []byte
	probe   string
	ass     string // 預覽呼叫時 s.ass 的內容
	exit    int
	outFile string
}

func (p *previewRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	p.calls = append(p.calls, c)
	if strings.HasSuffix(c.Path, "ffprobe") {
		return toolexec.Result{Stdout: []byte(p.probe)}, nil
	}
	if p.exit != 0 {
		return toolexec.Result{ExitCode: p.exit, Stderr: []byte("No such filter: 'ass'\n")}, nil
	}
	b, _ := os.ReadFile(filepath.Join(c.Dir, "s.ass"))
	p.ass = string(b)
	out := p.outFile
	if out == "" {
		out = "out.png"
	}
	return toolexec.Result{}, os.WriteFile(filepath.Join(c.Dir, out), p.png, 0o600)
}

func newPreviewRig(t *testing.T, pr *previewRunner) *rig {
	t.Helper()
	dir := t.TempDir()
	cfg := map[string]string{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg[name] = p
	}
	tool := media.New(pr, toolexec.Resolver{Config: cfg, Vars: map[string]string{}})
	return newRig(t, func(d *Deps) { d.Media = tool; d.Files = pathguard.Unrestricted() })
}

var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)

func TestPreviewFrame(t *testing.T) {
	ctx := context.Background()
	pr := &previewRunner{png: testPNG}
	r := newPreviewRig(t, pr)
	it := newRenderItem(t, r)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We use Kafka.", "It is a log.")
	seedVariant(t, r, it, subtitle.VariantTranslated, "zh-Hant-TW", "我們用 Kafka。", "它是日誌。")
	style := subtitle.ASSStyle{
		Primary:     subtitle.ASSLine{Size: 36, Color: "#FFFFFF", OutlineColor: "#000000", BackColor: "#00000080"},
		Translation: subtitle.ASSLine{Size: 52, Color: "#FFFFFF", OutlineColor: "#000000", BackColor: "#00000080"},
		Layout:      subtitle.ASSLayout{Show: subtitle.ShowBoth, Order: subtitle.OrderTranslationFirst, TranslationAt: subtitle.AtStacked, Alignment: 2, MarginV: 48, BorderStyle: 1},
	}

	// at = 2.5 秒：第二句（2～4 秒）；用短代號指定影音；字型依語言補上。
	png, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: "yt:abc123xxxxx", Style: style, AtMS: 2500})
	if err != nil || !bytes.Equal(png, testPNG) {
		t.Fatalf("PreviewFrame = %d bytes，%v", len(png), err)
	}
	for _, want := range []string{"Style: Translation,Noto Sans CJK TC,52", "Style: Primary,Noto Sans,36", "它是日誌。", "It is a log."} {
		if !strings.Contains(pr.ass, want) {
			t.Errorf("s.ass 缺少 %q\n%s", want, pr.ass)
		}
	}
	// 線上影音沒有媒體：用合成畫布，工作目錄是 <data>/tmp 底下專用的目錄，用完就刪掉。
	call := pr.calls[len(pr.calls)-1]
	if !strings.Contains(strings.Join(call.Args, " "), "-f lavfi") || !strings.HasPrefix(call.Dir, filepath.Join(r.dir, "tmp", "preview-")) {
		t.Errorf("呼叫 = %+v", call)
	}
	if _, err := os.Stat(call.Dir); !os.IsNotExist(err) {
		t.Errorf("預覽的暫存目錄應已刪除：%v", err)
	}
	// 在所有字幕之後：取最近的一句（最後一句）。
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: it.ID, Style: style, AtMS: 3_000_000}); err != nil || !strings.Contains(pr.ass, "它是日誌。") {
		t.Errorf("最近的一句：%v\n%s", err, pr.ass)
	}

	// 影音沒有任何字幕：範例文字。
	fresh := r.ingest("https://www.youtube.com/watch?v=zzzzzzzzzzz", nil, StageMetadata)
	r.run()
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: fresh.ID, Style: style}); err != nil || !strings.Contains(pr.ass, previewSampleMain) {
		t.Errorf("範例文字：%v\n%s", err, pr.ass)
	}
}

func TestPreviewFrameFailures(t *testing.T) {
	ctx := context.Background()
	style := subtitle.ASSStyle{Layout: subtitle.ASSLayout{Alignment: 2}}

	pr := &previewRunner{png: []byte("not a png at all")}
	r := newPreviewRig(t, pr)
	it := newRenderItem(t, r)
	// 外部工具的失敗都是 *UpstreamError（502）；訊息不含工具的輸出，原始錯誤留在 Err。
	var up *UpstreamError
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: it.ID, Style: style}); !errors.As(err, &up) || !strings.Contains(up.Err.Error(), "PNG") {
		t.Errorf("輸出不是 PNG：%v", err)
	}
	pr.outFile = "wrong.png"
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: it.ID, Style: style}); !errors.As(err, &up) {
		t.Errorf("ffmpeg 沒有產生 out.png：%v", err)
	}
	pr.exit = 1
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: it.ID, Style: style}); !errors.As(err, &up) || !strings.Contains(up.Msg, "libass") || strings.Contains(up.Msg, "No such filter") {
		t.Errorf("ffmpeg 失敗：%v", err)
	}
	if _, err := r.p.PreviewFrame(ctx, PreviewRequest{Item: "yt:zzzzzzzzzzz", Style: style}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
	// 沒有 ffmpeg：501。
	bare := newRig(t)
	var ni *NotImplementedError
	if _, err := bare.p.PreviewFrame(ctx, PreviewRequest{Item: "x", Style: style}); !errors.As(err, &ni) {
		t.Errorf("沒有 media 工具：%v", err)
	}
}

// 本機影片有影像串流時抽真正的畫面；只有音訊、檔案不存在時改用合成畫布。
func TestPreviewVideoSelection(t *testing.T) {
	ctx := context.Background()
	pr := &previewRunner{}
	r := newPreviewRig(t, pr)
	file := filepath.Join(t.TempDir(), "lecture.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	it := store.Item{Platform: "local", ExternalID: "lecture.mp4", SourcePath: file}

	pr.probe = `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264"},{"index":1,"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"10","format_name":"mov,mp4"}}`
	if got := r.p.previewVideo(ctx, it); got == "" || filepath.Base(got) != "lecture.mp4" {
		t.Errorf("有影像串流：%q", got)
	}
	pr.probe = `{"streams":[{"index":0,"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"10","format_name":"mov,mp4"}}`
	if got := r.p.previewVideo(ctx, it); got != "" {
		t.Errorf("只有音訊：%q", got)
	}
	it.SourcePath = filepath.Join(t.TempDir(), "missing.mp4")
	if got := r.p.previewVideo(ctx, it); got != "" {
		t.Errorf("檔案不存在：%q", got)
	}
	if got := r.p.previewVideo(ctx, store.Item{Platform: "youtube", ExternalID: "abc123xxxxx"}); got != "" {
		t.Errorf("線上影音：%q", got)
	}
}
