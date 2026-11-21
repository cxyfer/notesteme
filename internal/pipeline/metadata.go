package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// liveRecheckDelay 是直播或首播尚未開始時，再檢查有沒有 VOD 的間隔（SRC-R7）。
const liveRecheckDelay = 15 * time.Minute

// runMetadata 以 yt-dlp -J 取得標題、片長、語言、公開狀態、頻道與章節。
//
// 原文語言採用平台中繼資料（SRC-R11，lang_source 為 metadata）；平台沒有提供時留空，
// 由 transcript 步驟推斷（Bilibili 的 AI 字幕、本機字幕）或 ASR 回報的語言補上；
// TODO(SRC-R11)：文字語言偵測尚未實作。
//
// 本機影音：檔案不存在或不在根目錄內是 permanent（GRD-R7）；影音還沒有 content_sha256 時在這裡計算並寫入（DEL-R11），
// 並記下容器的種類（video 或 audio）。
// 直播中或首播尚未開始的影音，步驟為 waiting，直到有 VOD（SRC-R7）；這種等待不計入重試次數。
//
// metadata 的快取鍵只記錄、不用來略過：平台上的中繼資料會改變（標題、字幕軌），重新取得才是正確的。
func (p *Pipeline) runMetadata(ctx context.Context, it store.Item, lang Language) (result, error) {
	ad, err := p.adapterFor(it.Platform)
	if err != nil {
		return result{}, err
	}
	info, err := ad.probe(ctx, it)
	if err != nil {
		return result{}, err
	}
	fields := map[string]any{
		"description": info.Description,
		"chapters":    info.Chapters,
		"live_status": info.LiveStatus,
		"channel":     info.ChannelTitle,
	}
	if info.LocalStat != nil {
		// 本機檔案的 size 與 mtime：之後的探測用它判斷檔案有沒有被替換。
		fields["local"] = info.LocalStat
	}
	extra, err := json.Marshal(fields)
	if err != nil {
		return result{}, err
	}
	mu := &metaUpdate{item: store.ItemMeta{
		Title: info.Title, DurationS: info.DurationS, PublishedAt: info.PublishedAt,
		OriginalLang: info.Language, Availability: info.Availability, Metadata: extra,
		MediaKind: info.MediaKind, ContentSHA256: info.ComputedSHA256,
	}}
	if info.Language != "" {
		mu.item.LangSource = "metadata"
	}
	if info.ChannelID != "" {
		mu.channel = &store.Channel{Platform: it.Platform, ExternalID: info.ChannelID, Title: info.ChannelTitle, URL: info.ChannelURL}
	}
	res := result{
		meta: mu,
		stage: &store.StageResult{
			Executor: "local", ImplVersion: p.implVersion(StageMetadata),
			CacheKey: cacheKey{Item: it.ID, Stage: StageMetadata, Impl: p.implVersion(StageMetadata), Provider: cacheProvider(it.Platform), Params: paramsSHA(metadataKeyInput(it))}.String(),
		},
	}
	if info.Live {
		at := p.clock.Now().Add(liveRecheckDelay)
		res.task, res.notBefore, res.countAttempt = taskRequeue, &at, false
		res.state = store.StageState{
			Status: store.StatusWaiting, ErrorClass: "wait", NotBefore: &at,
			Message: "這部影片還在直播或尚未首播，等有重播檔後會自動處理。",
		}
		return res, nil
	}
	res.task, res.advance = taskDone, true
	// 趁著剛取得字幕軌，決定下游 transcript 任務的資源類別（PIPE 規格待決問題）。
	// Bilibili 沒有語言中繼資料：用 AI 字幕軌推斷的原文語言判斷，與 transcript 步驟選軌時的結論一致。
	origLang, _ := inferOriginalLang(it.Platform, info.Language, info.Tracks, lang)
	res.transcriptASR = transcriptNeedsASR(it.Platform, lang, info.Tracks, origLang)
	res.state = store.StageState{Status: store.StatusDone}
	return res, nil
}

// metadataKeyInput 是 metadata 快取鍵的輸入：網址；沒有網址的本機影音用它的位置。
func metadataKeyInput(it store.Item) string {
	if it.URL == "" {
		return it.ExternalID
	}
	return it.URL
}
