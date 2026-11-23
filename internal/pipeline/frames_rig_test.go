package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 關鍵畫面（FRM）的測試替身：假 ffmpeg／ffprobe（實作 toolexec.Runner，產生真的 JPEG）、
// 會下載影片的假 YouTube、回答 picks 的假 vision provider。單元測試不呼叫任何外部程式。

// fakeScene 是假影片的一個畫面：場景偵測在 At 秒回報 Score，擷取該時間的畫面是 Color 的純色。
type fakeScene struct {
	At    float64
	Score float64
	Color color.RGBA
}

// gridColor 是第 i 種顏色：三個色版各取 0、127、254，任兩種顏色的 RGB 平均差至少 0.16，遠大於預設的去重門檻 0.05。
func gridColor(i int) color.RGBA {
	return color.RGBA{R: uint8(i % 3 * 127), G: uint8(i / 3 % 3 * 127), B: uint8(i / 9 % 3 * 127), A: 255}
}

// frameMedia 是 ffmpeg 與 ffprobe 的替身：ffprobe 回報固定的影片長度與尺寸，場景偵測回報 scenes，
// 擷取時依 -ss 寫出「不晚於該時間的最後一個場景」的顏色（沒有場景時依秒數取 gridColor）。
type frameMedia struct {
	t  *testing.T
	mu sync.Mutex

	// width、height 是影像串流的尺寸；0 代表只有音訊串流。
	width, height int
	durationS     float64
	scenes        []fakeScene

	sceneCalls int
	// thresholds 記錄場景偵測收到的門檻（-vf 中的數字）。
	thresholds []string
	// frames 記錄每次擷取：時間、輸出的尺寸。
	frames []frameCall
	// sceneErr 非空時場景偵測以 exit 1 結束。
	sceneErr string
	// badFrame 為 true 時擷取寫出的不是 JPEG。
	badFrame bool
}

type frameCall struct {
	AtS  float64
	W, H int
	In   string
}

// newFrameMedia 建立 frameMedia 與用它組成的 media.Tool。
func newFrameMedia(t *testing.T) (*frameMedia, *media.Tool) {
	t.Helper()
	f := &frameMedia{t: t, width: 640, height: 360, durationS: 300}
	dir := t.TempDir()
	cfg := map[string]string{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg[name] = p
	}
	return f, media.New(f, toolexec.Resolver{Config: cfg, Vars: map[string]string{}})
}

func (f *frameMedia) sceneCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sceneCalls
}

func (f *frameMedia) extracted() []frameCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]frameCall(nil), f.frames...)
}

// extractedTimes 回傳每次擷取的秒數。
func (f *frameMedia) extractedTimes() []float64 {
	var out []float64
	for _, c := range f.extracted() {
		out = append(out, c.AtS)
	}
	return out
}

// colorAt 是 at 秒的畫面顏色：場景的時間用場景的顏色，其他時間（密度下限補上的）依秒數取不同的顏色。
func (f *frameMedia) colorAt(at float64) color.RGBA {
	for _, s := range f.scenes {
		if s.At-at < 0.0005 && at-s.At < 0.0005 {
			return s.Color
		}
	}
	return gridColor(int(at))
}

func (f *frameMedia) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tool := filepath.Base(c.Path)
	if hasArg(c.Args, "-version") {
		return toolexec.Result{Stdout: []byte(tool + " version 6.1.1 Copyright (c) the FFmpeg developers\n")}, nil
	}
	if tool == "ffprobe" {
		streams := `{"index":0,"codec_type":"audio","codec_name":"aac"}`
		if f.width > 0 {
			streams = fmt.Sprintf(`{"index":0,"codec_type":"video","codec_name":"h264","width":%d,"height":%d},`, f.width, f.height) + streams
		}
		out := fmt.Sprintf(`{"streams":[%s],"format":{"duration":"%v","format_name":"mov,mp4"}}`, streams, f.durationS)
		return toolexec.Result{Stdout: []byte(out)}, nil
	}
	vf := argAfter(c.Args, "-vf")
	switch {
	case strings.HasPrefix(vf, "select="):
		f.sceneCalls++
		f.thresholds = append(f.thresholds, strings.TrimSuffix(strings.TrimPrefix(vf, "select='gt(scene,"), ")',showinfo,metadata=print"))
		if f.sceneErr != "" {
			return toolexec.Result{ExitCode: 1, Stderr: []byte(f.sceneErr + "\n")}, nil
		}
		var sb strings.Builder
		for i, s := range f.scenes {
			fmt.Fprintf(&sb, "[Parsed_showinfo_1 @ 0x1] n:%4d pts:%d pts_time:%v pos:1 fmt:yuv420p\n", i, int(s.At*1000), s.At)
			fmt.Fprintf(&sb, "[Parsed_metadata_2 @ 0x1] lavfi.scene_score=%.6f\n", s.Score)
		}
		return toolexec.Result{Stderr: []byte(sb.String())}, nil
	case strings.HasPrefix(vf, "scale="):
		at, err := strconv.ParseFloat(argAfter(c.Args, "-ss"), 64)
		if err != nil {
			f.t.Fatalf("擷取的 -ss 不是數字：%q", c.Args)
		}
		var w, h int
		if _, err := fmt.Sscanf(vf, "scale=%d:%d", &w, &h); err != nil {
			f.t.Fatalf("擷取的 scale 不正確：%q", vf)
		}
		in := argAfter(c.Args, "-i")
		f.frames = append(f.frames, frameCall{AtS: at, W: w, H: h, In: in})
		body := solidJPEG(w, h, f.colorAt(at))
		if f.badFrame {
			body = []byte("not a jpeg")
		}
		if err := os.WriteFile(c.Args[len(c.Args)-1], body, 0o600); err != nil {
			f.t.Fatal(err)
		}
		return toolexec.Result{}, nil
	}
	f.t.Fatalf("frameMedia 不認得的 ffmpeg argv：%q", c.Args)
	return toolexec.Result{}, nil
}

// solidJPEG 回傳 w×h 純色的 JPEG。
func solidJPEG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// videoYT 在 fakeYT 之上加上 DownloadVideo（VideoDownloader），記錄下載並在 dir 寫出假的影片檔。
type videoYT struct {
	*fakeYT
	mu          sync.Mutex
	dirs        []string
	downloadErr error
}

func (v *videoYT) DownloadVideo(_ context.Context, _, dir string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.dirs = append(v.dirs, dir)
	if v.downloadErr != nil {
		return "", v.downloadErr
	}
	path := filepath.Join(dir, "video.mp4")
	return path, os.WriteFile(path, []byte("VIDEO"), 0o600)
}

func (v *videoYT) downloads() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.dirs)
}

// framesPrompts 把每個引用都解析成 frames 類型的第 1 版。
type framesPrompts struct{}

func (framesPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	return ResolvedPrompt{ID: id, Kind: StageFrames, Version: 1, SHA256: "sha-fr1", Body: "選圖。{{title}}"}, nil
}

// framesLLM 是假的 vision provider：依第幾次呼叫回答 picks，並記下每個請求。
type framesLLM struct {
	mu   sync.Mutex
	outs []string // 第 i 次呼叫的回答；超過時重複最後一個
	reqs []llm.Request
}

func (f *framesLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := min(len(f.reqs), len(f.outs)-1)
	f.reqs = append(f.reqs, req)
	return llm.Response{Output: json.RawMessage(f.outs[i]), Usage: llm.Usage{InputTokens: 100, OutputTokens: 10}}, nil
}

func (f *framesLLM) requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.reqs...)
}

// framesRigOpts 是 newFramesRig 的選項。
type framesRigOpts struct {
	scenes    []fakeScene
	durationS int // 影片長度（秒）；預設 300
	// width、height 是 ffprobe 回報的影像尺寸；預設 640x360，audioOnly 時沒有影像串流。
	width, height int
	audioOnly     bool
	// maxW、maxH 是 yt-dlp 格式清單中最高畫質的影像格式。
	maxW, maxH int
	llm        *framesLLM
	sponsor    *fakeSponsor
	chapters   []youtube.Chapter
	// frames 是 stages.frames 的設定（預設 executor: rules、min_interval_s: 3600，不補密度下限）。
	frames map[string]any
}

// framesRig 是已經做完 metadata、並 seed 了校正字幕的 YouTube 影音；接著從 frames 重跑（rerunFrames）。
type framesRig struct {
	*rig
	fm   *frameMedia
	vyt  *videoYT
	item store.Item
}

// testCues 是 seed 的校正字幕：第 2 句含預設的提示詞 "as you can see"。
func testCues() []subtitle.Cue {
	mk := func(i int, from, to int64, text string) subtitle.Cue {
		return subtitle.Cue{Index: i, StartMS: from, EndMS: to, Text: text, Timing: subtitle.TimingSource}
	}
	return []subtitle.Cue{
		mk(1, 18_000, 22_000, "Opening slide."),
		mk(2, 48_000, 52_000, "As you can see, the cache has two layers."),
		mk(3, 78_000, 82_000, "Eviction\npolicy."),
	}
}

func newFramesRig(t *testing.T, o framesRigOpts) *framesRig {
	t.Helper()
	fm, tool := newFrameMedia(t)
	fm.scenes = o.scenes
	if o.durationS == 0 {
		o.durationS = 300
	}
	fm.durationS = float64(o.durationS)
	if o.width > 0 {
		fm.width, fm.height = o.width, o.height
	}
	if o.audioOnly {
		fm.width, fm.height = 0, 0
	}
	if o.sponsor == nil {
		o.sponsor = &fakeSponsor{}
	}
	var vyt *videoYT
	r := newRig(t, func(d *Deps) {
		vyt = &videoYT{fakeYT: d.YouTube.(*fakeYT)}
		d.YouTube = vyt
		d.Media = tool
		d.SponsorBlock = o.sponsor
		if o.llm != nil {
			reg := llm.NewRegistry(nil, d.Clock)
			reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
			reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return o.llm }
			d.LLM = reg
			d.Prompts = framesPrompts{}
		}
	})
	dur := o.durationS
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	r.yt.info.DurationS = &dur
	r.yt.info.MaxVideoW, r.yt.info.MaxVideoH = o.maxW, o.maxH
	r.yt.info.Chapters = o.chapters
	cfg := map[string]any{"executor": "rules", "min_interval_s": 3600}
	for k, v := range o.frames {
		cfg[k] = v
	}
	over := map[string]any{"stages": map[string]any{StageFrames: cfg}}
	it := r.ingest(watch, over, StageMetadata)
	r.run()
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: subtitle.VariantCorrected, Lang: "en", Cues: testCues()}); err != nil {
		t.Fatal(err)
	}
	return &framesRig{rig: r, fm: fm, vyt: vyt, item: it}
}

// rerunFrames 從 frames 重跑並執行到沒有任務；沒有查過 SponsorBlock 時依 H8 從 sponsorblock 開始。
func (r *framesRig) rerunFrames(overrides map[string]any, reuse bool) {
	r.t.Helper()
	if _, err := r.p.Rerun(context.Background(), r.item.ID, RerunRequest{From: StageFrames, Overrides: overrides, ReuseCandidates: reuse}); err != nil {
		r.t.Fatal(err)
	}
	r.run()
}

// apiFrames 是把 frames 換成 vision 選圖的覆寫。
func apiFrames(extra map[string]any) map[string]any {
	cfg := map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "frames-default"}
	for k, v := range extra {
		cfg[k] = v
	}
	return map[string]any{"stages": map[string]any{StageFrames: cfg}}
}

// currentFramesArtifact 回傳影音現行的 frames 產出與它的來源紀錄。
func (r *framesRig) currentFramesArtifact() (store.Artifact, map[string]any) {
	r.t.Helper()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), r.item.ID, KindFrames)
	if err != nil || len(arts) != 1 {
		r.t.Fatalf("現行的 frames 產出 = %d 個，%v", len(arts), err)
	}
	var prov map[string]any
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		r.t.Fatal(err)
	}
	return arts[0], prov
}

// framesDirEntries 回傳 <item>/frames/ 底下的版本目錄名稱。
func (r *framesRig) framesDirEntries() []string {
	r.t.Helper()
	ents, err := os.ReadDir(filepath.Join(r.dir, artifactsDir, r.item.ID, framesSubdir))
	if err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
