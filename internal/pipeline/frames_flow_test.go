package pipeline

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// mainScenes 是 300 秒影片的 9 個場景：第 9 個與第 8 個同色（去重）。
// 規則分數：第 2 個只有 0.35，但前後有提示詞「as you can see」（+0.5 = 0.85）。
func mainScenes() []fakeScene {
	spec := []struct{ at, score float64 }{
		{20, 0.9}, {50, 0.35}, {80, 0.8}, {110, 0.4}, {140, 0.7}, {170, 0.45}, {200, 0.6}, {230, 0.5}, {260, 0.55},
	}
	var out []fakeScene
	for i, s := range spec {
		out = append(out, fakeScene{At: s.at, Score: s.score, Color: gridColor(i + 1)})
	}
	out[8].Color = out[7].Color
	return out
}

// newRulesRig 建立 mainScenes 的 rig 並以 rules 選圖完成：第 4 個候選（110 秒）在 SponsorBlock 片段內。
func newRulesRig(t *testing.T) *framesRig {
	t.Helper()
	r := newFramesRig(t, framesRigOpts{
		scenes: mainScenes(), sponsor: sponsorAt(100_000, 120_000), maxW: 3840, maxH: 2160,
		chapters: []youtube.Chapter{{Title: "Intro", Start: 0, End: 100}, {Title: "Main", Start: 100, End: 300}},
	})
	r.rerunFrames(nil, false)
	return r
}

func framesView(t *testing.T, r *framesRig) FramesView {
	t.Helper()
	v, err := r.p.Frames(context.Background(), r.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func selectedNs(v FramesView) []int {
	var out []int
	for _, f := range v.Frames {
		if f.Selected {
			out = append(out, f.N)
		}
	}
	return out
}

// FRM-R2～R6、R8、R9、R11：場景偵測 → 擷取 → 去重 → 排除 SponsorBlock 片段 → 規則選圖 → manifest 產出。
func TestFramesRulesFlow(t *testing.T) {
	r := newRulesRig(t)
	ctx := context.Background()
	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusDone || st.Executor != ExecutorRules || st.Message != "" || st.ArtifactID == "" {
		t.Fatalf("frames = %+v", st)
	}

	// 步驟使用 frames、local 兩種資源類別的任務；ffmpeg 只呼叫 1 次場景偵測，所有場景都擷取（去重在擷取之後）。
	if r.fm.sceneCount() != 1 || r.vyt.downloads() != 1 {
		t.Errorf("場景偵測 %d 次、下載 %d 次", r.fm.sceneCount(), r.vyt.downloads())
	}
	if got, want := r.fm.extractedTimes(), []float64{20, 50, 80, 110, 140, 170, 200, 230, 260}; !slices.Equal(got, want) {
		t.Errorf("擷取的時間 = %v，預期 %v", got, want)
	}
	if got := r.fm.thresholds; len(got) != 1 || got[0] != "0.3000" {
		t.Errorf("場景門檻 = %v，預設是 0.3", got)
	}
	wantIn := filepath.Join(r.dir, "media", r.item.ID, "video.mp4")
	for _, c := range r.fm.extracted() {
		if c.W != 640 || c.H != 360 || c.In != wantIn {
			t.Fatalf("擷取 = %+v，預期 640x360 的 %s", c, wantIn)
		}
	}

	// FRM-R3：第 9 個與第 8 個同色，去重後剩 8 張；FRM-R4：5 分鐘的範圍是 5–8，N = ceil(1 × 5) = 5。
	v := framesView(t, r)
	if v.Target != 5 || v.RangeMin != 5 || v.RangeMax != 8 || v.CandidateCount != 8 || v.SelectedCount != 5 || v.Shortfall {
		t.Fatalf("view = %+v", v)
	}
	// FRM-R5、R6：分數 0.9、0.85（提示詞）、0.8、0.7、0.6 → 1、2、3、5、7；第 4 張在片段內。
	if got, want := selectedNs(v), []int{1, 2, 3, 5, 7}; !slices.Equal(got, want) {
		t.Errorf("選中 = %v，預期 %v", got, want)
	}
	// FRM-R9：規則的圖說是涵蓋該時間的 cue（去掉換行）；10 秒內沒有 cue 時為空；章節是起點不晚於畫面時間的最後一個。
	want := map[int]struct {
		at                int64
		caption, chapter  string
		excluded, section string
	}{
		1: {20_000, "Opening slide.", "Intro", "", ""},
		2: {50_000, "As you can see, the cache has two layers.", "Intro", "", ""},
		3: {80_000, "Eviction policy.", "Intro", "", ""},
		5: {140_000, "", "Main", "", ""},
		7: {200_000, "", "Main", "", ""},
	}
	for _, f := range v.Frames {
		if f.Width != 640 || f.Height != 360 {
			t.Errorf("第 %d 張的尺寸 = %dx%d", f.N, f.Width, f.Height)
		}
		if w, ok := want[f.N]; ok {
			if f.AtMS != w.at || f.Caption != w.caption || f.Chapter != w.chapter || f.Excluded != "" {
				t.Errorf("第 %d 張 = %+v，預期 %+v", f.N, f, w)
			}
			continue
		}
		if f.Selected || f.Caption != "" || f.Chapter != "" {
			t.Errorf("第 %d 張沒有被選中，不該有圖說與章節：%+v", f.N, f)
		}
	}
	if f := v.Frames[3]; f.Excluded != "sponsor" || f.AtMS != 110_000 || f.Score != 0.4 {
		t.Errorf("第 4 張在 SponsorBlock 片段內，應排除：%+v", f)
	}
	if f := v.Frames[0]; f.Score != 0.9 {
		t.Errorf("場景分數 = %v", f.Score)
	}

	// FRM-R11：來源紀錄與 parents（輸入字幕、sponsor_segments）。
	art, prov := r.currentFramesArtifact()
	if art.ID != st.ArtifactID || art.Format != framesFmt {
		t.Errorf("artifact = %+v", art)
	}
	fr, _ := prov["frames"].(map[string]any)
	if fr["select"] != selectRules || fr["scene_threshold"] != 0.3 || fr["min_interval_s"] != float64(3600) ||
		fr["dedupe_threshold"] != 0.05 || fr["dedupe_window"] != float64(5) || fr["reused_candidates"] != false {
		t.Errorf("provenance.frames = %v", fr)
	}
	if prov["source_resolution"] != "3840x2160" || prov["extract_resolution"] != "640x360" || prov["duration_ms"] != float64(300_000) {
		t.Errorf("provenance = %v", prov)
	}
	if tv, _ := prov["tool_versions"].(map[string]any); !strings.Contains(tv["ffmpeg"].(string), "6.1.1") {
		t.Errorf("tool_versions = %v", prov["tool_versions"])
	}
	if _, ok := prov["usage"]; ok {
		t.Errorf("規則選圖沒有用量：%v", prov)
	}
	sb, ok, _ := r.p.currentSponsorArtifact(ctx, r.item.ID)
	if !ok || len(art.Parents) != 2 || art.Parents[1] != sb.ID {
		t.Fatalf("parents = %v，sponsor = %s", art.Parents, sb.ID)
	}
	if in, err := r.st.Read().Artifact(ctx, art.Parents[0]); err != nil || in.Variant != "corrected" {
		t.Errorf("parents[0] 應是校正後的字幕：%+v %v", in, err)
	}

	// 版本目錄只剩 8 組圖片與縮圖：候選中間檔、被去重的暫存圖都已清掉。
	ents, err := os.ReadDir(framesDir(r.dir, r.item.ID, art.ID))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 16 || slices.Contains(names, candidatesFile) || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "raw-") }) {
		t.Errorf("版本目錄 = %v", names)
	}
	if got := r.framesDirEntries(); len(got) != 1 || got[0] != art.ID {
		t.Errorf("frames 目錄 = %v", got)
	}
}

// FRM-R10：圖片與縮圖（寬 320）、ETag，以及各種找不到與不安全的情況。
func TestFrameImageAndErrors(t *testing.T) {
	r := newRulesRig(t)
	ctx := context.Background()
	art, _ := r.currentFramesArtifact()
	dir := framesDir(r.dir, r.item.ID, art.ID)

	read := func(n int, thumb bool) (w, h int, etag string) {
		t.Helper()
		f, fi, etag, err := r.p.FrameImage(ctx, r.item.ID, n, thumb)
		if err != nil {
			t.Fatalf("n=%d thumb=%v：%v", n, thumb, err)
		}
		defer f.Close()
		b := make([]byte, fi.Size())
		if _, err := f.Read(b); err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("n=%d 不是 JPEG：%v", n, err)
		}
		return cfg.Width, cfg.Height, etag
	}
	if w, h, etag := read(2, false); w != 640 || h != 360 || etag != `"`+art.SHA256[:16]+`-2-full"` {
		t.Errorf("完整圖 = %dx%d %s", w, h, etag)
	}
	if w, h, etag := read(2, true); w != frames.ThumbWidth || h != 180 || etag != `"`+art.SHA256[:16]+`-2-thumb"` {
		t.Errorf("縮圖 = %dx%d %s", w, h, etag)
	}

	for _, n := range []int{0, -1, 9, 1000} {
		if f, _, _, err := r.p.FrameImage(ctx, r.item.ID, n, false); !errors.Is(err, ErrFrameNotFound) || f != nil {
			t.Errorf("n=%d：err = %v", n, err)
		}
	}
	if _, err := r.p.Frames(ctx, "missing-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, "missing-item", 1, false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音（圖片）：%v", err)
	}

	// 圖片檔被換成指向版本目錄外的 symlink：不能讀出目錄外的檔案（路徑穿越）。
	secret := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(secret, solidJPEG(8, 8, gridColor(0)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, frameFile(3))); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, frameFile(3))); err != nil {
		t.Fatal(err)
	}
	if f, _, _, err := r.p.FrameImage(ctx, r.item.ID, 3, false); err == nil || f != nil {
		t.Errorf("symlink 到目錄外不該被開啟：f=%v err=%v", f, err)
	}
	// 檔案被換成目錄、或不見：都當作沒有這張畫面。
	if err := os.Remove(filepath.Join(dir, frameFile(4))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, frameFile(4)), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, r.item.ID, 4, false); !errors.Is(err, ErrFrameNotFound) {
		t.Errorf("目錄：%v", err)
	}
	if err := os.Remove(filepath.Join(dir, frameThumb(5))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, r.item.ID, 5, true); !errors.Is(err, ErrFrameNotFound) {
		t.Errorf("檔案不見：%v", err)
	}
	// 整個版本目錄不見。
	if err := os.Rename(dir, dir+".gone"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, r.item.ID, 1, false); !errors.Is(err, ErrFrameNotFound) {
		t.Errorf("版本目錄不見：%v", err)
	}
	if err := os.Rename(dir+".gone", dir); err != nil {
		t.Fatal(err)
	}

	// manifest 被改動：sha256 不符，兩個 API 都回報產出損毀。
	if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(art.Path)), []byte(`{"target":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Frames(ctx, r.item.ID); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("Frames：%v", err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, r.item.ID, 1, false); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("FrameImage：%v", err)
	}
	// manifest 不見：讀檔錯誤原樣回傳。
	if err := os.Remove(filepath.Join(r.dir, filepath.FromSlash(art.Path))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Frames(ctx, r.item.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("manifest 不見：%v", err)
	}

	// 影音在垃圾桶：兩個 API 都是 InTrashError。
	if _, err := r.p.Trash(ctx, r.item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var trash *InTrashError
	if _, err := r.p.Frames(ctx, r.item.ID); !errors.As(err, &trash) {
		t.Errorf("Frames：%v", err)
	}
	if _, _, _, err := r.p.FrameImage(ctx, r.item.ID, 1, false); !errors.As(err, &trash) {
		t.Errorf("FrameImage：%v", err)
	}
}

// 步驟沒有執行過：404 no_frames（FRM-R10）。
func TestFramesBeforeTheStepRuns(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{scenes: mainScenes()})
	ctx := context.Background()
	if _, err := r.p.Frames(ctx, r.item.ID); !errors.Is(err, ErrNoFrames) {
		t.Errorf("Frames：%v", err)
	}
	if f, _, _, err := r.p.FrameImage(ctx, r.item.ID, 1, false); !errors.Is(err, ErrNoFrames) || f != nil {
		t.Errorf("FrameImage：%v", err)
	}
}

// FRM-R2：沒有場景變化時，每隔 min_interval_s 補一張（1 個場景在 100 秒：補 60，再補 160、220、280）；補上的候選分數為 0。
func TestFramesDensityFloor(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{
		scenes: []fakeScene{{At: 100, Score: 0.9, Color: gridColor(1)}},
		frames: map[string]any{"min_interval_s": 60},
	})
	r.rerunFrames(nil, false)
	if got, want := r.fm.extractedTimes(), []float64{60, 100, 160, 220, 280}; !slices.Equal(got, want) {
		t.Fatalf("擷取的時間 = %v，預期 %v", got, want)
	}
	v := framesView(t, r)
	if v.CandidateCount != 5 || v.SelectedCount != 5 || v.Shortfall {
		t.Fatalf("view = %+v", v)
	}
	for _, f := range v.Frames {
		if wantScore := map[int64]float64{100_000: 0.9}[f.AtMS]; f.Score != wantScore {
			t.Errorf("%d 毫秒的分數 = %v，預期 %v", f.AtMS, f.Score, wantScore)
		}
	}
}

// FRM 介面的參數會傳到場景偵測與選圖：scene_threshold 0.6 只留下分數更高的場景（20、80、140 秒），
// per_minute 0.5 讓目標張數 = ceil(0.5 × 5) = 3。
func TestFramesParamsReachTheSteps(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{scenes: mainScenes(), frames: map[string]any{"scene_threshold": 0.6, "per_minute": 0.5}})
	r.rerunFrames(nil, false)
	if got := r.fm.thresholds; len(got) != 1 || got[0] != "0.6000" {
		t.Errorf("場景門檻 = %v", got)
	}
	if got, want := r.fm.extractedTimes(), []float64{20, 80, 140}; !slices.Equal(got, want) {
		t.Errorf("擷取的時間 = %v，預期 %v", got, want)
	}
	// 只有 3 張可選（少於範圍下限 5）：全部選用，仍是 done，提醒中的 N 是選中的張數、M 是範圍下限（FRM-R4）。
	v := framesView(t, r)
	if v.Target != 3 || !v.Shortfall || v.SelectedCount != 3 || v.CandidateCount != 3 {
		t.Errorf("view = %+v", v)
	}
	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusDone || st.Message != "只找到 3 張有意義的畫面，少於建議的 5 張。" {
		t.Errorf("frames = %+v", st)
	}
}

// FRM-R1：下載後發現沒有影像串流（YouTube 只有音訊）時步驟 skipped（audio_only），不留下產出與目錄。
func TestFramesAudioOnlyVideoIsSkipped(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{audioOnly: true, scenes: mainScenes()})
	r.rerunFrames(nil, false)
	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusSkipped || st.BlockReason != SkipAudioOnly || st.ArtifactID != "" || st.Executor != ExecutorRules {
		t.Fatalf("frames = %+v", st)
	}
	if r.vyt.downloads() != 1 || r.fm.sceneCount() != 0 {
		t.Errorf("下載 %d 次、場景偵測 %d 次：沒有影像時不該偵測場景", r.vyt.downloads(), r.fm.sceneCount())
	}
	if got := r.framesDirEntries(); len(got) != 0 {
		t.Errorf("不該留下版本目錄：%v", got)
	}
	if _, err := r.p.Frames(context.Background(), r.item.ID); !errors.Is(err, ErrNoFrames) {
		t.Errorf("Frames：%v", err)
	}
}

// 候選任務失敗（下載失敗、場景偵測失敗、擷取的不是圖片）：步驟 failed（permanent），版本目錄清掉，不留下產出。
func TestFramesCandidateFailuresCleanUp(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*framesRig)
		// reached 回報失敗發生的位置：場景偵測次數、擷取次數。
		scenes, extracted int
	}{
		{"下載失敗", func(r *framesRig) { r.vyt.downloadErr = errPermanent("yt-dlp 找不到影片") }, 0, 0},
		{"場景偵測失敗", func(r *framesRig) { r.fm.sceneErr = "Invalid data found" }, 1, 0},
		{"擷取的不是圖片", func(r *framesRig) { r.fm.badFrame = true }, 1, 1},
	}
	for _, c := range cases {
		r := newFramesRig(t, framesRigOpts{scenes: mainScenes()})
		c.setup(r)
		r.rerunFrames(nil, false)
		st := latestStage(t, r.rig, r.item.ID, StageFrames)
		if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.ArtifactID != "" {
			t.Errorf("%s：frames = %+v", c.name, st)
		}
		if r.fm.sceneCount() != c.scenes || len(r.fm.extracted()) != c.extracted {
			t.Errorf("%s：場景偵測 %d 次、擷取 %d 次，預期 %d、%d", c.name, r.fm.sceneCount(), len(r.fm.extracted()), c.scenes, c.extracted)
		}
		if got := r.framesDirEntries(); len(got) != 0 {
			t.Errorf("%s：不該留下版本目錄：%v", c.name, got)
		}
		if _, err := r.p.Frames(context.Background(), r.item.ID); !errors.Is(err, ErrNoFrames) {
			t.Errorf("%s：Frames = %v", c.name, err)
		}
	}
}
