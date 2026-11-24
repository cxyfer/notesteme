package pipeline

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/langdetect"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// 沒有任何可用字幕來源時，使用者看到的訊息與修正動作（SRC-R5）。
const (
	msgNoSource    = "找不到可用的字幕來源。"
	actionSetupASR = "設定語音轉錄服務"

	// Supadata 拒絕 API key 時的訊息與修正動作（SRC-R10）。
	msgSupadataKey = "Supadata 拒絕了 API key。請確認 API key 正確、帳號仍有額度。"
	actionSetKey   = "設定 API key"
	// Supadata 的方案不支援所選功能時（402）的訊息與修正動作。
	msgSupadataPlan   = "Supadata 方案不支援這個功能。請升級方案，或把 supadata.mode 改為 native。"
	actionUpgradePlan = "升級 Supadata 方案"
	// 所有 fallback 來源都沒有設定（缺 API key 或尚未實作）時的訊息。
	msgNoKey = "沒有可用的字幕來源：fallback 中的來源都還沒有設定 API key。"
	// 來源回報 needs_action 卻沒有帶訊息時的通用訊息。
	msgSourceNeedsAction = "字幕來源需要你處理才能繼續。"
)

// source_probes.reason：來源沒有這部影片的字幕；來源回報的語言和影音原文語言不同（SRC-R4）。
const (
	reasonNoTranscript     = "no_transcript"
	reasonLanguageMismatch = "language_mismatch"
	// reasonDownloadFailed 是 ASR 來源需要的音訊下載失敗（yt-dlp 的問題，不是來源本身的問題）。
	reasonDownloadFailed = "download_failed"
)

// 等待 YouTube 自動字幕（SRC-R6）：每 2 小時檢查一次，從發布時間算起 24 小時後放棄。
const (
	autoWaitInterval = 2 * time.Hour
	autoWaitDeadline = 24 * time.Hour
	// reasonNotReady 是自動字幕還沒生成（source_probes.reason）。
	reasonNotReady  = "not_ready"
	msgAutoNotReady = "YouTube 還沒產生自動字幕，預計 2 小時後再檢查。"
	actionUseASRNow = "現在改用語音轉錄"
)

// sourceTiming 是各平台字幕來源宣告的時間粒度（ADR-0006 的 Caps.Timing、plan 第 6.1 節）。
// 它是來源的宣告，不是逐檔檢查的結果：YouTube 自動字幕是近 word 級。
// Bilibili 的 CC 與 AI 字幕都是 srt 的 cue 級時間。
func sourceTiming(platform, kind string) string {
	if platform == source.PlatformYouTube && kind == transcript.KindAuto {
		return "word"
	}
	return "cue"
}

// supadataTiming 是 Supadata 宣告的時間粒度：以數秒為單位的 chunk（plan 第 6.1 節）。
const supadataTiming = "chunk"

// runTranscript 依 language.caption_priority 選出第一個符合的平台字幕軌並下載（SRC-R1、SRC-R2），
// 產出 source 變體。每次 Probe 的結果都記錄在 source_probes（SRC-R17）。
//
// 平台字幕都不符合時，依 language.fallback 的順序嘗試其他來源（SRC-R3）；目前只有 Supadata 已實作，
// 其他來源（ASR 等）記為 not_configured 並略過。
// TODO(SRC-R4)：每個來源的結果還要通過品質檢查（涵蓋率、語言、重複句、純音樂）。
// 自動字幕還沒生成時等待（SRC-R6，見 waitForAutoCaptions），超過 24 小時才改用下一個來源。
// SRC-R8：Bilibili 的 AI 字幕需要 cookie 而沒有時 blocked，在等待與 fallback 之前判斷（見 blockForCookie）。
func (p *Pipeline) runTranscript(ctx context.Context, it store.Item, lang Language) (result, error) {
	// 選軌需要平台目前的字幕軌清單，所以一定要先 Probe；快取鍵只含會影響選軌結果的輸入
	// （字幕軌清單、原文語言與選軌參數），標題、片長這類不影響結果的 metadata 不算。
	ad, err := p.adapterFor(it.Platform)
	if err != nil {
		return result{}, err
	}
	// HSHR-R9、R11：有等待中的 Hub 字幕時直接寫入，不呼叫任何其他字幕來源。
	if imp, ok, err := p.pendingHubImport(ctx, it.ID); err != nil {
		return result{}, err
	} else if ok {
		return p.runHubImport(ctx, it, imp)
	}
	info, err := ad.probe(ctx, it)
	if err != nil {
		return result{}, err
	}
	tracks := info.Tracks
	la, isLocal := ad.(localAdapter)
	isPodcast := it.Platform == source.PlatformPodcast
	// 本機檔案重新計算了 sha256（內容被替換，或第一次記錄）時，隨結果一併寫回影音。
	refresh := refreshMeta(it, info)

	var (
		usable       = tracks // 還沒被拒絕的字幕軌
		originalLang string
		inputLang    string
		inferred     bool
		chosen       *transcript.Track
		decisions    []transcript.Decision
		rejected     []transcript.Decision // 選中但無法使用的字幕軌（本機字幕讀不了），已記為 rejected
		dropped      []transcript.Track    // 已放棄的字幕軌：結果由後面的字幕軌或 fallback 產生，要進快取鍵
		meta         *metaUpdate           // 推斷出原文語言時要寫回影音的欄位
		fit          store.Item            // 交給 fallback 來源的影音（原文語言為 inputLang）
	)
	// reselect 依 usable 重新選軌，並重算原文語言。
	reselect := func() {
		// Bilibili 沒有語言中繼資料：看得到 AI 字幕軌時，用它的語言當原文語言（lang_source 為 metadata）；
		// 本機影音則用選中的本機字幕的語言。寫回 item，之後的步驟與重跑都從 item 讀到。
		// UpdateItemMeta 會整欄覆寫 metadata_json，所以原樣帶回。
		originalLang, inferred = inferOriginalLang(it.Platform, it.OriginalLang, usable, lang)
		meta = nil
		if inferred {
			meta = &metaUpdate{item: store.ItemMeta{OriginalLang: originalLang, LangSource: "metadata", Metadata: it.Metadata}}
		}
		var fresh []transcript.Decision
		chosen, fresh = selectTracks(it.Platform, usable, originalLang, lang)
		decisions = mergeDecisions(rejected, fresh)

		// 原文語言可能是這次執行才推斷出來的（Bilibili），快取鍵與 fallback 都要用推斷後的值。
		// 例外：lang_source 為 asr 時，這個語言是本階段（ASR）自己的產出，不是輸入。
		// 快取鍵、Groq 的語言提示與 language_mismatch 檢查都以 inputLang 為準：第一次執行時原文語言為空，
		// 回寫之後重跑要算出相同的鍵，否則快取不會命中而重複付費。
		inputLang = originalLang
		if selfLangSource(it.LangSource) {
			inputLang = ""
		}
		fit = it
		fit.OriginalLang = inputLang
	}
	// drop 把選中的字幕軌記為 state（reason），從可用的字幕軌移除並重新選軌：
	// 本機字幕讀不了時依序改用下一條外掛字幕、內嵌字幕；Podcast 的字幕抓不到時改用下一條宣告的字幕軌；
	// 都沒有了才是 fallback（ASR）。
	drop := func(state, reason string) {
		for i := range decisions {
			if decisions[i].State == transcript.StateUsed {
				decisions[i].State, decisions[i].Reason = state, reason
				rejected = append(rejected, decisions[i])
			}
		}
		// 下載時 chosen.Format 可能被判斷出來而改變（沒有宣告 type 的字幕軌），所以不能用整個結構比對：
		// 用不含 Format 的識別找回 usable 中原本的字幕軌，否則移除不到、重新選軌又選到同一條而一直重試。
		same := func(t transcript.Track) bool {
			return t.Handle == chosen.Handle && t.Lang == chosen.Lang && t.Kind == chosen.Kind
		}
		if i := slices.IndexFunc(usable, same); i >= 0 {
			dropped = append(dropped, usable[i])
		} else {
			dropped = append(dropped, *chosen)
		}
		usable = slices.DeleteFunc(slices.Clone(usable), same)
		reselect()
	}
	reject := func(reason string) { drop(transcript.StateRejected, reason) }
	reselect()

	// 本機外掛字幕：在算快取鍵之前先讀取並解析，內容的 sha256 要進快取鍵（原地修改字幕後不能命中舊結果）。
	// 不在允許的目錄、不是一般檔案、太大或解析失敗的外掛字幕不會讓步驟失敗：記為 rejected 後改用下一條。
	var (
		cues       []subtitle.Cue
		sidecarSHA string
		cuesLoaded bool
	)
	for isLocal && chosen != nil && chosen.Kind == transcript.KindSidecar {
		c, sum, lerr := la.loadSidecar(*chosen)
		if lerr == nil {
			cues, sidecarSHA, cuesLoaded = c, sum, true
			break
		}
		if !isPermanent(lerr) {
			return result{}, lerr
		}
		p.log.Warn("外掛字幕無法使用，改用下一個字幕來源", "item", it.ID, "track", chosen.Handle, "error", lerr)
		reject(transcript.ReasonUnreadable)
	}

	// 快取鍵只含會影響結果的輸入。用平台字幕時 fallback 來源的參數不影響結果，不進快取鍵；
	// 會用到 fallback 來源時（沒選到字幕軌，或字幕軌下載後是空的）它們的參數要進快取鍵，
	// 所以後者要重算快取鍵，否則換 mode 或 model 重跑會命中舊結果。
	keyFor := func(withFallback bool) string {
		params := map[string]any{"caption_priority": lang.CaptionPriority, "allow_platform_translated": lang.AllowPlatformTranslated, "fallback": lang.Fallback}
		if withFallback && slices.Contains(lang.Fallback, supadata.ID) {
			// Supadata 的參數影響結果。
			params["supadata"] = lang.Supadata
		}
		if f, ok := p.fetchers[groq.ID]; ok && withFallback && slices.Contains(lang.Fallback, groq.ID) {
			// 同理，Groq 的 base_url、model 與語言提示影響結果。
			params["groq"] = f.params(fit, lang)
		}
		if it.Platform == source.PlatformLocal {
			// 本機影音的內容改變（sha256 不同）時，快取不能命中。
			params["content_sha256"] = info.ContentSHA256
		}
		upstream := map[string]any{"tracks": sortedTracks(tracks), "original_lang": inputLang}
		if isPodcast {
			// 音檔內容改變（重跑 metadata 重新下載）時，快取不能命中：字幕的時間可能和新的音檔不符。
			upstream["content_sha256"] = info.ContentSHA256
		}
		if len(dropped) > 0 {
			// 偏好的字幕軌無法使用、由後面的字幕軌（或 fallback）產生結果時，放棄的字幕軌也是輸入：
			// 否則後面字幕軌的成品會用和偏好字幕軌相同的鍵存起來，下次重跑時直接命中，
			// 來源紀錄卻記成偏好的字幕軌被使用。沒有放棄任何字幕軌時不加欄位，鍵與先前相同。
			upstream["dropped"] = sortedTracks(dropped)
		}
		if sidecarSHA != "" {
			// 選中的外掛字幕的內容也是輸入。
			upstream["sidecar_sha256"] = sidecarSHA
		}
		return cacheKey{
			Item: it.ID, Stage: StageTranscript, Impl: p.implVersion(StageTranscript), Provider: cacheProvider(it.Platform),
			Upstream: paramsSHA(upstream),
			Params:   paramsSHA(params),
		}.String()
	}
	stage := &store.StageResult{Executor: "local", CacheKey: keyFor(chosen == nil), ImplVersion: p.implVersion(StageTranscript)}

	// reuse 在快取鍵相同的步驟已經完成時，直接重用，不下載字幕，也不建立新版本（PIPE-R5）。
	// Probe 的結果照樣記錄（SRC-R17）。
	reuse := func() (result, bool) {
		hit, err := p.st.Read().DoneStageByCacheKey(ctx, it.ID, StageTranscript, stage.CacheKey)
		if err != nil || hit.ArtifactID == "" {
			return result{}, false
		}
		stage.ArtifactID = hit.ArtifactID
		state := store.StageState{Status: store.StatusDone}
		if isPodcast && p.timingFlagged(ctx, hit.ArtifactID) {
			// SRC-R15：快取命中時，訊息也要和第一次一樣（來源紀錄的 timing_check 記著當時的結論）。
			state.Message = msgTimingDrift
		}
		return result{
			task: taskDone, advance: true, stage: stage, probes: toProbes(decisions), meta: mergeMeta(meta, refresh),
			state: state,
		}, true
	}
	if res, ok := reuse(); ok {
		return res, nil
	}

	for chosen != nil {
		if !cuesLoaded {
			if cues, err = p.fetchCues(ctx, ad, it, chosen); err != nil {
				if isPodcast && ctx.Err() == nil && podcastFallbackable(err) {
					// Podcast 的字幕抓不到（404 等）、被防護擋下或解析失敗：記為 unavailable／permanent，
					// 改用 feed 宣告的下一條字幕軌；都不能用才改用 fallback（ASR）。
					p.log.Warn("Podcast 字幕無法使用，改用下一個字幕來源", "item", it.ID, "track", chosen.Handle, "error", err)
					drop(transcript.StateUnavailable, string(errclass.ClassPermanent))
					stage.CacheKey = keyFor(chosen == nil)
					if res, ok := reuse(); ok {
						return res, nil
					}
					continue
				}
				if !isLocal || !isPermanent(err) {
					return result{}, err
				}
				// 內嵌字幕抽取失敗：與外掛字幕相同，記為 rejected 後改用下一條，最後才是 fallback。
				p.log.Warn("內嵌字幕無法使用，改用下一個字幕來源", "item", it.ID, "track", chosen.Handle, "error", err)
				reject(transcript.ReasonUnreadable)
				stage.CacheKey = keyFor(chosen == nil)
				if res, ok := reuse(); ok {
					return res, nil
				}
				continue
			}
		}
		if len(cues) > 0 {
			break
		}
		if isPodcast {
			// Podcast 宣告的字幕軌是空的：和抓不到一樣，記為 rejected（empty）後改用下一條宣告的字幕軌，
			// 都不能用才改用 fallback（ASR）。
			p.log.Warn("Podcast 字幕是空的，改用下一個字幕來源", "item", it.ID, "track", chosen.Handle)
			reject("empty")
			cuesLoaded = false
			stage.CacheKey = keyFor(chosen == nil)
			if res, ok := reuse(); ok {
				return res, nil
			}
			continue
		}
		for i := range decisions {
			if decisions[i].State == transcript.StateUsed {
				decisions[i].State, decisions[i].Reason = transcript.StateRejected, "empty"
			}
		}
		chosen = nil
		// 改用 fallback 來源：快取鍵要含它們的參數，並再確認有沒有相同鍵的完成結果。
		stage.CacheKey = keyFor(true)
		if res, ok := reuse(); ok {
			return res, nil
		}
	}

	if chosen == nil {
		// SRC-R8：先判斷來源需不需要 cookie，才輪到等待與 fallback。
		if res, ok := p.blockForCookie(it, lang, info, stage, decisions); ok {
			res.meta = meta
			return res, nil
		}
		if res, ok := p.waitForAutoCaptions(it, lang, info, decisions); ok {
			return res, nil
		}
		res, err := p.runFallback(ctx, fit, lang, stage, decisions)
		if err == nil {
			if meta != nil {
				res.meta = meta
			}
			res.meta = mergeMeta(res.meta, refresh)
		}
		return res, err
	}

	versions, err := ad.toolVersions(ctx)
	if err != nil {
		return result{}, err
	}
	srcID := transcript.PlatformSource(it.Platform, chosen.Kind)
	artLang := transcript.NormalizeLang(chosen.Lang)
	timing := sourceTiming(it.Platform, chosen.Kind)
	state := store.StageState{Status: store.StatusDone}
	fields := map[string]any{
		"stage": StageTranscript, "impl_version": p.implVersion(StageTranscript),
		"source": srcID,
		"track":  map[string]string{"lang": artLang, "kind": chosen.Kind},
		// HSHR-R7：來源需要登入才拿得到時為 true（不進快取鍵）。
		"cookie_required": cookieRequired(it, srcID),
		"params_sha256":   paramsSHA(map[string]any{"caption_priority": lang.CaptionPriority, "allow_platform_translated": lang.AllowPlatformTranslated}),
	}
	if isPodcast {
		// 字幕沒有宣告語言、feed 也沒有時，用影音的原文語言；都不知道就標為 und。
		artLang = cmp.Or(artLang, transcript.NormalizeLang(originalLang), "und")
		fields["track"] = map[string]string{"lang": artLang, "kind": chosen.Kind}
		timing = podcastTiming(chosen.Format, cues)
		// SRC-R15：字幕的最後一句和音檔的實際長度差太多，時間可能偏移；步驟仍然完成，只附上訊息。
		if ratio, ok := timingDrift(info.AudioDurationS, cues); ok {
			flagged := ratio > timingDriftLimit
			fields["timing_check"] = map[string]any{
				"audio_duration_s": info.AudioDurationS, "last_cue_end_s": lastCueEndS(cues), "drift_ratio": ratio, "flagged": flagged,
			}
			if flagged {
				state.Message = msgTimingDrift
			}
		}
	}
	fields["timing"] = timing
	prov, err := p.provenance(fields, versions)
	if err != nil {
		return result{}, err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: artLang, Variant: subtitle.VariantSource, Cues: cues}, nil, prov)
	if err != nil {
		return result{}, err
	}
	return result{
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(decisions),
		meta: detectLang(it, mergeMeta(meta, refresh), cues), state: state,
	}, nil
}

// SRC-R15：字幕最後一句的結束時間與音檔實際長度的差距超過這個比例（不含），就提醒時間可能偏移。
const (
	timingDriftLimit = 0.02
	msgTimingDrift   = "時間可能偏移"
)

// lastCueEndS 回傳所有 cue 中最大的結束時間（秒）。
func lastCueEndS(cues []subtitle.Cue) float64 {
	var end int64
	for _, c := range cues {
		end = max(end, c.EndMS)
	}
	return float64(end) / 1000
}

// timingDrift 回傳字幕與音檔長度的差距比例：|音檔長度 − 最大的 cue 結束時間| ÷ 音檔長度。
// 音檔長度未知（不是正數）或沒有 cue 時 ok 為 false，不做檢查。
func timingDrift(audioS float64, cues []subtitle.Cue) (ratio float64, ok bool) {
	if audioS <= 0 || len(cues) == 0 {
		return 0, false
	}
	return math.Abs(audioS-lastCueEndS(cues)) / audioS, true
}

// timingFlagged 讀出產出的來源紀錄中，偏移檢查當時的結論（timing_check.flagged）。
func (p *Pipeline) timingFlagged(ctx context.Context, artifactID string) bool {
	a, err := p.st.Read().Artifact(ctx, artifactID)
	if err != nil {
		return false
	}
	var prov struct {
		TimingCheck struct {
			Flagged bool `json:"flagged"`
		} `json:"timing_check"`
	}
	return json.Unmarshal(a.Provenance, &prov) == nil && prov.TimingCheck.Flagged
}

// podcastTiming 是 Podcast 字幕的時間粒度（來源紀錄的 timing）：VTT、SRT 是 cue；
// JSON 有字詞時間是 word，否則一個 segment 一個 cue，是 segment。
func podcastTiming(format string, cues []subtitle.Cue) string {
	if format != "json" {
		return "cue"
	}
	if slices.ContainsFunc(cues, func(c subtitle.Cue) bool { return len(c.Words) > 0 }) {
		return "word"
	}
	return "segment"
}

// podcastFallbackable 回報 Podcast 字幕的錯誤是不是要改用下一個字幕來源：permanent（404、解析失敗）與被防護擋下的請求。
// 其餘（連線失敗、5xx、429）可以重試，由步驟的重試機制處理。
func podcastFallbackable(err error) bool {
	if _, ok := netguard.PolicyMessage(err); ok {
		return true
	}
	return isPermanent(err)
}

// mergeDecisions 把已拒絕的字幕軌放在重新選軌的結果之前。
// 已有拒絕紀錄的來源，重新選軌後沒有剩下字幕軌（no_track）的那一列略過，避免同一個來源同時 rejected 又 unavailable。
// 結果依來源的評估順序排列（外掛字幕、內嵌字幕），同一來源內拒絕的在前。
func mergeDecisions(rejected, fresh []transcript.Decision) []transcript.Decision {
	out := slices.Clone(rejected)
	for _, d := range fresh {
		if d.State == transcript.StateUnavailable && d.Reason == transcript.ReasonNoTrack &&
			slices.ContainsFunc(rejected, func(r transcript.Decision) bool { return r.Source == d.Source }) {
			continue
		}
		out = append(out, d)
	}
	rank := func(d transcript.Decision) int {
		switch d.Source {
		case transcript.SourceLocalSidecar:
			return 0
		case transcript.SourceLocalEmbedded:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(out, func(a, b transcript.Decision) int { return rank(a) - rank(b) })
	return out
}

// refreshMeta 回傳本機檔案這次探測時重新計算 sha256 後要寫回影音的欄位（content_sha256 與 metadata_json 的 local 記錄）；
// 沒有重算時為 nil。UpdateItemMeta 會整欄覆寫 metadata_json，所以原樣帶回其他欄位。
func refreshMeta(it store.Item, info *probed) *metaUpdate {
	if info.ComputedSHA256 == "" {
		return nil
	}
	md := it.Metadata
	if info.LocalStat != nil {
		md = withLocalStat(it.Metadata, *info.LocalStat)
	}
	return &metaUpdate{item: store.ItemMeta{ContentSHA256: info.ComputedSHA256, Metadata: md}}
}

// mergeMeta 把 refresh 的 content_sha256 與 metadata_json 併入 base；兩者的 Metadata 都由 it.Metadata 而來。
func mergeMeta(base, refresh *metaUpdate) *metaUpdate {
	switch {
	case refresh == nil:
		return base
	case base == nil:
		return refresh
	}
	out := *base
	out.item.ContentSHA256, out.item.Metadata = refresh.item.ContentSHA256, refresh.item.Metadata
	return &out
}

// reasonNeedsAction 是來源需要使用者處理（例如 cookie）時記在 source_probes 的原因。
const reasonNeedsAction = "needs_action"

// blockForCookie 實作 SRC-R8：Bilibili 的 AI 字幕需要 cookie 才看得到。以下條件全部成立時，transcript 為 blocked
// （needs_action，ResumeOn 為 cookie:bilibili），bilibili-ai 記為 unavailable／needs_action；不成立時 ok 為 false。
//  1. 平台是 Bilibili，而且 Probe 回報未登入（NeedsLogin）。
//  2. 沒有選到字幕軌（呼叫端保證）。
//  3. language.caption_priority 有 kind 為 auto 的規則。
//
// 這是推定：未登入時分不出平台有沒有 CC 或 AI 字幕、也看不到語言（SRC 規格待決問題），等 M0 驗證。
func (p *Pipeline) blockForCookie(it store.Item, lang Language, info *probed, stage *store.StageResult, decisions []transcript.Decision) (result, bool) {
	if it.Platform != source.PlatformBilibili || !info.NeedsLogin {
		return result{}, false
	}
	if !slices.ContainsFunc(lang.CaptionPriority, func(r transcript.Rule) bool { return r.Kind == transcript.KindAuto }) {
		return result{}, false
	}
	ds := slices.Clone(decisions)
	found := false
	for i, d := range ds {
		if d.Source == transcript.SourceBilibiliAI && d.State == transcript.StateUnavailable {
			ds[i].Reason, found = reasonNeedsAction, true
		}
	}
	if !found {
		ds = append(ds, transcript.Decision{Source: transcript.SourceBilibiliAI, Kind: transcript.KindAuto, State: transcript.StateUnavailable, Reason: reasonNeedsAction})
	}
	return blockedFor(stage, ds, bilibili.ResumeOnCookie, bilibili.MsgAICaptionsNeedLogin, bilibili.ActionUploadCookie, bilibili.ActionUseASR), true
}

// waitForAutoCaptions 在 YouTube 自動字幕可能還沒生成時，回傳 waiting 的結果（SRC-R6）；不需要等待時 ok 為 false。
//
// YouTube 沒有「還沒生成」的訊號，所以用推定：以下條件全部成立才等待。
//  1. 平台是 YouTube。
//  2. 沒有選到字幕軌（呼叫端保證）。
//  3. language.caption_priority 有 kind 為 auto 的規則。
//  4. 平台的 automatic_captions 完全是空的（含機器翻譯軌）。
//  5. 有發布時間，而且現在還沒超過發布時間 + 24 小時。只有日期精度（upload_date，當天 00:00 UTC）時，
//     實際發布時間可能是當天任何時刻，所以期限多補一天：該日 00:00 UTC + 48 小時。
//
// 沒有語音的影片永遠不會有自動字幕，會白等 24 小時；這是已知的取捨（見 SRC 規格的待決問題）。
// 等待不消耗重試次數，下次檢查固定是 2 小時後，不截到 24 小時的期限：期限過後的那次檢查才改用下一個來源。
func (p *Pipeline) waitForAutoCaptions(it store.Item, lang Language, info *probed, decisions []transcript.Decision) (result, bool) {
	if it.Platform != source.PlatformYouTube || info.PublishedAt == nil {
		return result{}, false
	}
	deadline := info.PublishedAt.Add(autoWaitDeadline)
	if info.PublishedAtApprox {
		deadline = deadline.Add(24 * time.Hour)
	}
	if !p.clock.Now().Before(deadline) {
		return result{}, false
	}
	if !slices.ContainsFunc(lang.CaptionPriority, func(r transcript.Rule) bool { return r.Kind == transcript.KindAuto }) {
		return result{}, false
	}
	if slices.ContainsFunc(info.Tracks, func(t transcript.Track) bool {
		return t.Kind == transcript.KindAuto || t.Kind == transcript.KindPlatformTranslated
	}) {
		return result{}, false
	}
	// 自動字幕的來源記為 unavailable（not_ready），讓影音詳情說明為什麼還沒有結果（SRC-R17）。
	probes := slices.Clone(decisions)
	for i, d := range probes {
		if d.Kind == transcript.KindAuto && d.State == transcript.StateUnavailable {
			probes[i].Reason = reasonNotReady
		}
	}
	at := p.clock.Now().Add(autoWaitInterval)
	return result{
		task: taskRequeue, notBefore: &at, countAttempt: false, probes: toProbes(probes),
		state: store.StageState{
			Status: store.StatusWaiting, ErrorClass: string(errclass.ClassWait), NotBefore: &at,
			Message: msgAutoNotReady, Actions: []string{actionUseASRNow},
		},
	}, true
}

// fetchResult 是 fallback 來源回傳的字幕，與來源無關（Supadata、Groq 等各自轉成這個形狀）。
type fetchResult struct {
	// Lang 是來源回報的語言，可能為空。
	Lang string
	Cues []subtitle.Cue
	// Timing 非空時，覆寫 fallbackFetcher.timing 寫進來源紀錄（來源依這次的結果才知道時間粒度時使用）。
	Timing string
	// ToolVersions 是這次結果用到的外部工具版本（例如 ffmpeg），寫進來源紀錄的 tool_versions；可以是 nil。
	ToolVersions map[string]string
	// ASR 是語音轉錄用到的提示（例如詞彙表組成的 prompt，COR-R7），寫進來源紀錄的 asr；nil 代表沒有。
	ASR map[string]any
}

// fallbackFetcher 是一個可以執行的 fallback 來源；沒有設定（缺 API key）的來源不在 Pipeline.fetchers 中。
type fallbackFetcher struct {
	// timing 是來源宣告的時間粒度（寫進來源紀錄）。
	timing string
	// params 是會影響結果的設定與輸入（寫進來源紀錄與快取鍵）。
	params func(store.Item, Language) any
	fetch  func(ctx context.Context, it store.Item, lang Language) (*fetchResult, error)
	// supportsGlossary 回報來源是否接受詞彙提示（ADR-0006 的 Caps.SupportsGlossary，COR-R7）。
	supportsGlossary bool
	// supports 回報來源是否支援這個平台；nil 代表支援所有平台。
	// 不支援的來源記為 unavailable（unsupported）並略過：不算已執行，也不送出任何資料。
	supports func(platform string) bool
	// configured 回報來源目前有沒有可用的 API key（key 可能存在資料庫中，執行期才知道）；nil 代表一律視為有設定。
	// 估價用它判斷 fallback 實際會不會選到這個來源。
	configured func(ctx context.Context) (bool, error)
}

// supadataSupports 回報 Supadata 是否處理這個平台：它只接受公開影音網址，不處理本機檔案（不送出本機路徑），
// 也不處理 Podcast（單集的網址是 feed 網址，不是影音頁面）。
func supadataSupports(platform string) bool {
	return platform != source.PlatformLocal && platform != source.PlatformPodcast
}

// newFetchers 依 deps 建立可執行的 fallback 來源。
func newFetchers(d Deps) map[string]fallbackFetcher {
	out := map[string]fallbackFetcher{}
	if sd := d.Supadata; sd != nil {
		f := fallbackFetcher{
			timing:   supadataTiming,
			supports: supadataSupports,
			params:   func(_ store.Item, l Language) any { return l.Supadata },
			fetch: func(ctx context.Context, it store.Item, l Language) (*fetchResult, error) {
				got, err := sd.Fetch(ctx, it.URL, l.Supadata.Mode)
				if err != nil {
					return nil, supadataUserError(err)
				}
				return &fetchResult{Lang: got.Lang, Cues: got.Cues}, nil
			},
		}
		if c, ok := sd.(interface {
			Configured(ctx context.Context) (bool, error)
		}); ok {
			f.configured = c.Configured
		}
		out[supadata.ID] = f
	}
	if gc := d.Groq; gc != nil {
		out[groq.ID] = groqFetcher(d, gc)
	}
	return out
}

// groqFetcher 是 Groq 語音轉錄來源（SRC-R3）：取得音訊（YouTube 用 yt-dlp 下載，本機影音用原檔），
// 前處理成 opus、必要時切塊後送去轉錄（見 transcribeASR）。
func groqFetcher(d Deps, gc Groq) fallbackFetcher {
	return fallbackFetcher{
		timing: groq.Timing,
		// COR-R7：Groq 的 Whisper 接受 prompt，詞彙表組成的提示會影響結果，所以進快取鍵。
		supportsGlossary: true,
		params: func(it store.Item, lang Language) any {
			baseURL, model := gc.Endpoint()
			params := map[string]any{
				"base_url":   baseURL,
				"model":      model,
				"lang_hint":  groq.LangHint(it.OriginalLang),
				"preprocess": asrPreprocess(gc.MaxFileBytes()),
			}
			// 沒有詞彙表時不加欄位：快取鍵與沒有這個功能之前相同，已付費的轉錄不會因此失效。
			if prompt := groq.GlossaryPrompt(lang.Glossary); prompt != "" {
				params["prompt"] = prompt
			}
			return params
		},
		fetch: func(ctx context.Context, it store.Item, lang Language) (*fetchResult, error) {
			// 沒有 API key 時不取得音訊，直接回報沒有設定（SRC-R10）。
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ok, err := gc.Configured(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				// 讀取 key 失敗不是「沒有設定」：和 Transcribe 一樣當成可以重試的錯誤。
				return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: groq.ID, Detail: "讀取 API key 失敗", Err: err}
			}
			if !ok {
				return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: groq.ID, Err: groq.ErrNoKey}
			}
			src := audioSourceFor(d, it.Platform)
			if src == nil {
				// 其他平台等各自的音訊來源實作。
				return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: groq.ID, Detail: "還不支援這個平台的音訊下載"}
			}
			// 暫存檔放在 <data>/tmp（啟動時也會清空），完成或失敗都清掉。
			tmpRoot, err := filepath.Abs(filepath.Join(d.DataDir, "tmp"))
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
				return nil, err
			}
			dir, err := os.MkdirTemp(tmpRoot, "asr-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(dir)

			path, versions, err := src.fetch(ctx, it, dir)
			if err != nil {
				return nil, err
			}
			// 工具版本要在轉錄之前取得：轉錄要付費，轉錄成功之後不能再有會失敗的步驟（失敗重試會重複付費）。
			tv := map[string]string{}
			for k, v := range versions {
				tv[k] = v
			}
			if d.Media != nil {
				if v, ok := d.Media.Versions(ctx)["ffmpeg"]; ok {
					tv["ffmpeg"] = v
				}
			}
			// 有原文語言才送提示；未知時讓 Whisper 自行偵測。
			var opts []groq.Option
			var asr map[string]any
			if prompt := groq.GlossaryPrompt(lang.Glossary); prompt != "" {
				opts, asr = append(opts, groq.WithPrompt(prompt)), map[string]any{"prompt": prompt}
			}
			got, err := transcribeASR(ctx, d.Media, gc, path, dir, groq.LangHint(it.OriginalLang), opts...)
			if err != nil {
				return nil, err
			}
			return &fetchResult{Lang: got.Lang, Cues: got.Cues, Timing: groq.Timing, ToolVersions: tv, ASR: asr}, nil
		},
	}
}

// downloadError 標示錯誤來自下載音訊（yt-dlp），不是 ASR 來源本身：錯誤的類別、訊息與動作沿用 yt-dlp 的，
// 但 source_probes 不能把它記成 ASR 來源要使用者處理的事（見 runFallback）。
type downloadError struct{ error }

func (e *downloadError) Unwrap() error { return e.error }

// probeReason 是 needs_action 與 permanent 錯誤記在 source_probes 的原因：
// 下載音訊失敗記為 download_failed，其餘用錯誤類別。
func probeReason(err error, class errclass.Class) string {
	var de *downloadError
	if errors.As(err, &de) {
		return reasonDownloadFailed
	}
	return string(class)
}

// supadataUserError 為 Supadata 的 needs_action 錯誤補上給使用者看的訊息與修正動作：
// 方案不支援（402）與 API key 被拒（401）要使用者做的事不同。已經帶訊息的錯誤原樣回傳。
func supadataUserError(err error) error {
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || ce.Message != "" || errors.Is(err, errclass.ErrNotConfigured) {
		return err
	}
	out := *ce
	if errors.Is(err, supadata.ErrPlanUnsupported) {
		out.Message, out.Actions = msgSupadataPlan, []string{actionUpgradePlan}
	} else {
		out.Message, out.Actions = msgSupadataKey, []string{actionSetKey}
	}
	return &out
}

// runFallback 在平台字幕都不符合時，依 language.fallback 的順序嘗試其他來源（SRC-R3）。
// decisions 是平台字幕的選擇結果，這裡接著記錄每個被嘗試的來源（SRC-R17）。
// 回傳的錯誤交給 classify 依類別處理（retryable 退避重試）；needs_action 在這裡直接轉成 blocked 狀態，
// 訊息與修正動作用來源回傳的 errclass.Error.Message 與 Actions；某個來源 permanent 時記下原因後繼續下一個來源
// （SRC-R3 依序用完）。這裡不認得任何特定來源：「沒有結果」與「沒有設定」用 errclass 的哨兵錯誤判斷。
//
// 沒有設定（缺 API key、尚未實作）的來源記為 not_configured 並略過。所有來源都因此無法執行時，
// 步驟是 blocked（needs_action，動作「設定 API key」），而不是 permanent 失敗；
// 只要有來源真的執行了並回報沒有結果，就照 SRC-R5 失敗。
func (p *Pipeline) runFallback(ctx context.Context, it store.Item, lang Language, stage *store.StageResult, decisions []transcript.Decision) (result, error) {
	executed, notConfigured := false, false
	// HSHR-R13～R15：即將呼叫第一個付費來源時查詢 Hub 一次；失敗時照常繼續，完成後附上提醒。
	prechecked, hubNote := false, ""
	for _, id := range lang.Fallback {
		f, ok := p.fetchers[id]
		if !ok {
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: transcript.ReasonNotConfigured})
			notConfigured = true
			continue
		}
		if f.supports != nil && !f.supports(it.Platform) {
			// 來源不支援這個平台（例如 Supadata 與本機檔案）：略過，不送出任何資料，也不算已執行。
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: transcript.ReasonUnsupported})
			continue
		}
		if !prechecked && paidSource(id) && (f.configured == nil || configuredOK(ctx, f)) {
			prechecked = true
			out, err := p.precheckHub(ctx, it, decisions)
			if err != nil {
				return result{}, err
			}
			if out.hit != nil {
				return *out.hit, nil
			}
			hubNote = out.note
		}
		srcLang := lang
		if !f.supportsGlossary {
			srcLang.Glossary = nil // 不支援詞彙提示的來源不會收到詞彙表（COR-R7）
		}
		got, err := f.fetch(ctx, it, srcLang)
		if !errors.Is(err, errclass.ErrNotConfigured) {
			executed = true
		}
		switch {
		case err == nil && langMismatch(it.OriginalLang, got.Lang):
			// 來源回報的語言和影音的原文語言不同：不採用，繼續下一個來源（SRC-R4）。
			// TODO(SRC-R4)：其他門檻（low_coverage、repetition、music_only）與文字語言偵測。
			decisions = append(decisions, transcript.Decision{
				Source: id, Lang: transcript.NormalizeLang(got.Lang), State: transcript.StateRejected, Reason: reasonLanguageMismatch,
			})
		case err == nil:
			res, err := p.fallbackResult(ctx, id, f, it, lang, stage, decisions, got)
			addNote(&res, hubNote)
			return res, err
		case errors.Is(err, errclass.ErrNoResult):
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: reasonNoTranscript})
		case errors.Is(err, errclass.ErrNotConfigured):
			// 來源本身沒有設定（缺 API key）：和沒有註冊的來源一樣略過。
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: transcript.ReasonNotConfigured})
			notConfigured = true
		case isNeedsAction(err):
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: probeReason(err, errclass.ClassNeedsAction)})
			msg, actions, resumeOn := msgSourceNeedsAction, []string(nil), ""
			var ce *errclass.Error
			if errors.As(err, &ce) {
				// 錯誤指定了恢復條件（例如 cookie:bilibili）時一併記錄，補上 cookie 後可依它恢復。
				resumeOn = ce.ResumeOn
				if ce.Message != "" {
					msg, actions = ce.Message, ce.Actions
				}
			}
			return blockedFor(stage, decisions, resumeOn, msg, actions...), nil
		case isPermanent(err):
			// 這個來源的請求有問題（重試也不會好）：記下原因後繼續下一個來源；全部用完才照 SRC-R5。
			// err 只含狀態碼等診斷資訊，不含 API key（NFR-SEC-3）。
			p.log.Warn("字幕來源回報永久性錯誤，改用下一個來源", "source", id, "error", err)
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: probeReason(err, errclass.ClassPermanent)})
		default:
			return result{}, err
		}
	}
	// 全都因為沒有設定而無法執行才 blocked；只是不支援這個平台的來源不要求使用者設定 API key。
	if !executed && notConfigured {
		return blockedFor(stage, decisions, "", msgNoKey, actionSetKey), nil
	}
	return result{
		task: taskFailed, stage: stage, probes: toProbes(decisions),
		state: store.StageState{
			Status: store.StatusFailed, ErrorClass: "permanent", Message: msgNoSource, Actions: []string{actionSetupASR},
		},
	}, nil
}

// blockedFor 是需要使用者處理的步驟結果（SRC-R8、SRC-R10）：設定或更換 API key、升級方案、上傳 cookie。
// resumeOn 是機器可讀的恢復條件（例如 cookie:bilibili），沒有時傳空字串。
func blockedFor(stage *store.StageResult, decisions []transcript.Decision, resumeOn, msg string, actions ...string) result {
	return result{
		task: taskFailed, stage: stage, probes: toProbes(decisions),
		state: store.StageState{
			Status: store.StatusBlocked, BlockReason: string(errclass.ClassNeedsAction), ErrorClass: string(errclass.ClassNeedsAction),
			ResumeOn: resumeOn, Message: msg, Actions: actions,
		},
	}
}

// fallbackResult 把 fallback 來源回傳的字幕寫成原始字幕的產出。
func (p *Pipeline) fallbackResult(ctx context.Context, id string, f fallbackFetcher, it store.Item, lang Language, stage *store.StageResult, decisions []transcript.Decision, got *fetchResult) (result, error) {
	artLang := transcript.NormalizeLang(got.Lang)
	if artLang == "" {
		// 來源沒回報語言時，用影音的原文語言；都不知道就標為 und。
		if artLang = it.OriginalLang; artLang == "" {
			artLang = "und"
		}
	}
	decisions = append(decisions, transcript.Decision{Source: id, Lang: artLang, State: transcript.StateUsed})
	timing := cmp.Or(got.Timing, f.timing)
	fields := map[string]any{
		"stage": StageTranscript, "impl_version": p.implVersion(StageTranscript),
		"source": id, "timing": timing,
		"params_sha256": paramsSHA(f.params(it, lang)),
		// HSHR-R7。
		"cookie_required": cookieRequired(it, id),
	}
	if got.ASR != nil {
		fields["asr"] = got.ASR
	}
	prov, err := p.provenance(fields, got.ToolVersions)
	if err != nil {
		return result{}, err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: artLang, Variant: subtitle.VariantSource, Cues: got.Cues}, nil, prov)
	if err != nil {
		return result{}, err
	}
	res := result{
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(decisions),
		state: store.StageState{Status: store.StatusDone},
	}
	// SRC-R11：原文語言未知、ASR 回報了語言時，寫回影音的原文語言（lang_source 為 asr）。
	// UpdateItemMeta 會整欄覆寫 metadata_json，所以一定要帶上原本的 Metadata。
	if isASR(id) && it.OriginalLang == "" && got.Lang != "" {
		res.meta = &metaUpdate{item: store.ItemMeta{OriginalLang: got.Lang, LangSource: langSourceASR, Metadata: it.Metadata}}
	}
	res.meta = detectLang(it, res.meta, got.Cues)
	return res, nil
}

// 原文語言的來源（items.lang_source，SRC-R11）。
const (
	langSourceASR      = "asr"
	langSourceDetected = "detected"
)

// selfLangSource 回報這個 lang_source 是不是 transcript 步驟自己的產出（ASR 回報、文字偵測）：
// 重跑 transcript 時這種原文語言不當成輸入，快取鍵、ASR 的語言提示都視為未知，否則重跑會因為鍵改變而重複付費。
func selfLangSource(src string) bool { return src == langSourceASR || src == langSourceDetected }

// detectLang 依這次的字幕文字補上 SRC-R11 的第三層（2026-11-24）：
//   - 原文語言：平台中繼資料、ASR 都沒有給（影音與 meta 都沒有原文語言，或原文語言本來就是偵測來的）時，
//     採用文字偵測的主要語言，lang_source 為 detected。
//   - mixed：不論原文語言來自哪一層，兩種語言都各占 20% 以上時標為 mixed，否則清掉。
//
// 文字太短、判斷不了時不改動任何欄位。meta 可以是 nil；回傳的 meta 一定帶著影音原本的 Metadata（UpdateItemMeta 整欄覆寫）。
func detectLang(it store.Item, meta *metaUpdate, cues []subtitle.Cue) *metaUpdate {
	var b strings.Builder
	for _, c := range cues {
		b.WriteString(c.Text)
		b.WriteByte('\n')
	}
	d, ok := langdetect.Detect(b.String())
	if !ok {
		return meta
	}
	out := metaUpdate{item: store.ItemMeta{Metadata: it.Metadata}}
	if meta != nil {
		out = *meta
	}
	// it 可能是交給 fallback 的副本（原文語言是 ASR 的產出時被清空，lang_source 仍是 asr）：那也算已知。
	known := out.item.OriginalLang != "" ||
		(it.LangSource != langSourceDetected && (it.OriginalLang != "" || it.LangSource != ""))
	if !known && d.Lang != "" {
		out.item.OriginalLang, out.item.LangSource = d.Lang, langSourceDetected
	}
	mixed := d.Mixed
	out.mixed = &mixed
	return &out
}

// langMismatch 回報來源回報的語言 got 是否和影音的原文語言 want 不同（SRC-R4 的 language_mismatch）。
// M1 只比較來源回報的語言，不做文字語言偵測；任一邊未知（空字串或 und）時略過檢查。
// 只比主要語言：zh 與 zh-Hans、en 與 en-orig 視為相同。
func langMismatch(want, got string) bool {
	w, g := transcript.PrimaryLang(want), transcript.PrimaryLang(got)
	return w != "" && g != "" && w != g
}

// isNeedsAction 回報 err 是否為 needs_action 類別。
func isNeedsAction(err error) bool {
	c, ok := errclass.ClassOf(err)
	return ok && c == errclass.ClassNeedsAction
}

// isPermanent 回報 err 是否為 permanent 類別。
func isPermanent(err error) bool {
	c, ok := errclass.ClassOf(err)
	return ok && c == errclass.ClassPermanent
}

// sortedTracks 回傳排序後的字幕軌副本，讓快取鍵不受平台回報順序影響。
func sortedTracks(in []transcript.Track) []transcript.Track {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b transcript.Track) int {
		return cmp.Or(strings.Compare(a.Lang, b.Lang), strings.Compare(a.Kind, b.Kind), strings.Compare(a.Handle, b.Handle))
	})
	return out
}

// fetchCues 下載一條字幕軌並解析成 cue。暫存檔放在 <data>/tmp，用完就清除（啟動時也會清空）。
// 平台在下載後才判斷格式時（formatSniffer），把判斷出的格式寫回 t.Format，供來源紀錄使用。
func (p *Pipeline) fetchCues(ctx context.Context, ad platformAdapter, it store.Item, t *transcript.Track) ([]subtitle.Cue, error) {
	if fs, ok := ad.(formatSniffer); ok {
		cues, format, err := fs.fetchCuesWithFormat(ctx, *t)
		if err == nil && t.Format == "" {
			t.Format = format
		}
		return cues, err
	}
	tmpRoot := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "sub-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return ad.fetchCues(ctx, it, *t, dir)
}

// newCuesArtifact 把字幕寫成產出檔，回傳要寫進資料庫的記錄。
func (p *Pipeline) newCuesArtifact(itemID string, tr subtitle.Transcript, parents []string, prov json.RawMessage) (*store.NewArtifact, error) {
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(p.dataDir, itemID, id, func(w io.Writer) error {
		return subtitle.RenderCuesJSON(w, tr)
	})
	if err != nil {
		return nil, fmt.Errorf("寫入產出檔：%w", err)
	}
	return &store.NewArtifact{
		ID: id, ItemID: itemID, Kind: "transcript", Variant: string(tr.Variant), Lang: tr.Lang,
		Format: cuesFormat, Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
		Timings: cueTimings(tr.Cues),
	}, nil
}

// cueTimings 回傳 cues 中出現過的 timing origin，排序並去重，供列表依 timing 篩選（LIB-R7）。
func cueTimings(cues []subtitle.Cue) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cues {
		if c.Timing == "" || seen[string(c.Timing)] {
			continue
		}
		seen[string(c.Timing)] = true
		out = append(out, string(c.Timing))
	}
	slices.Sort(out)
	return out
}

// toProbes 把選擇的結果轉成 source_probes 的列。
func toProbes(ds []transcript.Decision) []store.NewProbe {
	out := make([]store.NewProbe, 0, len(ds))
	for _, d := range ds {
		out = append(out, store.NewProbe{
			SourceID: d.Source, Lang: d.Lang, Kind: d.Kind,
			Available: d.State == transcript.StateUsed || d.State == transcript.StateRejected,
			State:     d.State, Reason: d.Reason,
		})
	}
	return out
}
