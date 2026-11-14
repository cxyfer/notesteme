package pipeline

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
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
	info, err := ad.probe(ctx, it)
	if err != nil {
		return result{}, err
	}
	tracks := info.Tracks
	// Bilibili 沒有語言中繼資料：看得到 AI 字幕軌時，用它的語言當原文語言（lang_source 為 metadata），
	// 寫回 item；之後的步驟與重跑都從 item 讀到。UpdateItemMeta 會整欄覆寫 metadata_json，所以原樣帶回。
	originalLang, inferred := inferOriginalLang(it.Platform, it.OriginalLang, tracks)
	var meta *metaUpdate
	if inferred {
		meta = &metaUpdate{item: store.ItemMeta{OriginalLang: originalLang, LangSource: "metadata", Metadata: it.Metadata}}
	}
	chosen, decisions := transcript.SelectFor(it.Platform, tracks, originalLang, lang.CaptionPriority, lang.AllowPlatformTranslated)

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
			// 同理，Groq 的 base_url 與 model 影響結果。
			params["groq"] = f.params(lang)
		}
		return cacheKey{
			Item: it.ID, Stage: StageTranscript, Impl: p.implVersion(StageTranscript), Provider: "yt-dlp",
			Upstream: paramsSHA(map[string]any{"tracks": sortedTracks(tracks), "original_lang": originalLang}),
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
		return result{
			task: taskDone, advance: true, stage: stage, probes: toProbes(decisions), meta: meta,
			state: store.StageState{Status: store.StatusDone},
		}, true
	}
	if res, ok := reuse(); ok {
		return res, nil
	}

	var cues []subtitle.Cue
	if chosen != nil {
		if cues, err = p.fetchCues(ctx, ad, it, *chosen); err != nil {
			return result{}, err
		}
		if len(cues) == 0 {
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
		// 原文語言可能是這次執行才推斷出來的（Bilibili），還沒寫進影音：fallback 的語言檢查、
		// ASR 的語言提示與產出的語言都要用推斷的值，所以放進傳入的副本。
		fit := it
		fit.OriginalLang = originalLang
		res, err := p.runFallback(ctx, fit, lang, stage, decisions)
		if err == nil {
			res.meta = meta
		}
		return res, err
	}

	version, err := ad.version(ctx)
	if err != nil {
		return result{}, err
	}
	srcID := transcript.PlatformSource(it.Platform, chosen.Kind)
	artLang := transcript.NormalizeLang(chosen.Lang)
	prov, err := p.provenance(map[string]any{
		"stage": StageTranscript, "impl_version": p.implVersion(StageTranscript),
		"source": srcID, "timing": sourceTiming(it.Platform, chosen.Kind),
		"track":         map[string]string{"lang": artLang, "kind": chosen.Kind},
		"params_sha256": paramsSHA(map[string]any{"caption_priority": lang.CaptionPriority, "allow_platform_translated": lang.AllowPlatformTranslated}),
	}, map[string]string{"yt-dlp": version})
	if err != nil {
		return result{}, err
	}
	art, err := p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: artLang, Variant: subtitle.VariantSource, Cues: cues}, nil, prov)
	if err != nil {
		return result{}, err
	}
	return result{
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(decisions), meta: meta,
		state: store.StageState{Status: store.StatusDone},
	}, nil
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
}

// fallbackFetcher 是一個可以執行的 fallback 來源；沒有設定（缺 API key）的來源不在 Pipeline.fetchers 中。
type fallbackFetcher struct {
	// timing 是來源宣告的時間粒度（寫進來源紀錄）。
	timing string
	// params 是會影響結果的設定（寫進來源紀錄）。
	params func(Language) any
	fetch  func(ctx context.Context, it store.Item, lang Language) (*fetchResult, error)
}

// newFetchers 依 deps 建立可執行的 fallback 來源。
func newFetchers(d Deps) map[string]fallbackFetcher {
	out := map[string]fallbackFetcher{}
	if sd := d.Supadata; sd != nil {
		out[supadata.ID] = fallbackFetcher{
			timing: supadataTiming,
			params: func(l Language) any { return l.Supadata },
			fetch: func(ctx context.Context, it store.Item, l Language) (*fetchResult, error) {
				got, err := sd.Fetch(ctx, it.URL, l.Supadata.Mode)
				if err != nil {
					return nil, supadataUserError(err)
				}
				return &fetchResult{Lang: got.Lang, Cues: got.Cues}, nil
			},
		}
	}
	if gc := d.Groq; gc != nil && d.YouTube != nil {
		out[groq.ID] = groqFetcher(d, gc)
	}
	return out
}

// groqFetcher 是 Groq 語音轉錄來源：用 yt-dlp 下載音訊到該任務的暫存目錄，再送去轉錄（SRC-R3）。
func groqFetcher(d Deps, gc Groq) fallbackFetcher {
	return fallbackFetcher{
		timing: groq.Timing,
		params: func(Language) any {
			baseURL, model := gc.Endpoint()
			return map[string]string{"base_url": baseURL, "model": model}
		},
		fetch: func(ctx context.Context, it store.Item, _ Language) (*fetchResult, error) {
			// 沒有 API key 時不下載音訊，直接回報沒有設定（SRC-R10）。
			if !gc.Configured() {
				return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: groq.ID, Err: groq.ErrNoKey}
			}
			if it.Platform != source.PlatformYouTube {
				// 目前只有 YouTube 能取得音訊；其他平台等各自的下載實作。
				return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: groq.ID, Detail: "還不支援這個平台的音訊下載"}
			}
			// 暫存檔放在 <data>/tmp（啟動時也會清空），完成或失敗都清掉。
			tmpRoot := filepath.Join(d.DataDir, "tmp")
			if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
				return nil, err
			}
			dir, err := os.MkdirTemp(tmpRoot, "asr-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(dir)

			// 版本要在轉錄之前取得：轉錄要付費，轉錄成功之後不能再有會失敗的步驟（失敗重試會重複付費）。
			version, err := d.YouTube.Version(ctx)
			if err != nil {
				return nil, err
			}
			audio, err := d.YouTube.DownloadAudio(ctx, it.ExternalID, dir)
			if err != nil {
				return nil, &downloadError{err}
			}
			// 有原文語言才送提示；未知時讓 Whisper 自行偵測。
			got, err := gc.Transcribe(ctx, audio, groq.LangHint(it.OriginalLang))
			if err != nil {
				return nil, err
			}
			return &fetchResult{Lang: got.Lang, Cues: got.Cues, Timing: groq.Timing, ToolVersions: map[string]string{"yt-dlp": version}}, nil
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
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || ce.Message != "" {
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
	executed := false
	for _, id := range lang.Fallback {
		f, ok := p.fetchers[id]
		if !ok {
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: transcript.ReasonNotConfigured})
			continue
		}
		got, err := f.fetch(ctx, it, lang)
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
			return p.fallbackResult(id, f, it, lang, stage, decisions, got)
		case errors.Is(err, errclass.ErrNoResult):
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: reasonNoTranscript})
		case errors.Is(err, errclass.ErrNotConfigured):
			// 來源本身沒有設定（缺 API key）：和沒有註冊的來源一樣略過。
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: transcript.ReasonNotConfigured})
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
	if !executed && len(lang.Fallback) > 0 {
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
func (p *Pipeline) fallbackResult(id string, f fallbackFetcher, it store.Item, lang Language, stage *store.StageResult, decisions []transcript.Decision, got *fetchResult) (result, error) {
	artLang := transcript.NormalizeLang(got.Lang)
	if artLang == "" {
		// 來源沒回報語言時，用影音的原文語言；都不知道就標為 und。
		if artLang = it.OriginalLang; artLang == "" {
			artLang = "und"
		}
	}
	decisions = append(decisions, transcript.Decision{Source: id, Lang: artLang, State: transcript.StateUsed})
	timing := cmp.Or(got.Timing, f.timing)
	prov, err := p.provenance(map[string]any{
		"stage": StageTranscript, "impl_version": p.implVersion(StageTranscript),
		"source": id, "timing": timing,
		"params_sha256": paramsSHA(f.params(lang)),
	}, got.ToolVersions)
	if err != nil {
		return result{}, err
	}
	art, err := p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: artLang, Variant: subtitle.VariantSource, Cues: got.Cues}, nil, prov)
	if err != nil {
		return result{}, err
	}
	return result{
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(decisions),
		state: store.StageState{Status: store.StatusDone},
	}, nil
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
		return cmp.Or(strings.Compare(a.Lang, b.Lang), strings.Compare(a.Kind, b.Kind))
	})
	return out
}

// fetchCues 下載一條字幕軌並解析成 cue。暫存檔放在 <data>/tmp，用完就清除（啟動時也會清空）。
func (p *Pipeline) fetchCues(ctx context.Context, ad platformAdapter, it store.Item, t transcript.Track) ([]subtitle.Cue, error) {
	tmpRoot := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "sub-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return ad.fetchCues(ctx, it, t, dir)
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
