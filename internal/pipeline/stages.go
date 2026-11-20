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

// M1 的步驟，依執行順序（plan 第 5 節）。
const (
	StageMetadata   = "metadata"
	StageTranscript = "transcript"
	StageNormalize  = "normalize"
)

// Stages 是步驟的執行順序。
var Stages = []string{StageMetadata, StageTranscript, StageNormalize}

// 每個步驟宣告的版本（plan 第 5.1 節）：演算法的行為改變時遞增，舊的快取就不再命中（PIPE-R6）。
// 這是預設值；測試可以經 Deps.ImplVersions 或 Pipeline.SetImplVersion 覆寫，模擬步驟改版。
const (
	metadataImplVersion   = 1
	transcriptImplVersion = 1
	normalizeImplVersion  = 1
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
func resourceClass(stage string, transcriptASR bool) string {
	switch {
	case stage == StageNormalize:
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

// ValidStage 回報 name 是不是 M1 的步驟。
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
