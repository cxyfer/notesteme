package pipeline

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// estimateConcurrency 是同時對平台 Probe 的上限（播放清單可能有很多部）。
const estimateConcurrency = 4

// sourcePodcastTranscript 是 Podcast feed 附的字幕（steps.md 第 3 節）。
const sourcePodcastTranscript = "podcast-transcript"

// EstimateItem 是估價結果中的一部影音。金額欄位為 nil 代表「不知道」，與 0（確定不用錢）不同。
type EstimateItem struct {
	Ref   string
	Title string
	// DurationS 為 nil 代表平台沒有提供長度。
	DurationS *float64
	// ExpectedSource 是預計使用的字幕來源；nil 代表沒有可用的來源。
	ExpectedSource *string
	// ASRUSD 是語音轉錄費用：用平台字幕時為 0；長度或單價未知時為 nil。
	ASRUSD *float64
	// SourceUSD 是其他付費字幕來源（Supadata）的費用；單價未知（credit 計價）或沒有用到時為 nil。
	SourceUSD *float64
	// LLMUSD 在 M2 實作 LLM 之前一律為 nil。
	LLMUSD *float64
	// TotalUSD 是已知金額的和；沒有任何已知金額時為 nil。
	TotalUSD         *float64
	RequiresCookie   bool
	AlreadyInLibrary bool
	Notes            []string
	// unknown 為 true 代表有金額不知道（長度、單價未知，或 Probe 失敗）；LLM 尚未實作不算。
	unknown bool
}

// Estimate 是估價的結果。
type Estimate struct {
	Items []EstimateItem
	// TotalUSD 是各部影音已知金額的和。
	TotalUSD   float64
	Currency   string
	PricesAsOf string
	Notes      []string
}

// Estimate 估算新增這個請求會花多少錢，不建立影音，也不寫入資料庫（ING-R8）。
//
// 辨識與展開與新增相同（expandRequest）；until 忽略。每部影音以平台的 Probe 取得字幕軌與長度，
// 依生效的 language 選軌（與 transcript 步驟相同）：選到平台字幕就不計 ASR 費用；沒選到時，
// 依 fallback 順序找第一個有設定、支援這個平台的來源。
// 錯誤同 Ingest；只有一部影音而 Probe 失敗時回傳錯誤，多部時只在該項的 notes 說明。
func (p *Pipeline) Estimate(ctx context.Context, req IngestRequest) (*Estimate, error) {
	req.Until = ""
	x, err := p.expandRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	lang, err := x.plan.eff.Language()
	if err != nil {
		return nil, err
	}
	e := &estimator{p: p, lang: lang}
	var items []EstimateItem
	if x.podcast != nil {
		items = e.podcastItems(ctx, x.podcast)
	} else if items, err = e.probeItems(ctx, x.targets); err != nil {
		return nil, err
	}

	out := &Estimate{Items: items, Currency: "USD", PricesAsOf: cost.PricesAsOf, Notes: []string{}}
	unknown := false
	for _, it := range out.Items {
		if it.TotalUSD != nil {
			out.TotalUSD += *it.TotalUSD
		}
		if it.unknown {
			unknown = true
		}
	}
	out.TotalUSD = roundUSD(out.TotalUSD)
	if unknown {
		out.Notes = append(out.Notes, "部分金額未知")
	}
	return out, nil
}

// estimator 是一次估價用到的狀態：生效的語言設定，以及（延遲查詢一次的）Groq 狀態。
type estimator struct {
	p    *Pipeline
	lang Language

	groqOnce  sync.Once
	groqReady bool
	groqModel string
	groqNote  string
}

// groqState 回報 Groq 有沒有設定 API key，以及使用的 model；整次估價只查一次。
func (e *estimator) groqState(ctx context.Context) (ready bool, model, note string) {
	e.groqOnce.Do(func() {
		g := e.p.groq
		if g == nil {
			e.groqNote = "已略過 groq：來源沒有註冊。"
			return
		}
		_, e.groqModel = g.Endpoint()
		ok, err := g.Configured(ctx)
		switch {
		case err != nil:
			e.p.log.Debug("估價時讀取 Groq API key 失敗，視為沒有設定", "error", err.Error())
			e.groqNote = "已略過 groq：無法確認 API key。"
		case !ok:
			e.groqNote = "已略過 groq：沒有設定 API key。"
		default:
			e.groqReady = true
		}
	})
	return e.groqReady, e.groqModel, e.groqNote
}

// probeItems 對每個 Target 做 Probe 並估價；結果的順序與 targets 相同。
func (e *estimator) probeItems(ctx context.Context, targets []source.Target) ([]EstimateItem, error) {
	items := make([]EstimateItem, len(targets))
	errs := make([]error, len(targets))
	sem := make(chan struct{}, estimateConcurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			items[i], errs[i] = e.probeItem(ctx, t)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, err := range errs {
		if err == nil {
			continue
		}
		if len(targets) == 1 {
			return nil, err
		}
		// 多部影音：一部失敗不讓整份估價失敗，在該項說明。
		items[i].unknown = true
		items[i].Notes = append(items[i].Notes, err.Error())
	}
	return items, nil
}

// probeItem 估算一部 YouTube 或 Bilibili 影音。Probe 失敗時回傳已填好 ref 的空白項目與使用者看得到的錯誤。
func (e *estimator) probeItem(ctx context.Context, t source.Target) (EstimateItem, error) {
	it := EstimateItem{Ref: source.Ref(t.Platform, t.ExternalID, t.Part), Notes: []string{}}
	it.AlreadyInLibrary = e.inLibrary(ctx, t)
	ad, err := e.p.adapterFor(t.Platform)
	if err != nil {
		return it, err
	}
	pctx, cancel := context.WithTimeout(ctx, resolveProbeTimeout)
	defer cancel()
	item := store.Item{Platform: t.Platform, ExternalID: t.ExternalID, Part: t.Part, URL: t.URL}
	info, err := ad.probe(pctx, item)
	if err != nil {
		if ctx.Err() != nil {
			return it, ctx.Err()
		}
		return it, probeFailure(err)
	}
	it.Title = info.Title
	if info.DurationS != nil {
		d := float64(*info.DurationS)
		it.DurationS = &d
	}
	if t.Platform == source.PlatformYouTube && slices.Contains(cookieAvailabilities, info.Availability) {
		it.RequiresCookie = true
		it.Notes = append(it.Notes, "這部影音需要 cookie 才能取得。")
	}

	originalLang, _ := inferOriginalLang(t.Platform, info.Language, info.Tracks, e.lang)
	chosen, decisions := selectTracks(t.Platform, info.Tracks, originalLang, e.lang)
	if chosen != nil {
		src := transcript.PlatformSource(t.Platform, chosen.Kind)
		e.platformCaptions(&it, src)
		return it, nil
	}

	// Bilibili 的 AI 字幕需要登入才看得到（SRC-R8）：步驟會等 cookie，上傳之後才會用 bilibili-ai。
	if t.Platform == source.PlatformBilibili && info.NeedsLogin &&
		slices.ContainsFunc(e.lang.CaptionPriority, func(r transcript.Rule) bool { return r.Kind == transcript.KindAuto }) {
		it.RequiresCookie = true
		it.Notes = append(it.Notes, bilibili.MsgAICaptionsNeedLogin)
		e.platformCaptions(&it, transcript.SourceBilibiliAI)
		return it, nil
	}
	// 剛發布的 YouTube 影片，自動字幕可能還沒生成：實際會先等待，最多 24 小時才改用其他來源（SRC-R6）。
	if _, waiting := e.p.waitForAutoCaptions(item, e.lang, info, decisions); waiting {
		it.Notes = append(it.Notes, "這部影片剛發布，自動字幕可能還沒生成；處理時會先等待，最多 24 小時之後才改用其他來源。")
	}
	e.fallback(ctx, &it, t.Platform)
	return it, nil
}

// podcastItems 估算 Podcast 的各集：長度與字幕來自 feed，不需要 Probe。
// 實際長度以下載的音檔為準（itunes:duration 只是 feed 的宣稱）。
func (e *estimator) podcastItems(ctx context.Context, s *podcastSelection) []EstimateItem {
	// feedKey 只有新增時才會建立：這裡只讀，已有頻道才能知道集在不在媒體庫中。
	key := ""
	if ch, err := e.p.st.Read().ChannelByKey(ctx, source.PlatformPodcast, s.slug); err == nil && ch.URL == s.canonical {
		key = ch.ExternalID
	}
	items := make([]EstimateItem, 0, len(s.picked))
	for _, ep := range s.picked {
		it := EstimateItem{Ref: source.Ref(source.PlatformPodcast, podcast.ExternalID(keyOrSlug(key, s.slug), ep.GUID), 0), Title: ep.Title, Notes: []string{}}
		if key != "" {
			it.AlreadyInLibrary = e.inLibrary(ctx, source.Target{Platform: source.PlatformPodcast, ExternalID: podcast.ExternalID(key, ep.GUID)})
		}
		it.DurationS = ep.DurationS
		if source.Implemented(sourcePodcastTranscript) && episodeInfo(ep).HasTranscript {
			e.platformCaptions(&it, sourcePodcastTranscript)
		} else {
			e.fallback(ctx, &it, source.PlatformPodcast)
		}
		items = append(items, it)
	}
	return items
}

// keyOrSlug 回傳 feedKey；媒體庫還沒有這個 feed 時用 slug 當候選（新增時才會決定最後的 key）。
func keyOrSlug(key, slug string) string {
	if key != "" {
		return key
	}
	return slug
}

// inLibrary 回報影音是否已在媒體庫中（垃圾桶中的不算）。
func (e *estimator) inLibrary(ctx context.Context, t source.Target) bool {
	it, err := e.p.st.Read().ItemByKey(ctx, t.Platform, t.ExternalID, t.Part)
	return err == nil && it.DeletedAt == nil
}

// platformCaptions 記錄使用平台（或 feed）字幕：不需要 ASR，費用為 0。
func (e *estimator) platformCaptions(it *EstimateItem, src string) {
	it.ExpectedSource = &src
	it.ASRUSD = usd(0)
	it.finish()
}

// asrPlatform 回報 Groq 取得得到這個平台的音訊（對應 audioSourceFor 與 Podcast 的音檔下載）。
func asrPlatform(platform string) bool {
	return platform == source.PlatformYouTube || platform == source.PlatformPodcast
}

// supadataPlatform 回報 Supadata 是否處理這個平台：它只收影片網址（Podcast 的網址是 feed，不適用）。
func supadataPlatform(platform string) bool {
	return platform == source.PlatformYouTube || platform == source.PlatformBilibili
}

// fallback 依 language.fallback 的順序找第一個有設定、支援這個平台的來源並估價；都沒有時 ExpectedSource 為 nil。
func (e *estimator) fallback(ctx context.Context, it *EstimateItem, platform string) {
	for _, id := range e.lang.Fallback {
		switch id {
		case supadata.ID:
			if _, ok := e.p.fetchers[supadata.ID]; !ok {
				it.Notes = append(it.Notes, "已略過 supadata：沒有設定 API key。")
				continue
			}
			if !supadataPlatform(platform) {
				continue
			}
			src := supadata.ID
			it.ExpectedSource = &src
			// Supadata 以 credit 計價，內建計價表沒有它的單價：source_usd 未知（null），不是 0。
			it.ASRUSD = usd(0)
			it.unknown = true
			it.Notes = append(it.Notes, "Supadata 以 credit 計價，沒有內建單價，無法換算成金額。")
			it.finish()
			return
		case groq.ID:
			ready, model, note := e.groqState(ctx)
			if !ready {
				it.Notes = append(it.Notes, note)
				continue
			}
			if !asrPlatform(platform) {
				continue
			}
			src := groq.ID
			it.ExpectedSource = &src
			e.asrPrice(it, model)
			it.finish()
			return
		}
	}
	it.ASRUSD = usd(0)
	it.Notes = append(it.Notes, "沒有可用的字幕來源")
	it.finish()
}

// asrPrice 以音訊長度乘上單價算出語音轉錄費用；長度或單價未知時為 nil，並在 notes 說明。
func (e *estimator) asrPrice(it *EstimateItem, model string) {
	price, ok := cost.Lookup(groq.ID, model, cost.UnitAudioHour)
	switch {
	case !ok:
		it.unknown = true
		it.Notes = append(it.Notes, "沒有 "+model+" 的內建單價，無法估算語音轉錄費用。")
	case it.DurationS == nil:
		it.unknown = true
		it.Notes = append(it.Notes, "不知道影音的長度，無法估算語音轉錄費用。")
	default:
		it.ASRUSD = usd(*it.DurationS / 3600 * price.USDPerUnit)
	}
}

// finish 算出項目的總額：已知金額的和，沒有任何已知金額時為 nil。
func (it *EstimateItem) finish() {
	var sum float64
	known := false
	for _, v := range []*float64{it.ASRUSD, it.SourceUSD, it.LLMUSD} {
		if v != nil {
			sum, known = sum+*v, true
		}
	}
	if known {
		it.TotalUSD = usd(sum)
	}
}

// usd 把金額四捨五入到 1e-6 美元（避免浮點誤差顯示成 0.30000000000000004）。
func usd(v float64) *float64 {
	r := roundUSD(v)
	return &r
}

func roundUSD(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// probeFailure 把平台 Probe 的錯誤對應成使用者看得到的錯誤（同展開播放清單）：
// 影音不存在是 422，其他是 *UpstreamError（502）。
func probeFailure(err error) error {
	class, _ := errclass.ClassOf(err)
	switch class {
	case errclass.ClassPermanent:
		return invalidf("找不到這部影音，可能已刪除、不是公開的或有地區限制。")
	case errclass.ClassNeedsAction:
		var ce *errclass.Error
		if errors.As(err, &ce) && ce.Message != "" {
			return &UpstreamError{Msg: ce.Message, Err: err}
		}
		return &UpstreamError{Msg: "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。", Err: err}
	}
	return &UpstreamError{Msg: "yt-dlp 讀取影音資訊失敗，請稍後再試。", Err: err}
}
