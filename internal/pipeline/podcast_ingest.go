package pipeline

import (
	"context"
	"net/url"
	"strings"

	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// checkURL 預檢網址的目的地（不連線）；沒有設定 Deps.CheckURL 時略過。
func (p *Pipeline) checkURL(ctx context.Context, rawURL string) error {
	if p.checkFn == nil {
		return nil
	}
	return p.checkFn(ctx, rawURL)
}

// ingestPodcast 新增 Podcast feed 的單集（ING-R6）：每個指定的集是一部影音，
// 識別為 (podcast, "<feedKey>/<guid>", 0)，網址是正規化的 feed 網址。
//
// 驗證依序：until、處理方案（501）、parts（422）、網址（http(s)、有主機、不是 YouTube／Bilibili，否則無法辨識）、
// 單次覆寫；然後抓取 feed（失敗對應同 Resolve）、episodes 不可為空、每集要在 feed 中而且有音檔；
// 最後對每個不同的音檔主機預檢（不連線）：被防護擋下回 *PolicyError，DNS 失敗只記 log，連線時再檢查一次（GRD-R2）。
// 交易內取得 feedKey（必要時建立頻道），再建立各集；已存在的集不重新建立（ING-R2）。
func (p *Pipeline) ingestPodcast(ctx context.Context, req IngestRequest) ([]IngestResult, error) {
	if req.Until != "" && !ValidStage(req.Until) {
		return nil, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	if req.Profile != "" {
		// TODO(PRF)：同 Ingest。
		return nil, &NotImplementedError{What: "處理方案"}
	}
	if len(req.Parts) > 0 {
		return nil, invalidf("Podcast 沒有分P 可以選擇。")
	}
	feedURL, err := podcast.CanonicalURL(req.URL)
	if err != nil || platformHost(feedURL.Hostname()) {
		return nil, ErrUnrecognizedURL
	}
	plan, err := p.planIngest(ctx, req)
	if err != nil {
		return nil, err
	}
	feed, err := p.fetchFeed(ctx, feedURL.String())
	if err != nil {
		return nil, p.feedError(err)
	}
	if len(req.Episodes) == 0 {
		return nil, invalidf("這是 Podcast feed，請在 episodes 指定要新增的集。")
	}

	// 依請求的順序挑出各集（重複的 guid 只算一次）。
	var picked []podcast.Episode
	seen := map[string]bool{}
	for _, raw := range req.Episodes {
		guid := strings.TrimSpace(raw)
		if seen[guid] {
			continue
		}
		ep, ok := feed.Find(guid)
		if !ok {
			return nil, invalidf("feed 中找不到這一集：%s。", guid)
		}
		if ep.Enclosure == nil || ep.Enclosure.URL == "" {
			return nil, invalidf("這一集沒有音檔：%s。", guid)
		}
		seen[guid] = true
		picked = append(picked, *ep)
	}
	if err := p.precheckEnclosures(ctx, picked); err != nil {
		return nil, err
	}

	canonical := feedURL.String()
	slug := podcast.FeedSlug(feedURL)
	return p.insertTargets(ctx, req, plan, func(r *store.Repo) ([]source.Target, string, error) {
		key, err := r.PodcastFeedKey(ctx, canonical, slug, feed.Title)
		if err != nil {
			return nil, "", err
		}
		ch, err := r.ChannelByKey(ctx, source.PlatformPodcast, key)
		if err != nil {
			return nil, "", err
		}
		targets := make([]source.Target, 0, len(picked))
		for _, ep := range picked {
			targets = append(targets, source.Target{
				Platform: source.PlatformPodcast, Kind: source.KindFeed, ExternalID: podcast.ExternalID(key, ep.GUID), URL: canonical,
			})
		}
		return targets, ch.ID, nil
	})
}

// precheckEnclosures 對每個不同 scheme 與主機的音檔網址預檢一次：被防護擋下回 *PolicyError；
// 其他錯誤（DNS 解析失敗、網址不完整等）只記 log，讓之後下載時的連線去回報。
func (p *Pipeline) precheckEnclosures(ctx context.Context, eps []podcast.Episode) error {
	checked := map[string]bool{}
	for _, ep := range eps {
		raw := ep.Enclosure.URL
		host, key := raw, raw
		if u, err := url.Parse(raw); err == nil {
			host = strings.ToLower(u.Hostname())
			// 去重鍵含 scheme：同一個主機的 ftp 網址不能因為 https 已檢查過就略過。
			key = strings.ToLower(u.Scheme) + "://" + host
		}
		if checked[key] {
			continue
		}
		checked[key] = true
		err := p.checkURL(ctx, raw)
		if err == nil {
			continue
		}
		if pe := asPolicyError(err); pe != nil {
			p.log.Warn("新增 Podcast 單集時音檔位址被防護擋下", "host", host, "error", err.Error())
			return pe
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.log.Debug("音檔位址預檢沒有結果，略過", "host", host, "error", err.Error())
	}
	return nil
}
