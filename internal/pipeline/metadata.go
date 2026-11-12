package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// liveRecheckDelay 是直播或首播尚未開始時，再檢查有沒有 VOD 的間隔（SRC-R7）。
const liveRecheckDelay = 15 * time.Minute

// runMetadata 以 yt-dlp -J 取得標題、片長、語言、公開狀態、頻道與章節。
//
// 原文語言採用平台中繼資料（SRC-R11，lang_source 為 metadata）；平台沒有提供時留空，
// TODO(SRC-R11)：之後由 ASR 回報的語言或文字語言偵測補上。
// 直播中或首播尚未開始的影音，步驟為 waiting，直到有 VOD（SRC-R7）；這種等待不計入重試次數。
//
// metadata 的快取鍵只記錄、不用來略過：平台上的中繼資料會改變（標題、字幕軌），重新取得才是正確的。
func (p *Pipeline) runMetadata(ctx context.Context, it store.Item) (result, error) {
	info, err := p.yt.Probe(ctx, it.ExternalID)
	if err != nil {
		return result{}, err
	}
	extra, err := json.Marshal(map[string]any{
		"description": info.Description,
		"chapters":    info.Chapters,
		"live_status": info.LiveStatus,
		"channel":     info.Channel,
	})
	if err != nil {
		return result{}, err
	}
	mu := &metaUpdate{item: store.ItemMeta{
		Title: info.Title, DurationS: info.DurationS, PublishedAt: info.PublishedAt,
		OriginalLang: info.Language, Availability: info.Availability, Metadata: extra,
	}}
	if info.Language != "" {
		mu.item.LangSource = "metadata"
	}
	if info.ChannelID != "" {
		mu.channel = &store.Channel{
			Platform: source.PlatformYouTube, ExternalID: info.ChannelID, Title: info.Channel,
			URL: "https://www.youtube.com/channel/" + info.ChannelID,
		}
	}
	res := result{
		meta: mu,
		stage: &store.StageResult{
			Executor: "local", ImplVersion: p.implVersion(StageMetadata),
			CacheKey: cacheKey{Item: it.ID, Stage: StageMetadata, Impl: p.implVersion(StageMetadata), Provider: "yt-dlp", Params: paramsSHA(it.URL)}.String(),
		},
	}
	if info.Live() {
		at := p.clock.Now().Add(liveRecheckDelay)
		res.task, res.notBefore, res.countAttempt = taskRequeue, &at, false
		res.state = store.StageState{
			Status: store.StatusWaiting, ErrorClass: "wait", NotBefore: &at,
			Message: "這部影片還在直播或尚未首播，等有重播檔後會自動處理。",
		}
		return res, nil
	}
	res.task, res.advance = taskDone, true
	res.state = store.StageState{Status: store.StatusDone}
	return res, nil
}
