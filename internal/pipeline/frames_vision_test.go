package pipeline

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/store"
)

// visionScenes 是 600 秒影片的 11 個場景（可選 11 張，範圍 8–12，N = 10；拼圖 9 + 2 格）。
// 第 3 個的分數最低（0.4），其餘都是 0.6，所以規則選圖會捨棄它。
func visionScenes() []fakeScene {
	var out []fakeScene
	for i := 0; i < 11; i++ {
		s := fakeScene{At: float64(30 * (i + 1)), Score: 0.6, Color: gridColor(i)}
		if i == 2 {
			s.Score = 0.4
		}
		out = append(out, s)
	}
	return out
}

func newVisionRig(t *testing.T, fake *framesLLM) *framesRig {
	t.Helper()
	return newFramesRig(t, framesRigOpts{scenes: visionScenes(), durationS: 600, llm: fake})
}

// FRM-R7、F4、F5：可選的候選每 9 張一張拼圖（9 + 2）一次送出，資料區塊是不可信的字幕；模型回傳的編號與圖說寫入 manifest。
func TestFramesVisionSelection(t *testing.T) {
	fake := &framesLLM{outs: []string{`{"picks":[{"sheet":1,"n":2,"caption":"圖一\n含換行"},{"sheet":2,"n":2,"caption":"第二張"}]}`}}
	r := newVisionRig(t, fake)
	r.rerunFrames(apiFrames(nil), false)

	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusDone || st.Executor != ExecutorAPI || st.Message != "" {
		t.Fatalf("frames = %+v", st)
	}
	reqs := fake.requests()
	if len(reqs) != 1 || len(reqs[0].Images) != 2 || reqs[0].Model != "m" {
		t.Fatalf("請求 = %d 個", len(reqs))
	}
	for i, img := range reqs[0].Images {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil || img.MediaType != "image/jpeg" {
			t.Fatalf("拼圖 %d：%s %v", i+1, img.MediaType, err)
		}
		// 3×3 的拼圖，每格 320×180；第 2 張只有 2 格，只有 1 列。
		wantH := map[int]int{0: 3 * frames.SheetCellH, 1: frames.SheetCellH}[i]
		if cfg.Width != 3*frames.SheetCellW || cfg.Height != wantH {
			t.Errorf("拼圖 %d = %dx%d", i+1, cfg.Width, cfg.Height)
		}
	}
	// 每張候選一項；文字以「[拼圖 s 編號 n]」開頭，後面是前後 10 秒內的字幕（第 1 個候選 30 秒：前一句 18～22 秒）。
	for _, want := range []string{"[拼圖 1 編號 1] Opening slide.", "[拼圖 1 編號 9]", "[拼圖 2 編號 1]", "[拼圖 2 編號 2]"} {
		if !strings.Contains(reqs[0].Data, want) {
			t.Errorf("資料區塊沒有 %q：%s", want, reqs[0].Data)
		}
	}
	if strings.Contains(reqs[0].Data, "[拼圖 2 編號 3]") {
		t.Error("第 2 張拼圖只有 2 格")
	}

	// sheet 1 的 2 號是第 2 個候選，sheet 2 的 2 號是第 11 個；圖說是模型的（去掉換行），不用字幕。
	v := framesView(t, r)
	if got, want := selectedNs(v), []int{2, 11}; !slices.Equal(got, want) || v.Target != 10 || v.CandidateCount != 11 || v.Shortfall {
		t.Fatalf("選中 = %v，view = %+v", got, v)
	}
	if v.Frames[1].Caption != "圖一 含換行" || v.Frames[10].Caption != "第二張" {
		t.Errorf("圖說 = %q、%q", v.Frames[1].Caption, v.Frames[10].Caption)
	}
	_, prov := r.currentFramesArtifact()
	if fr, _ := prov["frames"].(map[string]any); fr["select"] != selectAPI {
		t.Errorf("provenance.frames = %v", fr)
	}
	if prov["provider"] != "anthropic" || prov["model"] != "m" || prov["executor"] != ExecutorAPI {
		t.Errorf("provenance = %v", prov)
	}
	if u, _ := prov["usage"].(map[string]any); u["input_tokens"] != float64(100) {
		t.Errorf("usage = %v", prov["usage"])
	}
	if pr, _ := prov["prompt"].(map[string]any); pr["id"] != "frames-default" || pr["sha256"] != "sha-fr1" {
		t.Errorf("prompt = %v", prov["prompt"])
	}
}

// FRM-R7：模型回傳不存在的編號時帶著錯誤說明重試，用盡後改用規則選圖（rules_fallback），影音顯示提醒與「重新選圖」。
func TestFramesVisionFallsBackToRules(t *testing.T) {
	fake := &framesLLM{outs: []string{`{"picks":[{"sheet":1,"n":99,"caption":"x"}]}`}}
	r := newVisionRig(t, fake)
	r.rerunFrames(apiFrames(nil), false)

	reqs := fake.requests()
	if len(reqs) < 2 || !strings.Contains(reqs[1].Retry, "第 1 張拼圖沒有編號 99") {
		t.Fatalf("重試沒有帶上錯誤：%d 個請求", len(reqs))
	}
	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusDone || st.Message != msgFramesFallback || !slices.Equal(st.Actions, []string{ActionRepickFrames}) {
		t.Fatalf("frames = %+v", st)
	}
	_, prov := r.currentFramesArtifact()
	if fr, _ := prov["frames"].(map[string]any); fr["select"] != selectRulesFallback {
		t.Errorf("provenance.frames = %v", fr)
	}
	if u, _ := prov["usage"].(map[string]any); u["input_tokens"] == nil {
		t.Errorf("退回規則也要記錄用量：%v", prov)
	}
	// 規則：N = 10，捨棄分數最低的第 3 張；圖說用字幕（第 1 張 30 秒：8 秒前結束的 cue 在 10 秒內）。
	v := framesView(t, r)
	if got, want := selectedNs(v), []int{1, 2, 4, 5, 6, 7, 8, 9, 10, 11}; !slices.Equal(got, want) {
		t.Errorf("選中 = %v，預期 %v", got, want)
	}
	if v.Frames[0].Caption != "Opening slide." {
		t.Errorf("rules_fallback 的圖說用字幕：%q", v.Frames[0].Caption)
	}
}

// FRM-R4：可選的候選少於範圍下限時全部選用，不需要模型（api 也一樣）。
func TestFramesShortfallSkipsTheModel(t *testing.T) {
	fake := &framesLLM{outs: []string{`{"picks":[]}`}}
	r := newFramesRig(t, framesRigOpts{scenes: mainScenes()[:3], llm: fake})
	r.rerunFrames(apiFrames(nil), false)
	if n := len(fake.requests()); n != 0 {
		t.Errorf("不該呼叫模型：%d 次", n)
	}
	v := framesView(t, r)
	if !v.Shortfall || v.SelectedCount != 3 || v.Target != 5 {
		t.Errorf("view = %+v", v)
	}
	st := latestStage(t, r.rig, r.item.ID, StageFrames)
	if st.Status != store.StatusDone || st.Executor != ExecutorAPI || st.Message != "只找到 3 張有意義的畫面，少於建議的 5 張。" {
		t.Errorf("frames = %+v", st)
	}
	if _, prov := r.currentFramesArtifact(); prov["frames"].(map[string]any)["select"] != selectAPI {
		t.Errorf("provenance = %v", prov)
	}
	// FRM-R9：沒有呼叫模型，圖說和 rules 一樣取該時間的 cue，不留空。
	for _, f := range v.Frames {
		if f.Selected && f.Caption == "" {
			t.Errorf("shortfall 的畫面 %d 沒有圖說", f.N)
		}
	}
}

// FRM-R13、R11：reuse_candidates 沿用現行的候選（不下載、不偵測、不擷取），只重新選圖；舊版本的目錄在新版本完成後刪除。
func TestFramesReuseCandidates(t *testing.T) {
	fake := &framesLLM{outs: []string{`{"picks":[{"sheet":1,"n":1,"caption":"重選的圖"}]}`}}
	r := newVisionRig(t, fake)
	r.rerunFrames(nil, false)
	old, prov := r.currentFramesArtifact()
	if fr, _ := prov["frames"].(map[string]any); fr["reused_candidates"] != false {
		t.Fatalf("第一次不是沿用：%v", fr)
	}
	oldDir := framesDir(r.dir, r.item.ID, old.ID)
	oldThumb, err := os.ReadFile(filepath.Join(oldDir, frameThumb(2)))
	if err != nil {
		t.Fatal(err)
	}
	calls := len(r.fm.extracted())

	r.rerunFrames(apiFrames(nil), true)
	if r.fm.sceneCount() != 1 || len(r.fm.extracted()) != calls || r.vyt.downloads() != 1 {
		t.Errorf("沒有重新偵測：場景 %d 次、擷取 %d→%d 次、下載 %d 次", r.fm.sceneCount(), calls, len(r.fm.extracted()), r.vyt.downloads())
	}
	cur, prov := r.currentFramesArtifact()
	if cur.ID == old.ID {
		t.Fatal("應該產生新版本")
	}
	if fr, _ := prov["frames"].(map[string]any); fr["reused_candidates"] != true || fr["select"] != selectAPI {
		t.Errorf("provenance.frames = %v", fr)
	}
	if prov["source_resolution"] != "640x360" || prov["extract_resolution"] != "640x360" || prov["duration_ms"] != float64(600_000) {
		t.Errorf("沿用候選時解析度與片長要承接：%v", prov)
	}
	if tv, _ := prov["tool_versions"].(map[string]any); !strings.Contains(tv["ffmpeg"].(string), "6.1.1") {
		t.Errorf("tool_versions = %v", prov["tool_versions"])
	}
	// 只保留現行版本（keep = 1）：舊目錄已刪除，新目錄的圖片與原本相同（hard link 或複製）。
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("舊版本的目錄應已刪除：%v", err)
	}
	if got := r.framesDirEntries(); len(got) != 1 || got[0] != cur.ID {
		t.Errorf("frames 目錄 = %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(framesDir(r.dir, r.item.ID, cur.ID), frameThumb(2))); err != nil || !bytes.Equal(b, oldThumb) {
		t.Errorf("沿用的縮圖內容不同：%v", err)
	}
	v := framesView(t, r)
	if v.CandidateCount != 11 || !slices.Equal(selectedNs(v), []int{1}) || v.Frames[0].Caption != "重選的圖" {
		t.Errorf("view = %+v", v)
	}
	// 沿用時的時間與分數承接舊的候選。
	if f := v.Frames[4]; f.AtMS != 150_000 || f.Score != 0.6 {
		t.Errorf("第 5 張 = %+v", f)
	}
	if reqs := fake.requests(); len(reqs) != 1 || len(reqs[0].Images) != 2 {
		t.Errorf("沿用的候選也要組成拼圖：%d 個請求", len(reqs))
	}
}

// FRM-R13：沒有現行的候選時忽略 reuse_candidates，照常下載與偵測。
func TestFramesReuseWithoutCurrentCandidates(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{scenes: mainScenes()})
	r.rerunFrames(nil, true)
	if r.fm.sceneCount() != 1 || r.vyt.downloads() != 1 {
		t.Errorf("場景偵測 %d 次、下載 %d 次", r.fm.sceneCount(), r.vyt.downloads())
	}
	if _, prov := r.currentFramesArtifact(); prov["frames"].(map[string]any)["reused_candidates"] != false {
		t.Errorf("provenance = %v", prov)
	}
	if st := latestStage(t, r.rig, r.item.ID, StageFrames); st.Status != store.StatusDone {
		t.Errorf("frames = %+v", st)
	}
}
