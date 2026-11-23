package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// userMessage 是錯誤給使用者看的說明（errclass.Error 的 Message）。
func userMessage(err error) string {
	var ce *errclass.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return ""
}

// frames 任務的 payload 不可信：壞掉的 JSON、不合法的版本、找不到或壞掉的候選中間檔都是 permanent，不會碰到版本目錄以外的路徑。
func TestRunFramesRejectsBadPayloads(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	eff := mustEffective(t, stagesOver(StageFrames, map[string]any{"executor": "rules"}))
	it := store.Item{ID: "item-1"}
	run := func(kind, payload string) error {
		_, err := r.p.runFrames(ctx, store.Task{Kind: kind, Payload: json.RawMessage(payload)}, store.StageRun{}, it, eff)
		return err
	}

	if err := run(TaskFramesCandidates, `{`); !isPermanent(err) {
		t.Errorf("壞掉的 JSON：%v", err)
	}
	for _, version := range []string{"", "../../etc", "a/b", "short", strings.Repeat("a", 65)} {
		payload, _ := json.Marshal(framesPayload{ItemID: it.ID, Version: version})
		if err := run(TaskFramesSelect, string(payload)); !isPermanent(err) || !strings.Contains(err.Error(), "版本不正確") {
			t.Errorf("版本 %q：%v", version, err)
		}
	}

	version := "ver-0000001"
	payload, _ := json.Marshal(framesPayload{ItemID: it.ID, Version: version})
	if err := run(TaskFramesSelect, string(payload)); !isPermanent(err) || !strings.Contains(err.Error(), "找不到候選畫面") {
		t.Errorf("沒有候選檔：%v", err)
	}
	dir := framesDir(r.dir, it.ID, version)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidatesFile), []byte(`{"candidates":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(TaskFramesSelect, string(payload)); !isPermanent(err) || !strings.Contains(err.Error(), "候選畫面的記錄不正確") {
		t.Errorf("壞掉的候選檔：%v", err)
	}
	// 候選檔不是檔案（讀取失敗不是「不存在」）：原樣回傳，不當成 permanent。
	if err := os.Remove(filepath.Join(dir, candidatesFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, candidatesFile), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := run(TaskFramesSelect, string(payload)); err == nil || isPermanent(err) {
		t.Errorf("讀取失敗應原樣回傳：%v", err)
	}
}

// 沒有任何場景也沒有密度下限補上的候選：步驟仍 done，manifest 的候選是空陣列（不是 null），顯示只找到 0 張。
func TestFramesWithoutCandidates(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{})
	r.rerunFrames(nil, false)
	v := framesView(t, r)
	if v.CandidateCount != 0 || v.SelectedCount != 0 || !v.Shortfall || v.Frames != nil {
		t.Errorf("view = %+v", v)
	}
	art, _ := r.currentFramesArtifact()
	b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(art.Path)))
	if err != nil || !strings.Contains(string(b), `"candidates":[]`) {
		t.Errorf("manifest = %s %v", b, err)
	}
	if st := latestStage(t, r.rig, r.item.ID, StageFrames); st.Status != store.StatusDone || st.Message != "只找到 0 張有意義的畫面，少於建議的 5 張。" {
		t.Errorf("frames = %+v", st)
	}
}

// F8：frames 只支援 YouTube（需要能下載影片的 client）與本機影片；其他平台與缺少檔案都是 permanent。
func TestFramesVideoSources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	r := newRig(t, func(d *Deps) { d.Files = pathguard.New([]string{root}) })
	video := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(video, []byte("V"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := r.p.framesVideo(ctx, store.Item{Platform: source.PlatformBilibili})
	if msg := userMessage(err); !isPermanent(err) || msg != "目前只有 YouTube 與本機影片可以擷取關鍵畫面。" {
		t.Errorf("bilibili：%v（%q）", err, msg)
	}
	// 一般的 fakeYT 沒有實作 VideoDownloader。
	if _, _, _, err := r.p.framesVideo(ctx, store.Item{Platform: source.PlatformYouTube, ExternalID: "abc123xxxxx"}); !isPermanent(err) {
		t.Errorf("沒有下載影片的 client：%v", err)
	}

	path, w, h, err := r.p.framesVideo(ctx, store.Item{Platform: source.PlatformLocal, SourcePath: video})
	if err != nil || path != video || w != 0 || h != 0 {
		t.Errorf("本機影片 = %q %dx%d %v", path, w, h, err)
	}
	if _, _, _, err := r.p.framesVideo(ctx, store.Item{Platform: source.PlatformLocal}); !isPermanent(err) {
		t.Errorf("沒有路徑：%v", err)
	}
	_, _, _, err = r.p.framesVideo(ctx, store.Item{Platform: source.PlatformLocal, SourcePath: filepath.Join(root, "gone.mp4")})
	if msg := userMessage(err); !isPermanent(err) || msg != ErrMediaMissing.Error() {
		t.Errorf("檔案不見：%v（%q）", err, msg)
	}
	// 不在守衛允許的範圍：同樣 permanent，不會被讀取。
	if _, _, _, err := r.p.framesVideo(ctx, store.Item{Platform: source.PlatformLocal, SourcePath: "/etc/hostname"}); !isPermanent(err) {
		t.Errorf("範圍外的路徑：%v", err)
	}

	// 相對路徑接在資料目錄之後（上傳的檔案，F8）；沒有守衛時不檢查檔案是否存在，交給後面的 ffprobe。
	bare := newRig(t)
	path, _, _, err = bare.p.framesVideo(ctx, store.Item{Platform: source.PlatformLocal, SourcePath: "uploads/a/clip.mp4"})
	if want := filepath.Join(bare.dir, "uploads", "a", "clip.mp4"); err != nil || path != want {
		t.Errorf("相對路徑 = %q %v，預期 %q", path, err, want)
	}
}

// FRM-R8：來源超過 1920×1080 時擷取縮到 1920×1080（不放大）；沒有影像串流時回傳 nil；沒有 ffmpeg 時 permanent。
func TestExtractCandidates(t *testing.T) {
	ctx := context.Background()
	params := frames.Defaults()
	params.MinIntervalS = 3600
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(video, []byte("V"), 0o600); err != nil {
		t.Fatal(err)
	}
	newPipeline := func(setup func(*frameMedia)) (*Pipeline, *frameMedia) {
		fm, tool := newFrameMedia(t)
		setup(fm)
		return newRig(t, func(d *Deps) { d.Media = tool }).p, fm
	}
	item := func(dur *int) store.Item {
		return store.Item{ID: "item-1", Platform: source.PlatformLocal, SourcePath: video, DurationS: dur}
	}

	t.Run("4K 的來源縮到 1920x1080", func(t *testing.T) {
		p, fm := newPipeline(func(fm *frameMedia) {
			fm.width, fm.height = 3840, 2160
			fm.scenes = []fakeScene{{At: 30, Score: 0.9, Color: gridColor(1)}}
		})
		dir := t.TempDir()
		c, err := p.extractCandidates(ctx, item(nil), dir, params)
		if err != nil || c == nil {
			t.Fatal(c, err)
		}
		if c.SourceResolution != "3840x2160" || c.ExtractResolution != "1920x1080" || c.Width != 1920 || c.Height != 1080 || c.DurationMS != 300_000 {
			t.Errorf("candidates = %+v", c)
		}
		if got := fm.extracted(); len(got) != 1 || got[0].W != 1920 || got[0].H != 1080 || got[0].In != video {
			t.Errorf("擷取 = %+v", got)
		}
		if len(c.Candidates) != 1 || c.Candidates[0] != (frameCandidate{N: 1, AtMS: 30_000, Score: 0.9, File: "001.jpg", Thumb: "001.thumb.jpg"}) {
			t.Errorf("候選 = %+v", c.Candidates)
		}
		if _, err := os.Stat(filepath.Join(dir, "001.thumb.jpg")); err != nil || !strings.Contains(c.FFmpeg, "6.1.1") {
			t.Errorf("縮圖 %v，ffmpeg %q", err, c.FFmpeg)
		}
	})

	t.Run("項目的片長較短時以它為準", func(t *testing.T) {
		p, fm := newPipeline(func(fm *frameMedia) {
			fm.scenes = []fakeScene{{At: 60, Score: 0.9, Color: gridColor(1)}, {At: 200, Score: 0.9, Color: gridColor(2)}}
		})
		dur := 120
		c, err := p.extractCandidates(ctx, item(&dur), t.TempDir(), params)
		if err != nil || c.DurationMS != 120_000 || !slices.Equal(fm.extractedTimes(), []float64{60}) {
			t.Errorf("candidates = %+v %v，擷取 %v：片長之後的場景不使用", c, err, fm.extractedTimes())
		}
	})

	t.Run("ffprobe 沒有片長時用項目的片長", func(t *testing.T) {
		p, _ := newPipeline(func(fm *frameMedia) { fm.durationS = 0 })
		dur := 200
		c, err := p.extractCandidates(ctx, item(&dur), t.TempDir(), params)
		if err != nil || c.DurationMS != 200_000 {
			t.Errorf("candidates = %+v %v", c, err)
		}
	})

	t.Run("沒有影像串流", func(t *testing.T) {
		p, fm := newPipeline(func(fm *frameMedia) { fm.width, fm.height = 0, 0 })
		if c, err := p.extractCandidates(ctx, item(nil), t.TempDir(), params); c != nil || err != nil || fm.sceneCount() != 0 {
			t.Errorf("c = %+v err = %v，場景偵測 %d 次", c, err, fm.sceneCount())
		}
	})

	t.Run("沒有 ffmpeg", func(t *testing.T) {
		bare := newRig(t)
		if _, err := bare.p.extractCandidates(ctx, item(nil), t.TempDir(), params); !isPermanent(err) {
			t.Errorf("err = %v", err)
		}
	})
}

// FRM-R5、R9：提示詞可以停用（空陣列）；非字串的項目略過；數字參數接受整數與 json.Number。
func TestFramesParamsVariants(t *testing.T) {
	eff := mustEffective(t, stagesOver(StageFrames, map[string]any{"executor": "rules", "hint_words": []any{}, "dedupe_window": 3, "dedupe_threshold": 0.1, "per_minute": 2}))
	p, err := framesParams(eff)
	if err != nil || len(p.HintWords) != 0 || p.DedupeWindow != 3 || p.DedupeThreshold != 0.1 || p.PerMinute != 2 {
		t.Errorf("params = %+v %v", p, err)
	}
	if d := frames.Defaults(); d.MinIntervalS != 60 || d.SceneThreshold != 0.3 || d.MaxFrames != 20 {
		t.Fatalf("預設值不是規格的值：%+v", d)
	}
	// 沒有設定的欄位用預設值。
	p, err = framesParams(mustEffective(t, stagesOver(StageFrames, map[string]any{"executor": "rules"})))
	if err != nil || p.SceneThreshold != 0.3 || p.MinIntervalS != 60 || p.MaxFrames != 20 || len(p.HintWords) != len(frames.DefaultHintWords) {
		t.Errorf("預設 params = %+v %v", p, err)
	}
	// 修改回傳的預設提示詞不能影響之後的呼叫。
	p.HintWords[0] = "changed"
	if p2, _ := framesParams(mustEffective(t, nil)); p2.HintWords[0] != frames.DefaultHintWords[0] {
		t.Error("預設提示詞被共用的 slice 改動")
	}
}

// FRM-R5：候選前後 10 秒內的字幕以空白串接，最多 300 字（以字元計，不切壞多位元組字元）。
func TestNearbyText(t *testing.T) {
	cues := []frames.Cue{
		{StartMS: 0, EndMS: 2_000, Text: "太早"},
		{StartMS: 45_000, EndMS: 47_000, Text: "前一句"},
		{StartMS: 50_000, EndMS: 52_000, Text: "這張圖"},
		{StartMS: 60_000, EndMS: 61_000, Text: "後一句"},
		{StartMS: 60_001, EndMS: 61_000, Text: "太晚"},
	}
	if got := nearbyText(50_000, cues); got != "前一句 這張圖 後一句" {
		t.Errorf("nearbyText = %q", got)
	}
	if got := nearbyText(500_000, cues); got != "" {
		t.Errorf("沒有字幕：%q", got)
	}
	long := []frames.Cue{{StartMS: 0, EndMS: 1_000, Text: strings.Repeat("字", 299)}, {StartMS: 1_000, EndMS: 2_000, Text: strings.Repeat("圖", 50)}}
	got := []rune(nearbyText(1_000, long))
	if len(got) != 300 || string(got[299]) != " " {
		t.Errorf("長度 = %d，結尾 %q", len(got), string(got[len(got)-1]))
	}
	if got := []rune(nearbyText(0, []frames.Cue{{EndMS: 1, Text: strings.Repeat("字", 500)}})); len(got) != 300 {
		t.Errorf("單句超過上限：%d", len(got))
	}
}

// FRM-R9：沒有 chapters 產出時用平台章節（metadata 的 start 是秒）；metadata 壞掉或沒有章節時為空。
func TestFramesChaptersFromMetadata(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	chapterOf := func(md string) []frames.Chapter {
		return r.p.framesChapters(ctx, r.st.Read(), store.Item{ID: "item-1", Metadata: json.RawMessage(md)})
	}
	got := chapterOf(`{"chapters":[{"title":"Intro","start":0,"end":10},{"title":"Main","start":12.5,"end":20}]}`)
	if want := []frames.Chapter{{StartMS: 0, Title: "Intro"}, {StartMS: 12_500, Title: "Main"}}; !slices.Equal(got, want) {
		t.Errorf("章節 = %+v", got)
	}
	if got := chapterOf(`not json`); got != nil {
		t.Errorf("壞掉的 metadata：%+v", got)
	}
	if got := chapterOf(`{}`); len(got) != 0 {
		t.Errorf("沒有章節：%+v", got)
	}
	if got := r.p.framesChapters(ctx, r.st.Read(), store.Item{ID: "item-1"}); got != nil {
		t.Errorf("沒有 metadata：%+v", got)
	}
	// 沒有字幕時 cues 為 nil，沒有輸入 ID（不會成為 parents）。
	if cues, id := r.p.framesCues(ctx, r.st.Read(), "item-1"); cues != nil || id != "" {
		t.Errorf("cues = %+v %q", cues, id)
	}
}

// 沿用候選時的連結：優先 hard link；不能 link 時複製；不覆蓋既有的檔案。
func TestLinkOrCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(src, []byte("IMG"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.jpg")
	if err := linkOrCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if !os.SameFile(a, b) {
		t.Error("同一個檔案系統應該用 hard link")
	}
	// 目的地已存在：失敗且不改動內容。
	if err := linkOrCopy(src, dst); err == nil {
		t.Error("目的地已存在應該失敗")
	}
	other := filepath.Join(dir, "c.jpg")
	if err := os.WriteFile(other, []byte("OTHER"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linkOrCopy(src, other); err == nil {
		t.Error("不能覆蓋既有的檔案")
	}
	if got, _ := os.ReadFile(other); string(got) != "OTHER" {
		t.Errorf("既有的檔案被改動：%q", got)
	}
	if err := linkOrCopy(filepath.Join(dir, "missing.jpg"), filepath.Join(dir, "d.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("來源不見：%v", err)
	}

	// 跨檔案系統（EXDEV）不能 link：複製內容，檔案權限只有擁有者。
	shm, err := os.MkdirTemp("/dev/shm", "notesteme-link-")
	if err != nil {
		t.Skip("沒有 /dev/shm，無法驗證跨檔案系統的複製")
	}
	defer os.RemoveAll(shm)
	far := filepath.Join(shm, "far.jpg")
	if err := os.WriteFile(far, []byte("FAR"), 0o600); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dir, "copied.jpg")
	if err := os.Link(far, copied); err == nil {
		_ = os.Remove(copied)
		t.Skip("同一個檔案系統，無法驗證跨檔案系統的複製")
	}
	if err := linkOrCopy(far, copied); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(copied); string(got) != "FAR" {
		t.Errorf("複製的內容 = %q", got)
	}
	if fi, _ := os.Stat(copied); fi.Mode().Perm() != filePerm {
		t.Errorf("權限 = %v", fi.Mode().Perm())
	}
}

func TestStageExecutorOf(t *testing.T) {
	if got := stageExecutorOf(store.StageRun{}); got != ExecutorRules {
		t.Errorf("沒有記錄時是 rules：%q", got)
	}
	if got := stageExecutorOf(store.StageRun{Executor: ExecutorAPI}); got != ExecutorAPI {
		t.Errorf("executor = %q", got)
	}
}
