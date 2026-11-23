package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
)

// Gemini 直接生成（GEM）：stages.note.engine 為 gemini 時，note 步驟把 YouTube 網址交給 Gemini 寫筆記，
// transcript 到 frames 都略過（GEM-R2）。

// GeminiDefaultMaxDuration 是 stages.note.gemini.max_duration 的預設值（GEM-R3、G1）。
const GeminiDefaultMaxDuration = time.Hour

// 給使用者看的動作（GEM-R3、R5、R7）。
const (
	ActionGeminiFallback = llm.ActionGeminiFallback
	ActionRegenerateNote = "重新產生筆記"
)

// geminiTokensPerSecond 是影片輸入的 token 估計，只用於 TPM 限流的預扣（實際值等 M0 實測，G1）。
const geminiTokensPerSecond = 300

// geminiSkippedStages 是 engine: gemini 時略過的步驟（GEM-R2）；metadata、sponsorblock、note、publish 照常執行。
var geminiSkippedStages = map[string]bool{
	StageTranscript: true, StageNormalize: true, StageSegment: true, StageCorrect: true,
	StageTranslate: true, StageChapters: true, StageFrames: true,
}

// availabilityPattern 是可以放進訊息的 availability（yt-dlp 的值，例如 unlisted、subscriber_only）。
var availabilityPattern = regexp.MustCompile(`^[a-z_]{1,32}$`)

// geminiEngine 回報生效設定的 note 是否用 Gemini 直接生成：executor 是 api 而且 engine 是 gemini（GEM-R1）。
// agent 與 off 不看 engine。
func geminiEngine(eff Effective) bool {
	cfg, err := eff.Stage(StageNote)
	if err != nil || cfg.Executor != ExecutorAPI {
		return false
	}
	extras, err := stageExtras(eff, StageNote)
	if err != nil {
		return false
	}
	return extras["engine"] == NoteEngineGemini
}

// geminiNoteRes 回報 note 的解析結果是不是 Gemini 直接生成（planChain、Handle、Rerun 的實作判斷共用）。
func geminiNoteRes(eff Effective, stage, executor string) bool {
	return stage == StageNote && executor == ExecutorAPI && geminiEngine(eff)
}

// geminiMaxDuration 讀出 stages.note.gemini.max_duration；沒有設定或格式不對時用預設值（格式由處理方案驗證）。
func geminiMaxDuration(eff Effective) time.Duration {
	extras, err := stageExtras(eff, StageNote)
	if err != nil {
		return GeminiDefaultMaxDuration
	}
	g, _ := extras["gemini"].(map[string]any)
	s, _ := g["max_duration"].(string)
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return GeminiDefaultMaxDuration
}

// geminiFileURI 是交給 Gemini 的影片網址（GEM-R4）：一律用正規化的 watch 網址，不用使用者輸入的網址。
func geminiFileURI(it store.Item) string {
	return "https://www.youtube.com/watch?v=" + it.ExternalID
}

// geminiClock 寫成 GEM-R3 的長度格式：H:MM:SS（不足 1 小時時 MM:SS）。
func geminiClock(d time.Duration) string { return llm.GeminiClock(d.Milliseconds()) }

func geminiNeedsAction(detail, msg string) error {
	return &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "gemini", Detail: detail, Message: msg, Actions: []string{ActionGeminiFallback},
	}
}

// geminiPrecheck 是呼叫前的檢查（GEM-R3）：不是 YouTube、不是公開影片、片長超過上限時不送出。
func geminiPrecheck(it store.Item, maxDur time.Duration) error {
	if it.Platform != source.PlatformYouTube || !sponsorblock.ValidVideoID(it.ExternalID) {
		return geminiNeedsAction("不是 YouTube 影片", "Gemini 直接生成只支援 YouTube 影片。")
	}
	if a := it.Availability; a != "" && a != "public" {
		if !availabilityPattern.MatchString(a) {
			a = "unknown"
		}
		return geminiNeedsAction("availability "+a, fmt.Sprintf("Gemini 只能讀取公開的 YouTube 影片，這部影片的狀態是 %s。", a))
	}
	if ms := durationMS(it); ms > 0 && time.Duration(ms)*time.Millisecond > maxDur {
		d := time.Duration(ms) * time.Millisecond
		return geminiNeedsAction("片長超過上限", fmt.Sprintf("影片長度 %s 超過 Gemini 直接生成的上限 %s。", geminiClock(d), geminiClock(maxDur)))
	}
	return nil
}

// geminiNote 是通過 GEM-R5 驗證的筆記與它的來源資訊，交給筆記的渲染（NOTE 的模板、跳脫與自動檢查，GEM-R6）。
type geminiNote struct {
	Output   llm.GeminiNoteOutput
	Provider string
	Model    string
	FileURI  string
	// Parents 是來源紀錄的 parents：metadata，有片段時加 sponsor_segments（GEM-R6）。
	Parents []string
	Usage   llm.Usage
	// Requests 是這次送出的請求數（含驗證重試）。
	Requests int
}

// runGeminiNote 執行 engine: gemini 的 note 任務（GEM-R3～R7）。
func (p *Pipeline) runGeminiNote(ctx context.Context, t store.Task, it store.Item, eff Effective) (result, error) {
	cfg, err := eff.Stage(StageNote)
	if err != nil {
		return result{}, err
	}
	if err := geminiPrecheck(it, geminiMaxDuration(eff)); err != nil {
		return result{}, err
	}
	if p.llm == nil {
		return result{}, errPermanent("沒有 LLM provider 的設定")
	}
	prov, ok := p.llm.Provider(cfg.Provider)
	if !ok {
		return result{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "llm", Detail: "找不到 LLM provider " + cfg.Provider,
			Message: fmt.Sprintf("找不到 LLM provider「%s」。", cfg.Provider)}
	}
	if prov.Type != llm.TypeGemini {
		// 處理方案已經檢查過（GEM-R1）；設定檔之後把同名的 provider 改成其他 type 時在這裡失敗。
		return result{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "gemini", Detail: "provider type " + prov.Type,
			Message: "engine: gemini 需要 type 為 gemini 的 provider。"}
	}
	client, err := p.llm.Client(ctx, cfg.Provider)
	if err != nil {
		return result{}, err
	}
	rp, err := p.stagePrompt(ctx, cfg)
	if err != nil {
		return result{}, err
	}
	settings, err := eff.LLM()
	if err != nil {
		return result{}, err
	}
	lang, err := eff.Language()
	if err != nil {
		return result{}, err
	}
	meta, parents, err := p.geminiMetadata(ctx, it)
	if err != nil {
		return result{}, err
	}
	dur := durationMS(it)
	spec := llm.GeminiNoteSpec(dur)
	fixed, err := llm.BuildGeminiFixed(spec, rp.Body, llm.GeminiVars{SourceLang: it.OriginalLang, NoteLang: noteLang(eff, lang, it)}, meta)
	if err != nil {
		return result{}, errPermanent("組出 Gemini 的指示失敗：" + err.Error())
	}
	videoTokens := int(dur/1000) * geminiTokensPerSecond
	limiter := p.llm.Limiter
	m := p.newMeter(t, store.StageRun{ID: t.StageRunID, Stage: StageNote}, it, cfg.Provider, cfg.Model)
	out, err := llm.RunGemini(ctx, llm.GeminiOptions{
		Client: client, Spec: spec, Model: cfg.Model, Fixed: fixed, FileURI: geminiFileURI(it),
		MaxRetries: settings.MaxValidationRetries, Timeout: p.chunkTimeout(t), AfterRequest: m.afterFunc(),
		BeforeRequest: m.then(func(_ context.Context, attempt int, req llm.Request) error {
			tokens := llm.RequestTokens(req) + videoTokens
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
		}),
	})
	if err != nil {
		return result{}, err
	}
	if out.Output == nil {
		p.log.Warn("Gemini 筆記的驗證重試用盡", "item", it.ID, "reason", out.Fallback.Reason, "detail", out.Fallback.Message)
		return result{}, &errclass.Error{
			Class: errclass.ClassPermanent, Source: "gemini", Detail: "驗證重試用盡：" + out.Fallback.Reason,
			Message: fmt.Sprintf("Gemini 回傳的筆記沒有通過驗證（%s）。", out.Fallback.Message),
			Actions: []string{ActionRegenerateNote, ActionGeminiFallback},
		}
	}
	note, verr := llm.CheckGeminiNote(out.Output, dur)
	if verr != nil {
		return result{}, errPermanent("Gemini 的筆記在驗證後無法解析：" + verr.Message)
	}
	return p.writeGeminiNote(ctx, it, eff, geminiNote{
		Output: note, Provider: prov.ID, Model: cfg.Model, FileURI: geminiFileURI(it), Parents: parents,
		Usage: out.Usage, Requests: out.Requests,
	})
}

// noteLang 是筆記的語言（NOTE-R5）：stages.note.lang，沒有時用 language.targets 的第一個，再沒有時用原文語言。
func noteLang(eff Effective, lang Language, it store.Item) string {
	if extras, err := stageExtras(eff, StageNote); err == nil {
		if s, ok := extras["lang"].(string); ok && s != "" {
			return s
		}
	}
	if len(lang.Targets) > 0 {
		return lang.Targets[0]
	}
	return it.OriginalLang
}

// geminiMetadata 組出 <video_metadata>（GEM-R4）與來源紀錄的 parents（GEM-R6）。
func (p *Pipeline) geminiMetadata(ctx context.Context, it store.Item) (llm.GeminiMetadata, []string, error) {
	var md itemMetadata
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md)
	}
	meta := llm.GeminiMetadata{Title: it.Title, Channel: md.Channel}
	if ms := durationMS(it); ms > 0 {
		meta.Duration = llm.GeminiClock(ms)
	}
	for _, c := range platformChapterList(it) {
		meta.Chapters = append(meta.Chapters, llm.ChapterInfo{Start: llm.GeminiClock(c.StartMS), Title: c.Title})
	}
	parents := []string{StageMetadata}
	art, segs, err := p.chaptersSponsor(ctx, it.ID)
	if err != nil {
		return meta, nil, err
	}
	if art != nil && len(segs) > 0 {
		parents = append(parents, "sponsor_segments")
		for _, s := range segs {
			meta.SkipRanges = append(meta.SkipRanges, llm.GeminiSkipRange{
				Start: llm.GeminiClock(s.StartMS), End: llm.GeminiClock(s.EndMS), Category: s.Category,
			})
		}
	}
	return meta, parents, nil
}

// GeminiNoteRendererMissing 是 NOTE（M3-S4）合併之前 Gemini 筆記的訊息（GEM G9）。M3-S4 之後 writeGeminiNote
// （gemini_note.go）用 NOTE 的渲染寫出筆記，不再出現這個訊息；保留常數讓驗收 harness 的判斷仍可編譯。
const GeminiNoteRendererMissing = "Gemini 的筆記通過驗證，但筆記模板還沒有實作（M3-S4）。"
