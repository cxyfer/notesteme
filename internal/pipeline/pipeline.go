// Package pipeline 建立影音、決定各步驟怎麼執行，並把步驟的結果寫回資料庫。
//
// M1 只有確定性的步驟（plan 第 5 節）：metadata → transcript → normalize。
// 任務依 DAG 建立：上游完成後才建立下游任務；單次覆寫的 until 之後的步驟不建立（PIPE-R1）。
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/vault"
)

// YouTube 是步驟用到的 YouTube 功能（*youtube.Client 實作它）；測試可以替換。
type YouTube interface {
	Probe(ctx context.Context, videoID string) (*youtube.Info, error)
	FetchCaptions(ctx context.Context, videoID string, t youtube.Track, dir string) ([]byte, error)
	// Playlist 展開播放清單（ING-R4）。
	Playlist(ctx context.Context, playlistID string) ([]youtube.PlaylistEntry, error)
	Version(ctx context.Context) (string, error)
	// DownloadAudio 下載影片的最佳音訊到 dir，回傳檔案路徑（ASR 來源使用）。
	DownloadAudio(ctx context.Context, videoID, dir string) (string, error)
}

// Bilibili 是步驟用到的 Bilibili 功能（*bilibili.Client 實作它）；測試可以替換。
// part 從 1 起算。
type Bilibili interface {
	// Parts 展開影片的分P（ING-R3）。
	Parts(ctx context.Context, bvid string) ([]bilibili.PartEntry, error)
	Probe(ctx context.Context, bvid string, part int) (*bilibili.Info, error)
	// FetchCaptions 下載字幕軌；key 是 Probe 回報的原始鍵。
	FetchCaptions(ctx context.Context, bvid string, part int, key, dir string) ([]byte, error)
	Version(ctx context.Context) (string, error)
}

// Supadata 是 Supadata 字幕來源（*supadata.Client 實作它）；測試可以替換。
type Supadata interface {
	Fetch(ctx context.Context, videoURL, mode string) (*supadata.Result, error)
}

// Groq 是 Groq 的語音轉錄來源（*groq.Client 實作它）；測試可以替換。
type Groq interface {
	Transcribe(ctx context.Context, audioPath, langHint string, opts ...groq.Option) (*groq.Result, error)
	// Configured 回報有沒有 API key；沒有時不下載音訊。讀取 key 失敗時回傳錯誤。
	Configured(ctx context.Context) (bool, error)
	// Endpoint 回傳實際使用的 base_url 與 model，它們影響結果，屬於快取鍵。
	Endpoint() (baseURL, model string)
	// MaxFileBytes 是單一音檔的大小上限；超過就要切塊（SRC-R12）。
	MaxFileBytes() int64
}

// Deps 是 Pipeline 的依賴。
type Deps struct {
	Store *store.Store
	Clock clock.Clock
	// DataDir 是資料目錄；產出寫在其下的 artifacts/，暫存檔在 tmp/。
	DataDir string
	Log     *slog.Logger
	YouTube YouTube
	// Bilibili 是 Bilibili 的來源；nil 代表沒有設定，新增 Bilibili 影片時回 501。
	Bilibili Bilibili
	// Supadata 是 Supadata 字幕來源（SRC-R3）；nil 代表沒有設定 API key，fallback 列出它時記為 not_configured。
	Supadata Supadata
	// Groq 是 Groq 語音轉錄來源（SRC-R3）；nil 代表來源沒有註冊，fallback 列出它時記為 not_configured。
	// 沒有 API key 時，client 仍然存在，Transcribe 會回報 ErrNoKey（同樣記為 not_configured）。
	Groq Groq
	// HTTP 是對外 HTTP 的 client（netguard 的 Transport，ADR-0014）；nil 時辨識網址不會抓取任何內容。
	HTTP *http.Client
	// CheckURL 在不連線的情況下預檢網址的目的地會不會被封鎖（netguard.CheckURL）；新增 Podcast 單集時用來提早回報（GRD-R2）。
	// 防護造成的錯誤（netguard.PolicyMessage 認得的）會讓新增失敗；解析失敗等其他錯誤只記 log。nil 代表略過預檢。
	CheckURL func(ctx context.Context, rawURL string) error
	// Media 是 ffprobe 與 ffmpeg 的包裝（ING-05）；nil 代表不支援本機影音的探測與前處理。
	Media *media.Tool
	// Files 是程式內讀取本機檔案的守衛（serve 為 inbox、media_roots 與 <data>/uploads；standalone 不受限制）；
	// nil 代表不支援讀取本機檔案。
	Files *pathguard.Guard
	// Wake 在建立了新任務時呼叫，讓 worker 醒來；可以是 nil。
	Wake func()
	// Version 是 notesteme 的版本，寫進來源紀錄。
	Version string
	// TrashRetention 是垃圾桶保留的時間，垃圾桶列表用它算出 purge_at（DEL-R4）；0 代表預設的 30 天。
	// 由 DEL-B2 接上設定檔的 library.trash_retention_days。
	TrashRetention time.Duration
	// ImplVersions 覆寫步驟的 impl_version（PIPE-R6），key 是步驟名稱；只給測試使用，nil 代表全用預設值。
	ImplVersions map[string]int
	// Providers 回答 LLM provider 存不存在（PRF-R4）；nil 代表不檢查 provider 是否存在。
	Providers ProviderCatalog
	// Outputs 回答設定檔的 vault 與 hook 存不存在（VAULT-R1、R10）；nil 代表不檢查。
	Outputs OutputCatalog
	// Vaults、Hooks 是設定檔定義的 vault 與 hook（VAULT-R1、R10），以 id 為鍵；nil 代表沒有定義。
	Vaults map[string]vault.Def
	Hooks  map[string]vault.Hook
	// Prompts 解析 prompt 引用（PRM、PRF-R4）；nil 代表不檢查 prompt 是否存在，也不記錄 prompt 版本。
	Prompts PromptResolver
	// LLM 是 LLM provider 的 client、API key 與限流（LLM 規格）；nil 代表 api executor 的 LLM 步驟以 permanent 失敗。
	LLM *llm.Registry
	// SponsorBlock 查詢 SponsorBlock 的片段（SPB-R2）；nil 代表沒有設定，sponsorblock 步驟以 permanent 失敗。
	SponsorBlock SponsorBlockLookup
	// SponsorBlockBaseURL 是設定檔的 sponsorblock.base_url，寫進來源紀錄（SPB-R6）。
	SponsorBlockBaseURL string
	// Notify 在步驟狀態寫回的同一個交易中寫入通知事件（NOTIFY-R3）；nil 代表不通知。
	Notify *notify.Outbox
}

// Pipeline 是影音處理的入口：API 呼叫 Ingest 與查詢方法，worker 呼叫 Handle。
type Pipeline struct {
	st      *store.Store
	clock   clock.Clock
	dataDir string
	log     *slog.Logger
	yt      YouTube
	bili    Bilibili
	// fetchers 是可以執行的 fallback 來源（SRC-R3），依來源 ID。
	fetchers map[string]fallbackFetcher
	// groq 只給估價用：查有沒有 API key 與使用的 model（見 Estimate）；nil 代表來源沒有註冊。
	groq    Groq
	http    *http.Client
	checkFn func(ctx context.Context, rawURL string) error
	media   *media.Tool
	files   *pathguard.Guard
	wake    func()
	version string
	// trashRetention 是垃圾桶保留的時間（已套用預設值）。
	trashRetention time.Duration
	// tasks 結束永久刪除時殘留的任務（DEL-R6）；可以是 nil，見 SetTaskCanceller。
	tasks TaskCanceller
	// beforeRerunTx 只給測試使用：Rerun 讀取影音之後、開啟寫入交易之前呼叫，用來重現與垃圾桶的競態。
	beforeRerunTx func()

	// impl 是 impl_version 的覆寫（PIPE-R6）；worker 與測試介面並行存取，用 implMu 保護。
	implMu sync.RWMutex
	impl   map[string]int

	// trashLocks 讓同一部影音的移到垃圾桶（含 commit 之後的善後）與還原互斥（DEL-R2、R5）。
	trashLocks itemLocks

	// uploadHolds 讓備份期間的永久刪除延後刪除上傳原始檔（OPS-R1），見 HoldUploads。
	uploadHolds uploadHolds

	// estimates 限制估價同時進行的 Probe 數（跨請求共用）。
	estimates estimateLimiter

	// providers、prompts 是驗證與 Compose 查詢外部資源用的（見 Deps）；可以是 nil。
	providers ProviderCatalog
	outputs   OutputCatalog
	prompts   PromptResolver
	// vaults、hooks 見 Deps.Vaults、Deps.Hooks。
	vaults map[string]vault.Def
	hooks  map[string]vault.Hook
	llm    *llm.Registry
	// sponsor、sponsorBase 見 Deps.SponsorBlock、Deps.SponsorBlockBaseURL。
	sponsor     SponsorBlockLookup
	sponsorBase string
	// agentLimit 回傳資源類別的並行上限，Agent 領取時用（AGT-R2）；nil 時用 queue.DefaultLimit。
	agentLimit func(class string) int
	// notify 見 Deps.Notify。
	notify *notify.Outbox
}

// New 建立 Pipeline。
func New(d Deps) *Pipeline {
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	wake := d.Wake
	if wake == nil {
		wake = func() {}
	}
	var impl map[string]int
	if len(d.ImplVersions) > 0 {
		impl = make(map[string]int, len(d.ImplVersions))
		for k, v := range d.ImplVersions {
			impl[k] = v
		}
	}
	retention := d.TrashRetention
	if retention <= 0 {
		retention = DefaultTrashRetention
	}
	return &Pipeline{trashRetention: retention, impl: impl, st: d.Store, clock: d.Clock, dataDir: d.DataDir, log: log, yt: d.YouTube, bili: d.Bilibili, fetchers: newFetchers(d), groq: d.Groq, http: d.HTTP, checkFn: d.CheckURL, media: d.Media, files: d.Files, wake: wake, version: d.Version, providers: d.Providers, outputs: d.Outputs, prompts: d.Prompts, llm: d.LLM, sponsor: d.SponsorBlock, sponsorBase: d.SponsorBlockBaseURL, notify: d.Notify, vaults: d.Vaults, hooks: d.Hooks}
}

// settingKey 是全域設定區段在 settings 表中的 key。
func settingKey(section string) string { return "settings." + section }

// globalSections 是可以用 PUT /api/settings/{section} 設定的全域設定區段（D16）。body 都是物件；
// outputs、glossary 是陣列，不開放全域設定。
var globalSections = []string{"language", "timing", "llm", "agent", "subtitle_rules", "stages", "supadata"}

// globalConfig 讀出全域設定（資料庫），形狀與 Effective 相同：{"language": {...}}。沒有設定過的區段不在其中。
func (p *Pipeline) globalConfig(ctx context.Context) (map[string]any, error) {
	return globalConfigIn(ctx, p.st.Read())
}

// globalConfigIn 在給定的 Repo（唯讀或交易）上讀出全域設定；prompt 刪除前的檢查要在同一個交易內讀（P7）。
func globalConfigIn(ctx context.Context, r *store.Repo) (map[string]any, error) {
	out := map[string]any{}
	for _, s := range globalSections {
		raw, ok, err := r.Setting(ctx, settingKey(s))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out[s] = v
	}
	return out, nil
}

// GetSettings 回傳全域設定區段的生效值（內建預設加上使用者設定）。
// 沒有內建預設的區段（timing、llm、agent、subtitle_rules）回傳存下來的值，沒存過就是空物件；
// 它們的預設值由使用它們的切片在 accessor 中補上。其他區段回傳 *NotImplementedError。
func (p *Pipeline) GetSettings(ctx context.Context, section string) (map[string]any, error) {
	if !slices.Contains(globalSections, section) {
		return nil, &NotImplementedError{What: "設定區段 " + section}
	}
	global, err := p.globalConfig(ctx)
	if err != nil {
		return nil, err
	}
	eff, err := BuildEffective(global)
	if err != nil {
		return nil, err
	}
	if v, ok := eff[section].(map[string]any); ok {
		return v, nil
	}
	return map[string]any{}, nil
}

// SetSettings 以 body 取代全域設定的某個區段。先依 schema 驗證（錯誤依固定順序列在 errors，路徑帶區段名稱，
// 例如 timing.split_timing），再與內建預設合併做語意檢查，通過才存入。
func (p *Pipeline) SetSettings(ctx context.Context, section string, body map[string]any) (map[string]any, error) {
	if !slices.Contains(globalSections, section) {
		return nil, &NotImplementedError{What: "設定區段 " + section}
	}
	if fs := ValidateLayer(ctx, map[string]any{section: body}, KindGlobal, p.validateDeps()); len(fs) > 0 {
		return nil, invalidFields(fs)
	}
	if _, err := BuildEffective(map[string]any{section: body}); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if err := p.st.SetSetting(ctx, settingKey(section), string(raw)); err != nil {
		return nil, err
	}
	return p.GetSettings(ctx, section)
}
