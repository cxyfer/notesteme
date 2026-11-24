package pipeline

import (
	"context"
	"net/url"
	"strings"
	"time"

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

// podcastSelection 是 Podcast 辨識與展開的結果：抓到的 feed，以及請求挑出的各集。
type podcastSelection struct {
	feed      *podcast.Feed
	canonical string // 正規化的 feed 網址
	slug      string // feedKey 的候選
	picked    []podcast.Episode
}

// build 回傳 insertTargets 用的 build：交易內取得 feedKey（必要時建立頻道），再組出各集的 Target。
func (s *podcastSelection) build(ctx context.Context) func(*store.Repo) ([]source.Target, string, error) {
	return func(r *store.Repo) ([]source.Target, string, error) {
		key, err := r.PodcastFeedKey(ctx, s.canonical, s.slug, s.feed.Title)
		if err != nil {
			return nil, "", err
		}
		ch, err := r.ChannelByKey(ctx, source.PlatformPodcast, key)
		if err != nil {
			return nil, "", err
		}
		targets := make([]source.Target, 0, len(s.picked))
		for _, ep := range s.picked {
			targets = append(targets, source.Target{
				Platform: source.PlatformPodcast, Kind: source.KindFeed, ExternalID: podcast.ExternalID(key, ep.GUID), URL: s.canonical,
			})
		}
		return targets, ch.ID, nil
	}
}

// expandPodcast 辨識 Podcast feed 並挑出要新增的單集（ING-R6），不寫入資料庫：
// 每個指定的集是一部影音，識別為 (podcast, "<feedKey>/<guid>", 0)，網址是正規化的 feed 網址。
//
// 驗證依序：until、parts（422）、網址（http(s)、有主機、不是 YouTube／Bilibili，否則無法辨識）、
// 處理方案與單次覆寫（422）；然後抓取 feed（失敗對應同 Resolve）、episodes 不可為空、每集要在 feed 中而且有音檔；
// 最後對每個不同的音檔主機預檢（不連線）：被防護擋下回 *PolicyError，DNS 失敗只記 log，連線時再檢查一次（GRD-R2）。
// 新增時，交易內取得 feedKey（必要時建立頻道），再建立各集；已存在的集不重新建立（ING-R2，見 podcastSelection.build）。
func (p *Pipeline) expandPodcast(ctx context.Context, req IngestRequest) (ingestExpansion, error) {
	if req.Until != "" && !ValidStage(req.Until) {
		return ingestExpansion{}, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	if len(req.Parts) > 0 {
		return ingestExpansion{}, invalidf("Podcast 沒有分P 可以選擇。")
	}
	feedURL, err := podcast.CanonicalURL(req.URL)
	if err != nil || platformHost(feedURL.Hostname()) {
		return ingestExpansion{}, ErrUnrecognizedURL
	}
	plan, err := p.planIngest(ctx, req)
	if err != nil {
		return ingestExpansion{}, err
	}
	feed, err := p.fetchFeed(ctx, feedURL.String())
	if err != nil {
		return ingestExpansion{}, p.feedError(err)
	}
	if len(req.Episodes) == 0 {
		return ingestExpansion{}, invalidf("這是 Podcast feed，請在 episodes 指定要新增的集。")
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
			return ingestExpansion{}, invalidf("feed 中找不到這一集：%s。", guid)
		}
		if ep.Enclosure == nil || ep.Enclosure.URL == "" {
			return ingestExpansion{}, invalidf("這一集沒有音檔：%s。", guid)
		}
		seen[guid] = true
		picked = append(picked, *ep)
	}
	if err := p.precheckEnclosures(ctx, picked); err != nil {
		return ingestExpansion{}, err
	}
	return ingestExpansion{plan: plan, podcast: &podcastSelection{
		feed: feed, canonical: feedURL.String(), slug: podcast.FeedSlug(feedURL), picked: picked,
	}}, nil
}

// precheckTimeout 是 precheckEnclosures 整體的逾時；變數是為了讓測試縮短。
var precheckTimeout = 10 * time.Second

// precheckEnclosures 對每個不同 scheme 與主機的音檔網址預檢一次。
// 只有全部的位址都被防護擋下時才回 *PolicyError（ING-R1、ADR-0017）；
// 有任何一個位址通過、沒有結果（DNS 解析失敗、網址不完整等，只記 log），或因逾時沒有檢查到，就不擋，留給之後下載時的連線去回報。
func (p *Pipeline) precheckEnclosures(ctx context.Context, eps []podcast.Episode) error {
	// 整體逾時：許多主機的 DNS 都很慢時，預檢不能拖住新增；逾時之後略過剩下的，連線時仍會檢查（GRD-R2）。
	pctx, cancel := context.WithTimeout(ctx, precheckTimeout)
	defer cancel()
	checked := map[string]bool{}
	var blocked *PolicyError
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
		if pctx.Err() != nil {
			// 還有沒檢查到的位址：不能斷定全部被擋。
			p.log.DebugContext(ctx, "音檔位址預檢逾時，略過剩下的", "host", host)
			return ctx.Err()
		}
		checked[key] = true
		err := p.checkURL(pctx, raw)
		if err == nil {
			return nil
		}
		if pe := asPolicyError(err); pe != nil {
			p.log.WarnContext(ctx, "新增 Podcast 單集時音檔位址被防護擋下", "host", host, "error", err.Error())
			if blocked == nil {
				blocked = pe
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.log.DebugContext(ctx, "音檔位址預檢沒有結果，略過", "host", host, "error", err.Error())
		return nil
	}
	if blocked != nil {
		return blocked
	}
	return nil
}
