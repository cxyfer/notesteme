package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/frames"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 關鍵畫面的產出與任務（FRM、ADR-0020 第 1、4 點）。
const (
	KindFrames = "frames"
	framesFmt  = "notesteme-frames+json"
	// frames 步驟的兩個任務（tasks.kind，FRM F1）：候選擷取（frames 資源類別）與選圖（rules 為 local、api 為 llm）。
	TaskFramesCandidates = "frames.candidates"
	TaskFramesSelect     = "frames.select"
	// ActionRepickFrames 是選圖退回規則時的動作（FRM-R7）。
	ActionRepickFrames = "重新選圖"
	// frames 子目錄：圖片放在 <data>/artifacts/<item>/frames/<版本>/。
	framesSubdir = "frames"
	// candidatesFile 是候選任務寫給選圖任務的中間結果（同一個版本目錄中）。
	candidatesFile = "candidates.json"
	// 擷取的上限（FRM-R8）。
	maxExtractW = 1920
	maxExtractH = 1080
	// filePerm 是圖片與中間結果的權限（同產出檔，只有服務帳號可讀）。
	filePerm = 0o600
)

// 選圖的方式（來源紀錄的 frames.select，FRM-R11）。
const (
	selectRules         = "rules"
	selectAPI           = "api"
	selectRulesFallback = "rules_fallback"
)

const (
	msgFramesFallback = "選圖的模型回傳了不存在的編號，已改用規則選圖。"
	msgFramesNoVideo  = "這部影音沒有影像，無法擷取關鍵畫面。"
)

// ErrNoFrames 表示影音沒有 frames 產出（步驟沒有執行過，FRM-R10 的 404 no_frames）。
var ErrNoFrames = errors.New("這部影音還沒有關鍵畫面。")

// VideoDownloader 是下載影片的能力（*youtube.Client 實作它）；Deps.YouTube 沒有實作時 YouTube 影片的 frames 以 permanent 失敗。
type VideoDownloader interface {
	DownloadVideo(ctx context.Context, videoID, dir string) (string, error)
}

// versionPattern 是版本目錄的名稱（產出 ID）：只允許 store.NewID 的字元，路徑不能被不可信的 payload 帶到別處。
var versionPattern = regexp.MustCompile(`^[0-9A-Za-z_-]{8,64}$`)

// framesPayload 是 frames 任務的 payload。
type framesPayload struct {
	ItemID string `json:"item_id"`
	// Version 是這次的版本目錄（之後的產出 ID）；選圖任務才有。
	Version string `json:"version,omitempty"`
	// Reuse 是 POST /rerun 的 reuse_candidates（FRM-R13）；只用於候選任務。
	Reuse bool `json:"reuse_candidates,omitempty"`
}

// frameCandidate 是一張去重後的候選畫面（candidates.json 與 manifest 共用）。
type frameCandidate struct {
	N     int     `json:"n"`
	AtMS  int64   `json:"at_ms"`
	Score float64 `json:"score"`
	File  string  `json:"file"`
	Thumb string  `json:"thumb"`
	// 以下只在 manifest 中有值。
	Selected bool   `json:"selected"`
	Excluded string `json:"excluded,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Chapter  string `json:"chapter,omitempty"`
}

// framesCandidates 是 candidates.json 的內容。
type framesCandidates struct {
	SourceResolution  string           `json:"source_resolution"`
	ExtractResolution string           `json:"extract_resolution"`
	Width             int              `json:"width"`
	Height            int              `json:"height"`
	DurationMS        int64            `json:"duration_ms"`
	FFmpeg            string           `json:"ffmpeg,omitempty"`
	Reused            bool             `json:"reused"`
	Candidates        []frameCandidate `json:"candidates"`
}

// framesManifest 是 frames 產出檔（ADR-0020 第 4 點）。
type framesManifest struct {
	SourceResolution  string           `json:"source_resolution"`
	ExtractResolution string           `json:"extract_resolution"`
	Width             int              `json:"width"`
	Height            int              `json:"height"`
	Target            int              `json:"target"`
	RangeMin          int              `json:"range_min"`
	RangeMax          int              `json:"range_max"`
	Shortfall         bool             `json:"shortfall"`
	Select            string           `json:"select"`
	Candidates        []frameCandidate `json:"candidates"`
}

// framesDir 是一個版本的圖片目錄。
func framesDir(dataDir, itemID, version string) string {
	return filepath.Join(dataDir, artifactsDir, itemID, framesSubdir, version)
}

func frameFile(n int) string  { return fmt.Sprintf("%03d.jpg", n) }
func frameThumb(n int) string { return fmt.Sprintf("%03d.thumb.jpg", n) }

// framesParams 讀出 stages.frames 的參數（FRM 介面），沒有設定的用預設值。
func framesParams(eff Effective) (frames.Params, error) {
	p := frames.Defaults()
	extras, err := stageExtras(eff, StageFrames)
	if err != nil {
		return p, err
	}
	num := func(k string) (float64, bool) {
		switch v := extras[k].(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			f, err := v.Float64()
			return f, err == nil
		}
		return 0, false
	}
	if v, ok := num("scene_threshold"); ok {
		p.SceneThreshold = v
	}
	if v, ok := num("min_interval_s"); ok {
		p.MinIntervalS = int(v)
	}
	if v, ok := num("dedupe_window"); ok {
		p.DedupeWindow = int(v)
	}
	if v, ok := num("dedupe_threshold"); ok {
		p.DedupeThreshold = v
	}
	if v, ok := num("max_frames"); ok {
		p.MaxFrames = int(v)
	}
	if v, ok := num("per_minute"); ok {
		p.PerMinute = v
	}
	if words, ok := extras["hint_words"].([]any); ok {
		p.HintWords = nil
		for _, w := range words {
			if s, ok := w.(string); ok {
				p.HintWords = append(p.HintWords, s)
			}
		}
	}
	return p, nil
}

// insertFramesTask 建立 frames 步驟的候選任務（frames 資源類別）；reuse 是 FRM-R13 的 reuse_candidates。
func insertFramesTask(ctx context.Context, r *store.Repo, itemID string, run store.StageRun, priority int, reuse bool) error {
	payload, err := json.Marshal(framesPayload{ItemID: itemID, Reuse: reuse})
	if err != nil {
		return err
	}
	_, err = r.InsertTask(ctx, store.NewTask{
		StageRunID: run.ID, Kind: TaskFramesCandidates, Executor: queue.ExecutorBuiltin,
		ResourceClass: queue.ResourceFrames, Priority: priority, Payload: payload, MaxAttempts: queue.DefaultMaxAttempts,
	})
	return err
}

// runFrames 分派 frames 的兩個任務。
func (p *Pipeline) runFrames(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective) (result, error) {
	var pl framesPayload
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &pl); err != nil {
			return result{}, errPermanent("frames 任務的 payload 不正確：" + err.Error())
		}
	}
	if t.Kind == TaskFramesSelect {
		if !versionPattern.MatchString(pl.Version) {
			return result{}, errPermanent("frames 任務的版本不正確")
		}
		return p.framesSelect(ctx, t, run, it, eff, pl.Version)
	}
	return p.framesCandidates(ctx, t, run, it, eff, pl.Reuse)
}

// framesCandidates 是候選任務（FRM-R2、R3、R8、R13）：下載影片（或沿用現行的候選）、場景偵測、擷取與去重，
// 寫出 candidates.json，之後建立選圖任務。影片沒有影像串流時步驟 skipped（audio_only）。
func (p *Pipeline) framesCandidates(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, reuse bool) (res result, err error) {
	params, err := framesParams(eff)
	if err != nil {
		return result{}, errPermanent(err.Error())
	}
	version := store.NewID()
	dir := framesDir(p.dataDir, it.ID, version)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return result{}, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	var cands *framesCandidates
	if reuse {
		cands, err = p.reuseCandidates(ctx, it.ID, dir)
		if err != nil {
			return result{}, err
		}
	}
	if cands == nil {
		cands, err = p.extractCandidates(ctx, it, dir, params)
		if err != nil {
			return result{}, err
		}
		if cands == nil {
			// 沒有影像串流（FRM-R1）：步驟略過，不留下目錄。
			_ = os.RemoveAll(dir)
			stage := &store.StageResult{Executor: stageExecutorOf(run), ImplVersion: p.implVersion(StageFrames)}
			return result{task: taskDone, advance: true, stage: stage, state: skippedState(SkipAudioOnly)}, nil
		}
	}
	body, err := json.Marshal(cands)
	if err != nil {
		return result{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, candidatesFile), body, filePerm); err != nil {
		return result{}, err
	}
	class := queue.ResourceLocal
	if run.Executor == ExecutorAPI {
		class = queue.ResourceLLM
	}
	payload, err := json.Marshal(framesPayload{ItemID: it.ID, Version: version})
	if err != nil {
		return result{}, err
	}
	return result{task: taskDone, keepState: true, afterDiscard: func() { _ = os.RemoveAll(dir) },
		inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
			_, err := tx.InsertTask(ctx, store.NewTask{
				StageRunID: run.ID, Kind: TaskFramesSelect, Executor: queue.ExecutorBuiltin, ResourceClass: class,
				Priority: t.Priority, Payload: payload, MaxAttempts: queue.DefaultMaxAttempts,
			})
			return err == nil, err
		}}, nil
}

// stageExecutorOf 是 stage run 記錄的 executor；沒有記錄時是 rules。
func stageExecutorOf(run store.StageRun) string {
	if run.Executor == "" {
		return ExecutorRules
	}
	return run.Executor
}

// reuseCandidates 把現行 frames 版本的候選圖片連到新的版本目錄（FRM-R13）；沒有現行的候選時回傳 nil。
// 舊版本的目錄在新版本完成後會被刪除，所以不能直接引用，用 hard link（不支援時複製）。
func (p *Pipeline) reuseCandidates(ctx context.Context, itemID, dir string) (*framesCandidates, error) {
	art, man, ok, err := p.currentFrames(ctx, itemID)
	if err != nil || !ok {
		return nil, err
	}
	old := framesDir(p.dataDir, itemID, art.ID)
	out := &framesCandidates{
		SourceResolution: man.SourceResolution, ExtractResolution: man.ExtractResolution,
		Width: man.Width, Height: man.Height, Reused: true,
	}
	var prov struct {
		ToolVersions map[string]string `json:"tool_versions"`
		DurationMS   int64             `json:"duration_ms"`
	}
	_ = json.Unmarshal(art.Provenance, &prov)
	out.FFmpeg, out.DurationMS = prov.ToolVersions["ffmpeg"], prov.DurationMS
	for i, c := range man.Candidates {
		n := i + 1
		for _, name := range []string{frameFile(n), frameThumb(n)} {
			if err := linkOrCopy(filepath.Join(old, name), filepath.Join(dir, name)); err != nil {
				return nil, fmt.Errorf("沿用候選畫面 %s：%w", name, err)
			}
		}
		out.Candidates = append(out.Candidates, frameCandidate{N: n, AtMS: c.AtMS, Score: c.Score, File: frameFile(n), Thumb: frameThumb(n)})
	}
	return out, nil
}

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// framesVideo 取得要擷取的影片檔與來源的最高畫質（寬、高；不知道時為 0）。
// YouTube：yt-dlp 下載到媒體快取（<data>/media/<id>/）；本機影片：原檔（F8）。其他平台以 permanent 失敗。
func (p *Pipeline) framesVideo(ctx context.Context, it store.Item) (path string, srcW, srcH int, err error) {
	switch it.Platform {
	case source.PlatformYouTube:
		dl, ok := p.yt.(VideoDownloader)
		if !ok || p.yt == nil {
			return "", 0, 0, errPermanent("沒有可以下載影片的 YouTube client")
		}
		info, err := p.yt.Probe(ctx, it.ExternalID)
		if err != nil {
			return "", 0, 0, err
		}
		dir, err := filepath.Abs(MediaCacheDir(p.dataDir, it.ID))
		if err != nil {
			return "", 0, 0, err
		}
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return "", 0, 0, err
		}
		path, err := dl.DownloadVideo(ctx, it.ExternalID, dir)
		return path, info.MaxVideoW, info.MaxVideoH, err
	case source.PlatformLocal:
		path := it.SourcePath
		if path == "" {
			return "", 0, 0, errPermanent("本機影音沒有檔案路徑")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.dataDir, filepath.FromSlash(path))
		}
		if p.files != nil {
			f, _, err := p.files.Open(path)
			if err != nil {
				// 影片檔不見時 permanent（F8，同 OUT 的 sidecar）。
				return "", 0, 0, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Err: err, Message: ErrMediaMissing.Error()}
			}
			_ = f.Close()
		}
		return path, 0, 0, nil
	}
	return "", 0, 0, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "frames 不支援平台 " + it.Platform,
		Message: "目前只有 YouTube 與本機影片可以擷取關鍵畫面。"}
}

// extractCandidates 執行場景偵測、補上密度下限、擷取每個候選並去重（FRM-R2、R3、R8）。沒有影像串流時回傳 nil。
func (p *Pipeline) extractCandidates(ctx context.Context, it store.Item, dir string, params frames.Params) (*framesCandidates, error) {
	if p.media == nil {
		return nil, errPermanent("沒有 ffmpeg")
	}
	in, srcW, srcH, err := p.framesVideo(ctx, it)
	if err != nil {
		return nil, err
	}
	info, err := p.media.Probe(ctx, in)
	if err != nil {
		return nil, err
	}
	w, h, ok := info.VideoSize()
	if !ok {
		return nil, nil
	}
	if srcW == 0 || srcW*srcH < w*h {
		srcW, srcH = w, h
	}
	ew, eh := frames.FitWithin(w, h, maxExtractW, maxExtractH)
	durMS := int64(math.Round(info.DurationS * 1000))
	if it.DurationS != nil && *it.DurationS > 0 && (durMS <= 0 || int64(*it.DurationS)*1000 < durMS) {
		durMS = int64(*it.DurationS) * 1000
	}
	stderr, err := p.media.DetectScenes(ctx, in, params.SceneThreshold)
	if err != nil {
		return nil, err
	}
	raw := frames.Candidates(frames.ParseShowinfo(stderr), durMS, params)
	sigs := make([]frames.Signature, len(raw))
	tmp := make([]string, len(raw))
	for i, c := range raw {
		tmp[i] = filepath.Join(dir, fmt.Sprintf("raw-%04d.jpg", i))
		if err := p.media.ExtractFrame(ctx, in, float64(c.AtMS)/1000, ew, eh, tmp[i]); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(tmp[i])
		if err != nil {
			return nil, err
		}
		img, err := frames.DecodeJPEG(b)
		if err != nil {
			return nil, errPermanent("擷取的畫面無法解碼：" + err.Error())
		}
		sigs[i] = frames.SignatureOf(img)
	}
	kept := frames.Dedupe(sigs, params.DedupeWindow, params.DedupeThreshold)
	out := &framesCandidates{
		SourceResolution: fmt.Sprintf("%dx%d", srcW, srcH), ExtractResolution: fmt.Sprintf("%dx%d", ew, eh),
		Width: ew, Height: eh, DurationMS: durMS, FFmpeg: p.media.Versions(ctx)["ffmpeg"],
	}
	keep := map[int]bool{}
	for k, i := range kept {
		n := k + 1
		keep[i] = true
		if err := os.Rename(tmp[i], filepath.Join(dir, frameFile(n))); err != nil {
			return nil, err
		}
		if err := writeThumb(filepath.Join(dir, frameFile(n)), filepath.Join(dir, frameThumb(n))); err != nil {
			return nil, err
		}
		out.Candidates = append(out.Candidates, frameCandidate{N: n, AtMS: raw[i].AtMS, Score: raw[i].Score, File: frameFile(n), Thumb: frameThumb(n)})
	}
	for i := range raw {
		if !keep[i] {
			_ = os.Remove(tmp[i])
		}
	}
	return out, nil
}

// writeThumb 從擷取的畫面產生寬 320 的縮圖（FRM-R10 的 size=thumb）。
func writeThumb(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	img, err := frames.DecodeJPEG(b)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := frames.EncodeJPEG(&buf, frames.Thumb(img)); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), filePerm)
}

// framesSelect 是選圖任務（FRM-R4～R7、R9、R11）：排除 SponsorBlock 片段內的候選，依 executor 用規則或 vision 模型選圖，
// 寫出 manifest 產出。vision 的驗證重試用盡時退回規則（rules_fallback）。
func (p *Pipeline) framesSelect(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, version string) (result, error) {
	dir := framesDir(p.dataDir, it.ID, version)
	b, err := os.ReadFile(filepath.Join(dir, candidatesFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result{}, errPermanent("找不到候選畫面，請重新執行 frames")
		}
		return result{}, err
	}
	var cands framesCandidates
	if err := json.Unmarshal(b, &cands); err != nil {
		return result{}, errPermanent("候選畫面的記錄不正確：" + err.Error())
	}
	params, err := framesParams(eff)
	if err != nil {
		return result{}, errPermanent(err.Error())
	}
	cfg, err := eff.Stage(StageFrames)
	if err != nil {
		return result{}, err
	}
	r := p.st.Read()
	durMS := cands.DurationMS
	if it.DurationS != nil && *it.DurationS > 0 {
		durMS = int64(*it.DurationS) * 1000
	}
	n := frames.Target(durMS, params)
	band := frames.BandFor(durMS)

	// 不參與選圖的片段（FRM-R6、SPB-R7）。讀不到時當作沒有片段，不讓整個步驟失敗。
	var segs []frames.Segment
	var parents []string
	if ss, ok, err := p.SponsorSegments(ctx, it.ID); err == nil && ok {
		for _, s := range ss {
			segs = append(segs, frames.Segment{StartMS: s.StartMS, EndMS: s.EndMS})
		}
		if art, ok, _ := p.currentSponsorArtifact(ctx, it.ID); ok {
			parents = append(parents, art.ID)
		}
	}
	cues, inputID := p.framesCues(ctx, r, it.ID)
	if inputID != "" {
		parents = append([]string{inputID}, parents...)
	}
	chapters := p.framesChapters(ctx, r, it)

	list := cands.Candidates
	eligible := make([]bool, len(list))
	scores := make([]float64, len(list))
	var nEligible int
	for i, c := range list {
		if frames.InSegment(c.AtMS, segs) {
			list[i].Excluded = "sponsor"
			continue
		}
		eligible[i] = true
		nEligible++
		scores[i] = frames.HintScore(frames.Candidate{AtMS: c.AtMS, Score: c.Score}, cues, params.HintWords)
	}

	executor := stageExecutorOf(run)
	mode := selectRules
	var picked []int
	captions := map[int]string{}
	state := store.StageState{Status: store.StatusDone}
	fields := map[string]any{}
	shortfall := nEligible < band.Min
	switch {
	case shortfall:
		// 有意義的畫面不夠時全部選用（FRM-R4），不需要模型。
		picked = frames.SelectRules(scores, eligible, nEligible)
		if executor == ExecutorAPI {
			mode = selectAPI
		}
	case executor == ExecutorAPI:
		sel, caps, usage, ok, err := p.framesVision(ctx, t, it, eff, cfg, dir, list, eligible, cues, n)
		if err != nil {
			return result{}, err
		}
		fields["usage"] = usage
		fields["provider"], fields["model"] = cfg.Provider, cfg.Model
		if ok {
			mode, picked, captions = selectAPI, sel, caps
		} else {
			mode = selectRulesFallback
			picked = frames.SelectRules(scores, eligible, n)
			state.Message, state.Actions = msgFramesFallback, []string{ActionRepickFrames}
		}
	default:
		picked = frames.SelectRules(scores, eligible, n)
	}
	if shortfall {
		state.Message = fmt.Sprintf("只找到 %d 張有意義的畫面，少於建議的 %d 張。", len(picked), band.Min)
	}
	for _, i := range picked {
		list[i].Selected = true
		caption := captions[i]
		if caption == "" && mode != selectAPI {
			caption = frames.CueAt(list[i].AtMS, cues)
		}
		list[i].Caption = frames.CleanCaption(caption)
		list[i].Chapter = frames.ChapterAt(list[i].AtMS, chapters)
	}
	man := framesManifest{
		SourceResolution: cands.SourceResolution, ExtractResolution: cands.ExtractResolution,
		Width: cands.Width, Height: cands.Height, Target: n, RangeMin: band.Min, RangeMax: band.Max,
		Shortfall: shortfall, Select: mode, Candidates: nonNilCandidates(list),
	}
	body, err := json.Marshal(man)
	if err != nil {
		return result{}, err
	}
	fields["stage"], fields["executor"], fields["impl_version"] = StageFrames, executor, p.implVersion(StageFrames)
	if executor == ExecutorAPI {
		fields["prompt"] = map[string]any{"id": cfg.Prompt, "version": cfg.PromptVersion, "sha256": cfg.PromptSHA256}
	}
	fields["frames"] = map[string]any{
		"select": mode, "scene_threshold": params.SceneThreshold, "min_interval_s": params.MinIntervalS,
		"dedupe_threshold": params.DedupeThreshold, "dedupe_window": params.DedupeWindow,
		"reused_candidates": cands.Reused,
	}
	fields["source_resolution"], fields["extract_resolution"] = cands.SourceResolution, cands.ExtractResolution
	fields["duration_ms"] = durMS
	tools := map[string]string{}
	if cands.FFmpeg != "" {
		tools["ffmpeg"] = cands.FFmpeg
	}
	prov, err := p.provenance(fields, tools)
	if err != nil {
		return result{}, err
	}
	rel, sha, size, err := writeArtifactFile(p.dataDir, it.ID, version, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return result{}, fmt.Errorf("寫入產出檔：%w", err)
	}
	_ = os.Remove(filepath.Join(dir, candidatesFile))
	art := &store.NewArtifact{
		ID: version, ItemID: it.ID, Kind: KindFrames, Variant: KindFrames, Format: framesFmt,
		Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
	}
	stage := &store.StageResult{Executor: executor, ImplVersion: p.implVersion(StageFrames)}
	return result{task: taskDone, advance: true, stage: stage, artifact: art, state: state}, nil
}

func nonNilCandidates(c []frameCandidate) []frameCandidate {
	if c == nil {
		return []frameCandidate{}
	}
	return c
}

// framesCues 讀出選圖用的字幕（translate 的輸入：校正後的字幕優先）；沒有字幕時為 nil。
func (p *Pipeline) framesCues(ctx context.Context, r *store.Repo, itemID string) ([]frames.Cue, string) {
	art, ok, err := translateInputFor(ctx, r, itemID, "")
	if err != nil || !ok {
		return nil, ""
	}
	tr, err := readTranscript(p.dataDir, art.Path, art.SHA256)
	if err != nil {
		return nil, ""
	}
	out := make([]frames.Cue, len(tr.Cues))
	for i, c := range tr.Cues {
		out[i] = frames.Cue{StartMS: c.StartMS, EndMS: c.EndMS, Text: c.Text}
	}
	return out, art.ID
}

// framesChapters 回傳章節（FRM-R9）：現行的 chapters 產出（CHAP），沒有時用平台章節，都沒有時為 nil。
func (p *Pipeline) framesChapters(ctx context.Context, r *store.Repo, it store.Item) []frames.Chapter {
	if arts, err := r.CurrentArtifacts(ctx, it.ID, "chapters"); err == nil && len(arts) > 0 {
		art := arts[len(arts)-1]
		if b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(art.Path))); err == nil {
			if chs, ok := parseChaptersDoc(b); ok {
				return chs
			}
		}
	}
	var md itemMetadata
	if len(it.Metadata) > 0 && json.Unmarshal(it.Metadata, &md) == nil {
		var out []frames.Chapter
		for _, c := range md.Chapters {
			out = append(out, frames.Chapter{StartMS: int64(math.Round(c.Start * 1000)), Title: c.Title})
		}
		return out
	}
	return nil
}

// parseChaptersDoc 解析 chapters 產出（ADR-0020 第 4 點：{origin, chapters: [{start_ms, title, cue}]}）。
func parseChaptersDoc(b []byte) ([]frames.Chapter, bool) {
	var doc struct {
		Chapters []struct {
			StartMS int64  `json:"start_ms"`
			Title   string `json:"title"`
		} `json:"chapters"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Chapters == nil {
		return nil, false
	}
	out := make([]frames.Chapter, 0, len(doc.Chapters))
	for _, c := range doc.Chapters {
		out = append(out, frames.Chapter{StartMS: c.StartMS, Title: c.Title})
	}
	return out, true
}

// framesSpec 是 vision 選圖的輸出格式（FRM-R7）：{picks: [{sheet, n, caption}]}。
// schema 不限制 sheet 與 n 的範圍：超出範圍要由 Check 給出「第 1 張拼圖沒有編號 12」這樣的說明（LLM-R5）。
func framesSpec(sizes []int, limit int) *llm.Spec {
	return &llm.Spec{
		Stage: StageFrames,
		Schema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"picks"},
			"properties": map[string]any{"picks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"sheet", "n", "caption"},
					"properties": map[string]any{
						"sheet": map[string]any{"type": "integer"}, "n": map[string]any{"type": "integer"},
						"caption": map[string]any{"type": "string"},
					},
				},
			}},
		},
		OutputRules: fmt.Sprintf("picks 最多 %d 張；sheet 是拼圖的編號（從 1 起算），n 是格子左上角的編號；caption 是一句簡短的圖說。不要輸出時間。", limit),
		Check: func(_ llm.ChunkInput, out json.RawMessage) *llm.ValidationError {
			picks, err := frames.ParsePicks(out)
			if err != nil {
				return &llm.ValidationError{Reason: llm.ReasonSchema, Message: err.Error()}
			}
			if err := frames.ValidatePicks(picks, sizes, limit); err != nil {
				return &llm.ValidationError{Reason: "invalid_pick", Message: err.Error()}
			}
			return nil
		},
	}
}

// framesVision 以 vision 模型選圖（FRM-R7、F5）：可選的候選依時間每 9 張組成一張加上編號的拼圖，
// 候選附近的字幕放在資料區塊中（每項文字以「[拼圖 s 編號 n]」開頭）。回傳選中的候選索引與圖說；
// 驗證重試用盡時 ok 為 false（由呼叫端退回規則）。
func (p *Pipeline) framesVision(ctx context.Context, t store.Task, it store.Item, eff Effective, cfg StageConfig, dir string,
	list []frameCandidate, eligible []bool, cues []frames.Cue, limit int) (picked []int, captions map[int]string, usage llm.Usage, ok bool, err error) {
	var order []int
	for i := range list {
		if eligible[i] {
			order = append(order, i)
		}
	}
	var sheets []llm.Image
	var sizes []int
	var dataCues []llm.Cue
	for s := 0; s*frames.SheetSize < len(order); s++ {
		group := order[s*frames.SheetSize : min((s+1)*frames.SheetSize, len(order))]
		imgs := make([]image.Image, 0, len(group))
		for k, i := range group {
			b, err := os.ReadFile(filepath.Join(dir, frameThumb(list[i].N)))
			if err != nil {
				return nil, nil, usage, false, err
			}
			img, err := frames.DecodeJPEG(b)
			if err != nil {
				return nil, nil, usage, false, errPermanent("候選畫面無法解碼：" + err.Error())
			}
			imgs = append(imgs, img)
			text := fmt.Sprintf("[拼圖 %d 編號 %d] %s", s+1, k+1, nearbyText(list[i].AtMS, cues))
			dataCues = append(dataCues, llm.Cue{I: len(dataCues) + 1, Text: text})
		}
		var buf bytes.Buffer
		if err := frames.EncodeJPEG(&buf, frames.ContactSheet(imgs)); err != nil {
			return nil, nil, usage, false, err
		}
		sheets = append(sheets, llm.Image{MediaType: "image/jpeg", Data: buf.Bytes()})
		sizes = append(sizes, len(group))
	}
	spec := framesSpec(sizes, limit)
	rp, err := p.stagePrompt(ctx, cfg)
	if err != nil {
		return nil, nil, usage, false, err
	}
	lang := it.OriginalLang
	fixed, err := llm.BuildFixed(spec, rp.Body, llm.TemplateVars{SourceLang: lang}, llm.Metadata{Title: it.Title})
	if err != nil {
		return nil, nil, usage, false, errPermanent("prompt 無法渲染：" + err.Error())
	}
	settings, err := eff.LLM()
	if err != nil {
		return nil, nil, usage, false, err
	}
	client, err := p.llm.Client(ctx, cfg.Provider)
	if err != nil {
		return nil, nil, usage, false, err
	}
	prov, _ := p.llm.Provider(cfg.Provider)
	limiter := p.llm.Limiter
	out, err := llm.RunChunk(ctx, llm.RunOptions{
		Client: client, Spec: spec, Model: cfg.Model, Fixed: fixed, Images: sheets,
		Input:      llm.ChunkInput{Chunk: llm.Chunk{Index: 1}, Chunks: 1, Cues: dataCues},
		MaxRetries: settings.MaxValidationRetries, Timeout: p.chunkTimeout(t),
		BeforeRequest: func(_ context.Context, attempt int, req llm.Request) error {
			tokens := llm.RequestTokens(req)
			if attempt > 0 {
				limiter.Charge(prov.ID, prov.Limits, tokens)
				return nil
			}
			if wait := limiter.Reserve(prov.ID, prov.Limits, tokens); wait > 0 {
				return &errclass.Error{
					Class: errclass.ClassWait, Source: "llm:" + prov.ID, Detail: "速率限制", RetryAfter: wait,
					Message: fmt.Sprintf("等待 LLM provider %s 的速率限制，稍後會繼續。", prov.ID),
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, nil, out.Usage, false, err
	}
	if out.Output == nil {
		p.log.Warn("選圖的驗證重試用盡，改用規則選圖", "item", it.ID, "detail", out.Fallback.Message)
		return nil, nil, out.Usage, false, nil
	}
	picks, err := frames.ParsePicks(out.Output)
	if err != nil {
		return nil, nil, out.Usage, false, nil
	}
	captions = map[int]string{}
	for _, pk := range picks {
		i := order[(pk.Sheet-1)*frames.SheetSize+pk.N-1]
		picked = append(picked, i)
		captions[i] = pk.Caption
	}
	slices.Sort(picked)
	return picked, captions, out.Usage, true, nil
}

// nearbyText 串接候選前後 10 秒內的字幕（最多 300 字），給模型參考（不可信的資料，放在資料區塊中）。
func nearbyText(at int64, cues []frames.Cue) string {
	var b []rune
	for _, q := range cues {
		if q.EndMS < at-frames.HintWindowMS || q.StartMS > at+frames.HintWindowMS {
			continue
		}
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = append(b, []rune(q.Text)...)
		if len(b) >= 300 {
			return string(b[:300])
		}
	}
	return string(b)
}

// currentFrames 回傳影音現行的 frames 產出與 manifest；沒有時 ok 為 false。
func (p *Pipeline) currentFrames(ctx context.Context, itemID string) (store.Artifact, framesManifest, bool, error) {
	arts, err := p.st.Read().CurrentArtifacts(ctx, itemID, KindFrames)
	if err != nil || len(arts) == 0 {
		return store.Artifact{}, framesManifest{}, false, err
	}
	art := arts[len(arts)-1]
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(art.Path)))
	if err != nil {
		return store.Artifact{}, framesManifest{}, false, err
	}
	if sha256Hex(b) != art.SHA256 {
		return store.Artifact{}, framesManifest{}, false, fmt.Errorf("%s：%w", art.Path, ErrCorruptArtifact)
	}
	var man framesManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return store.Artifact{}, framesManifest{}, false, err
	}
	return art, man, true, nil
}

// FrameInfo 是 GET /api/items/:id/frames 的一張畫面（FRM-R10）。
type FrameInfo struct {
	N        int
	AtMS     int64
	Score    float64
	Selected bool
	Excluded string
	Caption  string
	Chapter  string
	Width    int
	Height   int
}

// FramesView 是 GET /api/items/:id/frames 的內容（FRM-R10）。
type FramesView struct {
	Target, RangeMin, RangeMax int
	CandidateCount             int
	SelectedCount              int
	Shortfall                  bool
	Frames                     []FrameInfo
}

// Frames 回傳影音現行的關鍵畫面。錯誤：store.ErrNotFound、*InTrashError、ErrNoFrames（步驟沒有執行過）。
func (p *Pipeline) Frames(ctx context.Context, itemID string) (FramesView, error) {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return FramesView{}, err
	}
	if it.DeletedAt != nil {
		return FramesView{}, &InTrashError{ItemID: it.ID}
	}
	_, man, ok, err := p.currentFrames(ctx, it.ID)
	if err != nil {
		return FramesView{}, err
	}
	if !ok {
		return FramesView{}, ErrNoFrames
	}
	v := FramesView{Target: man.Target, RangeMin: man.RangeMin, RangeMax: man.RangeMax, CandidateCount: len(man.Candidates), Shortfall: man.Shortfall}
	for i, c := range man.Candidates {
		if c.Selected {
			v.SelectedCount++
		}
		v.Frames = append(v.Frames, FrameInfo{
			N: i + 1, AtMS: c.AtMS, Score: c.Score, Selected: c.Selected, Excluded: c.Excluded,
			Caption: c.Caption, Chapter: c.Chapter, Width: man.Width, Height: man.Height,
		})
	}
	return v, nil
}

// FrameImage 開啟第 n 張畫面的圖片（thumb 為 true 時是寬 320 的縮圖），供 API 以 http.ServeContent 回應。
// 路徑只由驗證過的 n 產生，在版本目錄內以 os.OpenRoot 開啟，不使用 manifest 中的檔名。呼叫端必須關閉回傳的檔案。
// 錯誤：同 Frames；n 不存在時是 ErrFrameNotFound。回傳的 etag 含引號。
func (p *Pipeline) FrameImage(ctx context.Context, itemID string, n int, thumb bool) (f *os.File, fi os.FileInfo, etag string, err error) {
	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return nil, nil, "", err
	}
	if it.DeletedAt != nil {
		return nil, nil, "", &InTrashError{ItemID: it.ID}
	}
	art, man, ok, err := p.currentFrames(ctx, it.ID)
	if err != nil {
		return nil, nil, "", err
	}
	if !ok {
		return nil, nil, "", ErrNoFrames
	}
	if n < 1 || n > len(man.Candidates) {
		return nil, nil, "", ErrFrameNotFound
	}
	if !versionPattern.MatchString(art.ID) {
		return nil, nil, "", ErrFrameNotFound
	}
	root, err := os.OpenRoot(framesDir(p.dataDir, it.ID, art.ID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, "", ErrFrameNotFound
		}
		return nil, nil, "", err
	}
	defer root.Close()
	name, kind := frameFile(n), "full"
	if thumb {
		name, kind = frameThumb(n), "thumb"
	}
	f, err = root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, "", ErrFrameNotFound
		}
		return nil, nil, "", err
	}
	fi, err = f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = ErrFrameNotFound
		}
		return nil, nil, "", err
	}
	return f, fi, `"` + art.SHA256[:16] + "-" + strconv.Itoa(n) + "-" + kind + `"`, nil
}

// ErrFrameNotFound 表示沒有這張畫面（n 超出範圍）。
var ErrFrameNotFound = errors.New("沒有這張畫面。")

// FrameTimeText 把毫秒寫成 HH:MM:SS.mmm（FRM-R10 的 at）。
func FrameTimeText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	s := int(d/time.Second) % 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
}
