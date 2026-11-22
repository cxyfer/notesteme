package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// pipeline 的步驟，依執行順序（plan 第 5 節、ADR-0020；summary 屬於 M4）。
const (
	StageMetadata     = "metadata"
	StageTranscript   = "transcript"
	StageNormalize    = "normalize"
	StageSegment      = "segment"
	StageCorrect      = "correct"
	StageTranslate    = "translate"
	StageSponsorBlock = "sponsorblock"
	StageChapters     = "chapters"
	StageFrames       = "frames"
	StageNote         = "note"
	StagePublish      = "publish"
)

// Stages 是步驟的執行順序（M3 的四個步驟接在 translate 之後、publish 之前，ADR-0020 第 1 點）。
var Stages = []string{
	StageMetadata, StageTranscript, StageNormalize, StageSegment, StageCorrect, StageTranslate,
	StageSponsorBlock, StageChapters, StageFrames, StageNote, StagePublish,
}

// plannedStages 是 plan 第 5 節中還沒排進來的步驟；重跑它們回傳 501，名稱不對則是 422。
// frames.candidates、frames.select 已合併成 frames（ADR-0020 第 1 點），現在是不認識的名稱（422）。
var plannedStages = []string{"summary"}

// IsM3Stage 回報步驟是不是 M3 的步驟（sponsorblock、chapters、frames、note，ADR-0020）。
// 和 IsLLMStage 分開：IsLLMStage 決定 M2 的 LLM 引擎、AGT 與資源類別的路由，M3 的步驟由各自的切片接上。
func IsM3Stage(stage string) bool {
	return stage == StageSponsorBlock || stage == StageChapters || stage == StageFrames || stage == StageNote
}

// 步驟的 executor（stage_runs.executor、來源紀錄的 executor）。
const (
	ExecutorLocal  = "local" // 確定性步驟（M1 已經寫成 "local"）
	ExecutorAPI    = "api"
	ExecutorAgent  = "agent"
	ExecutorOff    = "off"
	ExecutorOpenCC = "opencc" // 只用於 translate
	ExecutorRules  = "rules"  // 只用於 frames 的規則選圖（FRM-R1）
)

// skipped 的原因，寫在 stage_runs.block_reason。
// not_applicable 見 publish.go（SkipNotApplicable）。
const (
	SkipCancelled     = "cancelled"      // 取消或被較新的重跑取代（PIPE-R15）
	SkipOff           = "off"            // executor: off（PRF-R6）
	SkipNotConfigured = "not_configured" // api 沒有 provider 或 model（X1 選項 C）
	SkipNoOutputs     = "no_outputs"     // publish 沒有任何輸出目標
	SkipSameLanguage  = "same_language"  // translate 的每個目標都與原文相同（TRN-R10）
	// SkipManualCaptions：來源是人工字幕，segment 預設略過（SEG-R1）。
	SkipManualCaptions = "manual_captions"
	// M3 的略過原因（ADR-0020 第 2 點）；由各自的切片判斷，S0 只登記名稱。
	SkipPlatformChapters = "platform_chapters" // 平台已有 2 個以上的章節（CHAP-R1）
	SkipAudioOnly        = "audio_only"        // 沒有影像串流（FRM-R1）
	SkipGemini           = "gemini"            // stages.note.engine 為 gemini（GEM-R2）
	// SkipNotImplemented 是暫時的原因（M3-S0）：設定要求執行、但這個 M3 步驟還沒有實作。
	// 前進時不建任務、直接 skipped，M1、M2 的影音才能做到 publish；各切片在 stageImplemented 登記後就不會再出現。
	SkipNotImplemented = "not_implemented"
)

// IsLLMStage 回報步驟是不是 LLM 步驟（ADR-0002）。
func IsLLMStage(stage string) bool {
	return stage == StageSegment || stage == StageCorrect || stage == StageTranslate
}

// StageExecutors 回傳步驟可以設定的 executor；確定性步驟回傳 nil（不能設定）。
func StageExecutors(stage string) []string {
	switch stage {
	case StageSegment, StageCorrect:
		return []string{ExecutorAPI, ExecutorAgent, ExecutorOff}
	case StageTranslate:
		return []string{ExecutorAPI, ExecutorAgent, ExecutorOff, ExecutorOpenCC}
	case StageChapters, StageNote:
		return []string{ExecutorAPI, ExecutorAgent, ExecutorOff}
	case StageFrames:
		return []string{ExecutorRules, ExecutorAPI, ExecutorAgent, ExecutorOff}
	}
	return nil
}

// 每個步驟宣告的版本（plan 第 5.1 節）：演算法的行為改變時遞增，舊的快取就不再命中（PIPE-R6）。
// 這是預設值；測試可以經 Deps.ImplVersions 或 Pipeline.SetImplVersion 覆寫，模擬步驟改版。
const (
	metadataImplVersion   = 1
	transcriptImplVersion = 1
	normalizeImplVersion  = 1
	segmentImplVersion    = 1
	correctImplVersion    = 1
	translateImplVersion  = 1
	publishImplVersion    = 1
	// M3 的步驟（ADR-0020）。
	sponsorblockImplVersion = 1
	chaptersImplVersion     = 1
	framesImplVersion       = 1
	noteImplVersion         = 1
)

// defaultImplVersion 是步驟宣告的 impl_version。
func defaultImplVersion(stage string) int {
	switch stage {
	case StageMetadata:
		return metadataImplVersion
	case StageTranscript:
		return transcriptImplVersion
	case StageNormalize:
		return normalizeImplVersion
	case StageSegment:
		return segmentImplVersion
	case StageCorrect:
		return correctImplVersion
	case StageTranslate:
		return translateImplVersion
	case StagePublish:
		return publishImplVersion
	case StageSponsorBlock:
		return sponsorblockImplVersion
	case StageChapters:
		return chaptersImplVersion
	case StageFrames:
		return framesImplVersion
	case StageNote:
		return noteImplVersion
	}
	return 0
}

// implVersion 回傳步驟目前生效的 impl_version：有覆寫用覆寫值，否則用宣告的預設值。
func (p *Pipeline) implVersion(stage string) int {
	p.implMu.RLock()
	defer p.implMu.RUnlock()
	if v, ok := p.impl[stage]; ok {
		return v
	}
	return defaultImplVersion(stage)
}

// SetImplVersion 覆寫步驟的 impl_version，之後新建立的快取鍵與 stage run 都用這個值（PIPE-R6）。
// 只給驗收測試的 harness 使用（經 app.Server.SetImplVersion）；不對應任何 HTTP 端點。
func (p *Pipeline) SetImplVersion(stage string, v int) error {
	if !ValidStage(stage) {
		return invalidf("不認識的步驟 %q。", stage)
	}
	if v < 1 {
		return invalidf("impl_version 必須至少是 1，收到 %d。", v)
	}
	p.implMu.Lock()
	defer p.implMu.Unlock()
	if p.impl == nil {
		p.impl = map[string]int{}
	}
	p.impl[stage] = v
	return nil
}

// resourceClass 是步驟使用的資源類別（ADR-0004）。
// transcriptASR 只對 transcript 有意義：true 代表這個任務很可能要走 ASR 來源（見 transcriptNeedsASR）。
// LLM 步驟走 api 或 agent 時是 llm；opencc 與 publish 是本機的確定性處理。
// M3 的步驟依 ADR-0020 第 3 點：sponsorblock 是 download；chapters、note 走 api 或 agent 時是 llm；
// frames 的任務由 FRM 的切片依 tasks.kind 分類，這裡先給候選擷取的 frames 類別。
func resourceClass(stage, executor string, transcriptASR bool) string {
	switch {
	case stage == StageChapters || stage == StageNote:
		if executor == ExecutorAPI || executor == ExecutorAgent {
			return queue.ResourceLLM
		}
		return queue.ResourceLocal
	case stage == StageFrames:
		return queue.ResourceFrames
	case IsLLMStage(stage):
		if executor == ExecutorAPI || executor == ExecutorAgent {
			return queue.ResourceLLM
		}
		return queue.ResourceLocal
	case stage == StageNormalize || stage == StagePublish:
		return queue.ResourceLocal
	case stage == StageTranscript && transcriptASR:
		return queue.ResourceASR
	}
	return queue.ResourceDownload
}

// isASR 回報字幕來源是不是 ASR（語音轉錄）；目前只有 groq，新增 ASR 來源時加在這裡。
func isASR(sourceID string) bool { return sourceID == "groq" }

// transcriptNeedsASR 回報 transcript 任務是否歸為 asr 資源類別：
// language.fallback 的第一個來源是 ASR，而且平台字幕不符合 caption_priority（用不到平台字幕）。
// 選軌依平台：本機影音有自己的規則（見 selectTracks）。
// 這是建立任務時的推定（PIPE 規格待決問題）：實際走哪個來源要等 transcript 執行時才知道。
// tracks 與 originalLang 是剛取得的平台字幕軌與原文語言；從 transcript 重跑時不知道字幕軌，呼叫端不使用這個判斷。
func transcriptNeedsASR(platform string, lang Language, tracks []transcript.Track, originalLang string) bool {
	// 不支援這個平台的來源（例如本機影音的 Supadata）不會被執行，不算「第一個來源」。
	first := slices.IndexFunc(lang.Fallback, func(id string) bool { return id != supadata.ID || supadataSupports(platform) })
	if first < 0 || !isASR(lang.Fallback[first]) {
		return false
	}
	chosen, _ := selectTracks(platform, tracks, originalLang, lang)
	return chosen == nil
}

// ValidStage 回報 name 是不是 pipeline 的步驟（不含 plannedStages）。
func ValidStage(name string) bool { return slices.Contains(Stages, name) }

// nextStage 回傳 stage 的下一個步驟；until 之後的步驟不建立（PIPE-R1）。
// until 為空代表做到最後。沒有下一個步驟時 ok 為 false。
func nextStage(stage, until string) (next string, ok bool) {
	i := slices.Index(Stages, stage)
	if i < 0 || i+1 >= len(Stages) {
		return "", false
	}
	if until != "" && i >= slices.Index(Stages, until) {
		return "", false
	}
	return Stages[i+1], true
}

// cacheKey 計算步驟的快取鍵（plan 第 5.1 節）：
// (item, stage, impl_version, 上游 artifact hash, provider, model, prompt sha256, params sha256)。
// yt-dlp 等外部工具的版本只寫進來源紀錄，不放進快取鍵，否則每次更新工具都會讓快取失效。
type cacheKey struct {
	Item     string `json:"item"`
	Stage    string `json:"stage"`
	Impl     int    `json:"impl_version"`
	Upstream string `json:"upstream"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
	Params   string `json:"params"`
}

func (k cacheKey) String() string {
	b, _ := json.Marshal(k)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
