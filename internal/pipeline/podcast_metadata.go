package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/store"
)

// runPodcastMetadata 取得 Podcast 單集的 metadata 並下載音檔（ING-R6、SRC-R15）：
// 抓取 feed（it.URL）、找到這一集、下載音檔到 <data>/media/<id>/（記錄 sha256 與 ffprobe 的實際長度），
// 然後把這一集的資訊與 transcript 清單寫成快照（podcastMeta），之後的步驟只讀它。
//
// 錯誤分類：被防護擋下的錯誤原樣傳出（classify 轉成 permanent 並說明原因）；feed 回 404／410、內容已不是 feed、
// feed 中已經沒有這一集、音檔 4xx 是 permanent；其餘（連線失敗、5xx、429）可以重試。
func (p *Pipeline) runPodcastMetadata(ctx context.Context, it store.Item, lang Language) (result, error) {
	if p.http == nil {
		return result{}, errPermanent("還不支援 podcast 的處理")
	}
	feedKey, guid, ok := podcast.SplitExternalID(it.ExternalID)
	if !ok {
		return result{}, podcastPermanent("這一集的識別資料不正確。", fmt.Errorf("external_id %q", it.ExternalID))
	}
	feed, err := podcast.Fetch(ctx, p.http, it.URL)
	if err != nil {
		return result{}, podcastFeedError(err)
	}
	ep, ok := feed.Find(guid)
	if !ok {
		return result{}, podcastPermanent(fmt.Sprintf("feed 中已經沒有這一集（%s）。", guid), nil)
	}
	if ep.Enclosure == nil || ep.Enclosure.URL == "" {
		return result{}, podcastPermanent(fmt.Sprintf("這一集沒有音檔（%s）。", guid), nil)
	}
	audio, err := p.downloadEpisodeAudio(ctx, it, *ep.Enclosure)
	if err != nil {
		return result{}, err
	}

	m := podcastMeta{
		Description: ep.Description,
		Channel:     feed.Title,
		Podcast: podcastInfo{
			GUID: guid, FeedURL: it.URL, FeedLanguage: feed.Language, Link: ep.Link, ItunesDurationS: ep.DurationS,
			Enclosure:   podcastEnclosure{URL: ep.Enclosure.URL, Type: ep.Enclosure.Type, Length: ep.Enclosure.Length},
			Transcripts: make([]podcastTranscript, 0, len(ep.Transcripts)),
		},
		Audio: audio,
	}
	for _, t := range ep.Transcripts {
		m.Podcast.Transcripts = append(m.Podcast.Transcripts, podcastTranscript{URL: t.URL, Type: t.Type, Language: t.Language, Rel: t.Rel})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return result{}, err
	}

	title := ep.Title
	if title == "" {
		title = guid
	}
	mu := &metaUpdate{item: store.ItemMeta{
		Title: title, PublishedAt: ep.PublishedAt, OriginalLang: podcast.Lang(feed.Language),
		Metadata: raw, MediaKind: audio.mediaKind, ContentSHA256: audio.SHA256,
	}}
	if audio.DurationS > 0 {
		d := int(math.Round(audio.DurationS))
		mu.item.DurationS = &d
	}
	if mu.item.OriginalLang != "" {
		mu.item.LangSource = "metadata"
	}
	mu.channel = &store.Channel{Platform: it.Platform, ExternalID: feedKey, Title: feed.Title, URL: it.URL}

	res := result{
		meta: mu,
		stage: &store.StageResult{
			Executor: "local", ImplVersion: p.implVersion(StageMetadata),
			CacheKey: cacheKey{
				Item: it.ID, Stage: StageMetadata, Impl: p.implVersion(StageMetadata), Provider: "podcast",
				Params: paramsSHA(map[string]string{"feed": it.URL, "guid": guid}),
			}.String(),
		},
		task: taskDone, advance: true,
		state: store.StageState{Status: store.StatusDone},
	}
	// 趁著剛取得字幕軌，決定下游 transcript 任務的資源類別（與 transcript 步驟選軌的結論一致）。
	tracks := podcastTracks(m.Podcast.Transcripts, feed.Language)
	origLang, _ := inferOriginalLang(it.Platform, mu.item.OriginalLang, tracks, lang)
	res.transcriptASR = transcriptNeedsASR(it.Platform, lang, tracks, origLang)
	return res, nil
}

// podcastFeedError 把重新抓取 feed 的錯誤分類（metadata 步驟）：防護錯誤原樣傳出；
// HTTP 404、410 與內容不再是 feed 是 permanent；其餘可以重試。
func podcastFeedError(err error) error {
	if _, ok := netguard.PolicyMessage(err); ok {
		return err
	}
	var he *podcast.HTTPError
	switch {
	case errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusGone):
		return podcastPermanent(fmt.Sprintf("這個 feed 已經不存在（HTTP %d）。", he.StatusCode), err)
	case errors.Is(err, podcast.ErrNotFeed):
		return podcastPermanent("這個網址現在不是 Podcast feed。", err)
	}
	// 沒有分類的錯誤視為 retryable（classify）。
	return fmt.Errorf("抓取 feed：%w", err)
}
