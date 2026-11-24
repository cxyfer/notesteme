package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

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

const (
	// estimateConcurrency 是整個程序同時為估價 Probe 的上限（跨請求共用，見 estimateLimiter）。
	estimateConcurrency = 4
	// maxEstimateItems 是一次估價最多 Probe 的影音數；播放清單超過時只估前面這些，其餘在 notes 說明。
	maxEstimateItems = 100
	// maxEstimateDurationS 是估價採用的長度上限（1000 小時）；feed 宣稱更長或不是有限值時視為不知道長度。
	maxEstimateDurationS = 1000 * 3600
)

// estimateBudget 是一次估價所有 Probe 的總時間上限；超過時還沒估到的影音標為未知。測試會調小它。
var estimateBudget = 3 * time.Minute

// estimateLimiter 限制整個程序同時為估價進行的 Probe 數：多個估價請求同時進來時，yt-dlp 的並行數不會倍增。
type estimateLimiter struct {
	once  sync.Once
	slots chan struct{}
}

func (l *estimateLimiter) sem() chan struct{} {
	l.once.Do(func() { l.slots = make(chan struct{}, estimateConcurrency) })
	return l.slots
}

// errEstimateBudget 是估價的總時間用完、還沒輪到 Probe 的影音。
var errEstimateBudget = errors.New("估價逾時，沒有取得這部影音的資訊。")

// acquire 取得一個 Probe 名額；ctx 已經結束或在等待時結束就回傳 false（不佔用名額）。
func acquire(ctx context.Context, sem chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

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
	// LLMUSD 是 LLM 步驟的費用：用片長推估 token 數，依 COST-R5 估算（estimateLLM）；
	// 片長或單價未知、或有沒有估算方法的付費步驟時為 nil；沒有任何付費 LLM 步驟時為 0。
	LLMUSD *float64
	// TotalUSD 是已知金額的和；沒有任何已知金額時為 nil。
	TotalUSD         *float64
	RequiresCookie   bool
	AlreadyInLibrary bool
	Notes            []string
	// unknown 為 true 代表有金額不知道（長度、單價未知，或 Probe 失敗）。
	unknown bool
	// sc 是估算 LLM 費用用的步驟情境；probed 為 false 代表沒有取得影音的資訊（Probe 失敗），不估算 LLM 費用。
	sc     StageContext
	probed bool
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
	e := &estimator{p: p, lang: lang, eff: x.plan.eff}
	out := &Estimate{Currency: "USD", PricesAsOf: cost.PricesAsOf, Notes: []string{}}
	targets := x.targets
	if x.podcast == nil && len(targets) > maxEstimateItems {
		out.Notes = append(out.Notes, fmt.Sprintf("共 %d 部影音，只估算前 %d 部。", len(targets), maxEstimateItems))
		targets = targets[:maxEstimateItems]
	}
	if x.podcast != nil {
		if out.Items, err = e.podcastItems(ctx, x.podcast); err != nil {
			return nil, err
		}
	} else if out.Items, err = e.probeItems(ctx, targets); err != nil {
		return nil, err
	}

	for i := range out.Items {
		if err := e.llm(ctx, &out.Items[i]); err != nil {
			return nil, err
		}
	}
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
	if e.allFailed {
		out.Notes = append(out.Notes, "所有影音都無法取得資訊，原因見各項的說明。")
	}
	if unknown {
		out.Notes = append(out.Notes, "部分金額未知")
	}
	return out, nil
}

// estimator 是一次估價用到的狀態：生效的語言設定，以及（延遲查詢一次的）Groq 狀態。
type estimator struct {
	p    *Pipeline
	lang Language
	eff  Effective
	// allFailed 為 true 代表有多部影音而且每一部的 Probe 都失敗（例如 yt-dlp 無法使用）。
	allFailed bool
	// groqCustomBase 為 true 代表 Groq 設定了自訂的 base_url。
	groqCustomBase bool

	supadataOnce  sync.Once
	supadataReady bool
	supadataNote  string

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
		var base string
		base, e.groqModel = g.Endpoint()
		// 自訂的 base_url 可能是其他相容服務（例如自架的 Whisper），Groq 的內建單價不適用。
		e.groqCustomBase = strings.TrimRight(base, "/") != strings.TrimRight(groq.DefaultBaseURL, "/")
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

// supadataState 回報 Supadata 有沒有可用的 API key；整次估價只查一次。
// serve 在有服務金鑰庫時一律裝上 Supadata 的 client，key 存在資料庫中，所以要實際查詢：
// 沒有 key 時，處理時 Supadata 會記為 not_configured 並改用下一個來源，估價也要略過它。
func (e *estimator) supadataState(ctx context.Context) (ready bool, note string) {
	e.supadataOnce.Do(func() {
		f, ok := e.p.fetchers[supadata.ID]
		if !ok {
			e.supadataNote = "已略過 supadata：沒有設定 API key。"
			return
		}
		if f.configured == nil {
			e.supadataReady = true
			return
		}
		switch ok, err := f.configured(ctx); {
		case err != nil:
			e.p.log.Debug("估價時讀取 Supadata API key 失敗，視為沒有設定", "error", err.Error())
			e.supadataNote = "已略過 supadata：無法確認 API key。"
		case !ok:
			e.supadataNote = "已略過 supadata：沒有設定 API key。"
		default:
			e.supadataReady = true
		}
	})
	return e.supadataReady, e.supadataNote
}

// probeItems 對每個 Target 做 Probe 並估價；結果的順序與 targets 相同。
// 所有 Probe 共用 estimateBudget 的總時間：用完時還沒輪到的影音不再 Probe，標為未知。
func (e *estimator) probeItems(ctx context.Context, targets []source.Target) ([]EstimateItem, error) {
	items := make([]EstimateItem, len(targets))
	errs := make([]error, len(targets))
	bctx, cancel := context.WithTimeout(ctx, estimateBudget)
	defer cancel()
	sem := e.p.estimates.sem()
	var wg sync.WaitGroup
	for i, t := range targets {
		if !acquire(bctx, sem) {
			items[i] = EstimateItem{Ref: source.Ref(t.Platform, t.ExternalID, t.Part), Notes: []string{}}
			errs[i] = errEstimateBudget
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			items[i], errs[i] = e.probeItem(bctx, t)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, err := range errs {
		if le := (*libraryError)(nil); errors.As(err, &le) {
			return nil, err
		}
	}
	failed := 0
	for i, err := range errs {
		if err == nil {
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = errEstimateBudget
		}
		if len(targets) == 1 {
			if errors.Is(err, errEstimateBudget) {
				return nil, &UpstreamError{Msg: err.Error(), Err: err}
			}
			return nil, err
		}
		// 多部影音：一部失敗不讓整份估價失敗，在該項說明。
		failed++
		items[i].unknown = true
		items[i].Notes = append(items[i].Notes, err.Error())
	}
	if failed == len(targets) && failed > 0 {
		e.allFailed = true
	}
	return items, nil
}

// probeItem 估算一部 YouTube 或 Bilibili 影音。Probe 失敗時回傳已填好 ref 的空白項目與使用者看得到的錯誤。
func (e *estimator) probeItem(ctx context.Context, t source.Target) (EstimateItem, error) {
	it := EstimateItem{Ref: source.Ref(t.Platform, t.ExternalID, t.Part), Notes: []string{}}
	in, err := e.inLibrary(ctx, t)
	if err != nil {
		return it, err
	}
	it.AlreadyInLibrary = in
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
		it.setDuration(&d)
	}
	if info.Live {
		it.Notes = append(it.Notes, "這是直播或還沒首播的影音，要等結束後才會處理；長度與費用以結束後為準。")
	}
	if t.Platform == source.PlatformYouTube && slices.Contains(cookieAvailabilities, info.Availability) {
		it.RequiresCookie = true
		it.Notes = append(it.Notes, "這部影音需要 cookie 才能取得。")
	}

	originalLang, _ := inferOriginalLang(t.Platform, info.Language, info.Tracks, e.lang)
	chosen, decisions := selectTracks(t.Platform, info.Tracks, originalLang, e.lang)
	it.probed = true
	it.sc = estimateContextFor(t.Platform, originalLang, chosen != nil && chosen.Kind == transcript.KindManual, len(info.Chapters), false)
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
func (e *estimator) podcastItems(ctx context.Context, s *podcastSelection) ([]EstimateItem, error) {
	// feedKey 只有新增時才會建立：這裡只讀。feed 已經新增過時用它的 key（和新增一樣以 feed 網址查），
	// 才知道各集在不在媒體庫中；還沒新增過時用新增此刻會取得的 key 當候選。
	key, existing, err := e.p.st.Read().PeekPodcastFeedKey(ctx, s.canonical, s.slug)
	if err != nil {
		return nil, &libraryError{err: err}
	}
	items := make([]EstimateItem, 0, len(s.picked))
	for _, ep := range s.picked {
		externalID := podcast.ExternalID(key, ep.GUID)
		it := EstimateItem{Ref: source.Ref(source.PlatformPodcast, externalID, 0), Title: ep.Title, Notes: []string{}}
		if existing {
			if it.AlreadyInLibrary, err = e.inLibrary(ctx, source.Target{Platform: source.PlatformPodcast, ExternalID: externalID}); err != nil {
				return nil, err
			}
		}
		it.setDuration(ep.DurationS)
		it.probed = true
		it.sc = estimateContextFor(source.PlatformPodcast, podcast.Lang(s.feed.Language), false, 0, true)
		if e.podcastCaptions(s.feed.Language, ep) {
			e.platformCaptions(&it, transcript.SourcePodcastTranscript)
		} else {
			e.fallback(ctx, &it, source.PlatformPodcast)
		}
		items = append(items, it)
	}
	return items, nil
}

// podcastCaptions 回報 transcript 步驟會不會選到這一集的 <podcast:transcript>：
// 與 transcript 步驟相同，用 feed 的語言（或字幕的語言）推斷原文語言，再依 caption_priority 選軌。
func (e *estimator) podcastCaptions(feedLang string, ep podcast.Episode) bool {
	if !source.Implemented(transcript.SourcePodcastTranscript) {
		return false
	}
	ts := make([]podcastTranscript, 0, len(ep.Transcripts))
	for _, t := range ep.Transcripts {
		ts = append(ts, podcastTranscript{URL: t.URL, Type: t.Type, Language: t.Language, Rel: t.Rel})
	}
	tracks := podcastTracks(ts, feedLang)
	originalLang, _ := inferOriginalLang(source.PlatformPodcast, podcast.Lang(feedLang), tracks, e.lang)
	chosen, _ := selectTracks(source.PlatformPodcast, tracks, originalLang, e.lang)
	return chosen != nil
}

// setDuration 記錄影音的長度。長度來自不可信的 feed 或平台：不是有限值、負數或超過 maxEstimateDurationS 時
// 視為不知道長度（DurationS 為 nil），在 notes 說明，金額也不會因此變成無限大。
func (it *EstimateItem) setDuration(d *float64) {
	if d == nil {
		return
	}
	if math.IsNaN(*d) || math.IsInf(*d, 0) || *d < 0 || *d > maxEstimateDurationS {
		it.Notes = append(it.Notes, "影音宣稱的長度不合理，視為不知道長度。")
		return
	}
	v := *d
	it.DurationS = &v
}

// inLibrary 回報影音是否已在媒體庫中（垃圾桶中的不算）。
// 找不到是 false；其他錯誤（資料庫無法讀取等）包成 libraryError 往上傳，不能被當成「不在媒體庫中」。
func (e *estimator) inLibrary(ctx context.Context, t source.Target) (bool, error) {
	it, err := e.p.st.Read().ItemByKey(ctx, t.Platform, t.ExternalID, t.Part)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, &libraryError{err: err}
	}
	return it.DeletedAt == nil, nil
}

// libraryError 是查詢媒體庫失敗：整份估價失敗，不會只記在單一項目的 notes。
type libraryError struct{ err error }

func (e *libraryError) Error() string { return "查詢媒體庫失敗：" + e.err.Error() }
func (e *libraryError) Unwrap() error { return e.err }

// llm 估算一部影音的 LLM 費用（ING-R8 的 llm_usd），並重算總額。Probe 失敗的項目不估算（金額已是未知）。
func (e *estimator) llm(ctx context.Context, it *EstimateItem) error {
	if !it.probed {
		return nil
	}
	est, err := e.p.estimateLLM(ctx, e.eff, it.sc, it.Title, it.DurationS)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 設定在新增時就驗證過；這裡失敗只讓 LLM 費用變成未知，不讓整份估價失敗。
		e.p.log.Debug("估算 LLM 費用失敗，視為未知", "ref", it.Ref, "error", err.Error())
		est = llmEstimate{unknown: 1}
	}
	if est.unknown > 0 {
		it.LLMUSD = nil
		it.unknown = true
		if it.DurationS == nil {
			it.Notes = append(it.Notes, "不知道影音的長度，無法估算 LLM 費用。")
		} else {
			it.Notes = append(it.Notes, "部分 LLM 步驟找不到單價或沒有估算方法，無法估算 LLM 費用。")
		}
	} else {
		it.LLMUSD = usd(est.usd.USD())
		if est.usd > 0 {
			it.Notes = append(it.Notes, "LLM 費用依片長推估字幕長度，是估算值。")
		}
	}
	it.finish()
	return nil
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
			// 與處理時相同：先看來源支不支援這個平台，再看 API key；
			// 不支援的來源就算設定了 key 也用不上，不該提示使用者去設定。
			if !supadataPlatform(platform) {
				continue
			}
			if ready, note := e.supadataState(ctx); !ready {
				it.Notes = append(it.Notes, note)
				continue
			}
			src := supadata.ID
			it.ExpectedSource = &src
			it.ASRUSD = usd(0)
			e.supadataPrice(ctx, it)
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
			e.asrPrice(ctx, it, model)
			it.finish()
			return
		}
	}
	it.ASRUSD = usd(0)
	it.Notes = append(it.Notes, "沒有可用的字幕來源")
	it.finish()
}

// asrPrice 以音訊長度乘上單價算出語音轉錄費用；長度或單價未知時為 nil，並在 notes 說明。
// 單價依 COST-R2 的順序（覆寫優先，再來是內建計價表；自訂的 base_url 不套用內建計價表），與預留相同（COST-R5）。
func (e *estimator) asrPrice(ctx context.Context, it *EstimateItem, model string) {
	if it.DurationS == nil {
		it.unknown = true
		it.Notes = append(it.Notes, "不知道影音的長度，無法估算語音轉錄費用。")
		return
	}
	est, priced, err := e.unitEstimate(ctx, cost.Call{
		Stage: StageTranscript, Provider: groq.ID, Model: model, CustomBase: e.groqCustomBase,
		Unit: cost.UnitAudioHour, Units: *it.DurationS / 3600,
	})
	switch {
	case err != nil:
		e.p.log.Debug("估價時讀取 Groq 的單價失敗", "error", err.Error())
		it.unknown = true
		it.Notes = append(it.Notes, "無法讀取 "+model+" 的單價，無法估算語音轉錄費用。")
	case !priced && e.groqCustomBase:
		it.unknown = true
		it.Notes = append(it.Notes, "Groq 使用自訂的 base_url，沒有內建單價，也沒有設定單價，無法估算語音轉錄費用。")
	case !priced:
		it.unknown = true
		it.Notes = append(it.Notes, "沒有 "+model+" 的單價，無法估算語音轉錄費用。")
	default:
		it.ASRUSD = usd(est.USD())
	}
}

// supadataPrice 估算 Supadata 的費用（source_usd）：依取得模式與片長估算的 credit 數乘以 credit 的單價（與預留相同）。
// 內建計價表沒有 credit 的單價，沒有設定單價時 source_usd 未知（null），不是 0（COST 待決問題 C18）。
func (e *estimator) supadataPrice(ctx context.Context, it *EstimateItem) {
	var durS float64
	if it.DurationS != nil {
		durS = *it.DurationS
	}
	est, priced, err := e.unitEstimate(ctx, cost.Call{
		Stage: StageTranscript, Provider: supadata.ID, Model: supadata.PriceModel,
		Unit: cost.UnitCredit, Units: supadata.EstimateCredits(e.lang.Supadata.Mode, durS),
	})
	switch {
	case err != nil:
		e.p.log.Debug("估價時讀取 Supadata 的單價失敗", "error", err.Error())
		it.unknown = true
		it.Notes = append(it.Notes, "無法讀取 Supadata 的單價，無法換算成金額。")
	case !priced:
		it.unknown = true
		it.Notes = append(it.Notes, "Supadata 以 credit 計價，沒有內建單價，無法換算成金額。")
	default:
		it.SourceUSD = usd(est.USD())
		if e.lang.Supadata.Mode != supadata.ModeNative {
			it.Notes = append(it.Notes, "Supadata 的 credit 依片長以 AI 產生字幕的用量推估，是估算值。")
		}
	}
}

// unitEstimate 估算以單一單價計價的呼叫（COST-R5 的方法，與預留共用 cost.Service.Estimate）；
// 沒有成本服務（測試的最小組裝）時只查內建計價表。
func (e *estimator) unitEstimate(ctx context.Context, c cost.Call) (cost.Micro, bool, error) {
	if e.p.cost != nil {
		return e.p.cost.Estimate(ctx, e.p.st.Read(), c)
	}
	if c.CustomBase {
		return 0, false, nil
	}
	price, ok := cost.Lookup(c.Provider, c.Model, c.Unit)
	if !ok {
		return 0, false, nil
	}
	m, _ := cost.FromUSD(c.Units * price.USDPerUnit)
	return m, true, nil
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
