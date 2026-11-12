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

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// YouTube 是步驟用到的 YouTube 功能（*youtube.Client 實作它）；測試可以替換。
type YouTube interface {
	Probe(ctx context.Context, videoID string) (*youtube.Info, error)
	FetchCaptions(ctx context.Context, videoID string, t youtube.Track, dir string) ([]byte, error)
	Version(ctx context.Context) (string, error)
}

// Supadata 是 Supadata 字幕來源（*supadata.Client 實作它）；測試可以替換。
type Supadata interface {
	Fetch(ctx context.Context, videoURL, mode string) (*supadata.Result, error)
}

// Deps 是 Pipeline 的依賴。
type Deps struct {
	Store *store.Store
	Clock clock.Clock
	// DataDir 是資料目錄；產出寫在其下的 artifacts/，暫存檔在 tmp/。
	DataDir string
	Log     *slog.Logger
	YouTube YouTube
	// Supadata 是 Supadata 字幕來源（SRC-R3）；nil 代表沒有設定 API key，fallback 列出它時記為 not_configured。
	Supadata Supadata
	// HTTP 是對外 HTTP 的 client（netguard 的 Transport，ADR-0014）；nil 時辨識網址不會抓取任何內容。
	HTTP *http.Client
	// Wake 在建立了新任務時呼叫，讓 worker 醒來；可以是 nil。
	Wake func()
	// Version 是 notesteme 的版本，寫進來源紀錄。
	Version string
}

// Pipeline 是影音處理的入口：API 呼叫 Ingest 與查詢方法，worker 呼叫 Handle。
type Pipeline struct {
	st      *store.Store
	clock   clock.Clock
	dataDir string
	log     *slog.Logger
	yt      YouTube
	// fetchers 是可以執行的 fallback 來源（SRC-R3），依來源 ID。
	fetchers map[string]fallbackFetcher
	http     *http.Client
	wake     func()
	version  string
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
	return &Pipeline{st: d.Store, clock: d.Clock, dataDir: d.DataDir, log: log, yt: d.YouTube, fetchers: newFetchers(d), http: d.HTTP, wake: wake, version: d.Version}
}

// settingKey 是全域設定區段在 settings 表中的 key。
func settingKey(section string) string { return "settings." + section }

// globalSections 是目前支援的全域設定區段。
var globalSections = []string{"language"}

// globalConfig 讀出全域設定（資料庫），形狀與 Effective 相同：{"language": {...}}。沒有設定過的區段不在其中。
func (p *Pipeline) globalConfig(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for _, s := range globalSections {
		raw, ok, err := p.st.GetSetting(ctx, settingKey(s))
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
// 目前只有 language；其他區段屬於之後的切片。
func (p *Pipeline) GetSettings(ctx context.Context, section string) (map[string]any, error) {
	if section != "language" {
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
	return eff["language"].(map[string]any), nil
}

// SetSettings 以 body 取代全域設定的某個區段。body 先與內建預設合併並驗證，通過才存入。
func (p *Pipeline) SetSettings(ctx context.Context, section string, body map[string]any) (map[string]any, error) {
	if section != "language" {
		return nil, &NotImplementedError{What: "設定區段 " + section}
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
