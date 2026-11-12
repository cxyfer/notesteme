package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/cxyfer/notesteme/internal/queue"
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
func resourceClass(stage string) string {
	if stage == StageNormalize {
		return queue.ResourceLocal
	}
	return queue.ResourceDownload
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
