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

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
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
	// 所有 fallback 來源都沒有設定（缺 API key 或尚未實作）時的訊息。
	msgNoKey = "沒有可用的字幕來源：fallback 中的來源都還沒有設定 API key。"
)

// reasonNoTranscript 是來源沒有這部影片的字幕（source_probes.reason）。
const reasonNoTranscript = "no_transcript"

// sourceTiming 是各平台字幕來源宣告的時間粒度（ADR-0006 的 Caps.Timing、plan 第 6.1 節）。
// 它是來源的宣告，不是逐檔檢查的結果：YouTube 自動字幕是近 word 級。
func sourceTiming(kind string) string {
	if kind == transcript.KindAuto {
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
// TODO(SRC-R6)：自動字幕還沒生成時等待（waiting），超過 24 小時才改用下一個來源。
// TODO(SRC-R8)：來源需要 cookie 而沒有時 blocked。
func (p *Pipeline) runTranscript(ctx context.Context, it store.Item, lang Language) (result, error) {
	// 選軌需要平台目前的字幕軌清單，所以一定要先 Probe；快取鍵只含會影響選軌結果的輸入
	// （字幕軌清單、原文語言與選軌參數），標題、片長這類不影響結果的 metadata 不算。
	info, err := p.yt.Probe(ctx, it.ExternalID)
	if err != nil {
		return result{}, err
	}
	tracks := make([]transcript.Track, 0, len(info.Tracks))
	for _, t := range info.Tracks {
		tracks = append(tracks, transcript.Track{Lang: t.Lang, Kind: t.Kind})
	}
	chosen, decisions := transcript.Select(tracks, it.OriginalLang, lang.CaptionPriority, lang.AllowPlatformTranslated)

	params := map[string]any{"caption_priority": lang.CaptionPriority, "allow_platform_translated": lang.AllowPlatformTranslated, "fallback": lang.Fallback}
	if chosen == nil && slices.Contains(lang.Fallback, supadata.ID) {
		// 會用到 Supadata 時，它的參數影響結果，要進快取鍵；用平台字幕時不受影響。
		params["supadata"] = lang.Supadata
	}
	key := cacheKey{
		Item: it.ID, Stage: StageTranscript, Impl: transcriptImplVersion, Provider: "yt-dlp",
		Upstream: paramsSHA(map[string]any{"tracks": sortedTracks(tracks), "original_lang": it.OriginalLang}),
		Params:   paramsSHA(params),
	}.String()
	stage := &store.StageResult{Executor: "local", CacheKey: key, ImplVersion: transcriptImplVersion}

	// 快取鍵相同的步驟已經完成：直接重用，不下載字幕，也不建立新版本（PIPE-R5）。
	// Probe 的結果照樣記錄（SRC-R17）。
	if hit, err := p.st.Read().DoneStageByCacheKey(ctx, it.ID, StageTranscript, key); err == nil && hit.ArtifactID != "" {
		stage.ArtifactID = hit.ArtifactID
		return result{
			task: taskDone, advance: true, stage: stage, probes: toProbes(decisions),
			state: store.StageState{Status: store.StatusDone},
		}, nil
	}

	var cues []subtitle.Cue
	if chosen != nil {
		if cues, err = p.fetchCues(ctx, it.ExternalID, *chosen); err != nil {
			return result{}, err
		}
		if len(cues) == 0 {
			for i := range decisions {
				if decisions[i].State == transcript.StateUsed {
					decisions[i].State, decisions[i].Reason = transcript.StateRejected, "empty"
				}
			}
			chosen = nil
		}
	}

	if chosen == nil {
		return p.runFallback(ctx, it, lang, stage, decisions)
	}

	version, err := p.yt.Version(ctx)
	if err != nil {
		return result{}, err
	}
	srcID := transcript.PlatformSource(chosen.Kind)
	artLang := transcript.NormalizeLang(chosen.Lang)
	prov, err := p.provenance(map[string]any{
		"stage": StageTranscript, "impl_version": transcriptImplVersion,
		"source": srcID, "timing": sourceTiming(chosen.Kind),
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
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(decisions),
		state: store.StageState{Status: store.StatusDone},
	}, nil
}

// fallbackFetcher 是一個可以執行的 fallback 來源；沒有設定（缺 API key）的來源不在 Pipeline.fetchers 中。
type fallbackFetcher struct {
	// timing 是來源宣告的時間粒度（寫進來源紀錄）。
	timing string
	// params 是會影響結果的設定（寫進來源紀錄）。
	params func(Language) any
	fetch  func(ctx context.Context, it store.Item, lang Language) (*supadata.Result, error)
}

// newFetchers 依 deps 建立可執行的 fallback 來源。
func newFetchers(d Deps) map[string]fallbackFetcher {
	out := map[string]fallbackFetcher{}
	if sd := d.Supadata; sd != nil {
		out[supadata.ID] = fallbackFetcher{
			timing: supadataTiming,
			params: func(l Language) any { return l.Supadata },
			fetch: func(ctx context.Context, it store.Item, l Language) (*supadata.Result, error) {
				return sd.Fetch(ctx, it.URL, l.Supadata.Mode)
			},
		}
	}
	return out
}

// runFallback 在平台字幕都不符合時，依 language.fallback 的順序嘗試其他來源（SRC-R3）。
// decisions 是平台字幕的選擇結果，這裡接著記錄每個被嘗試的來源（SRC-R17）。
// 回傳的錯誤交給 classify 依類別處理；needs_action 在這裡直接轉成帶修正動作的 blocked 狀態。
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
		executed = true
		got, err := f.fetch(ctx, it, lang)
		switch {
		case err == nil:
			return p.fallbackResult(id, f, it, lang, stage, decisions, got)
		case errors.Is(err, supadata.ErrNoTranscript):
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: reasonNoTranscript})
		case isNeedsAction(err):
			decisions = append(decisions, transcript.Decision{Source: id, State: transcript.StateUnavailable, Reason: string(errclass.ClassNeedsAction)})
			return blockedForKey(stage, decisions, msgSupadataKey), nil
		default:
			return result{}, err
		}
	}
	if !executed && len(lang.Fallback) > 0 {
		return blockedForKey(stage, decisions, msgNoKey), nil
	}
	return result{
		task: taskFailed, stage: stage, probes: toProbes(decisions),
		state: store.StageState{
			Status: store.StatusFailed, ErrorClass: "permanent", Message: msgNoSource, Actions: []string{actionSetupASR},
		},
	}, nil
}

// blockedForKey 是需要使用者設定或更換 API key 的步驟結果（SRC-R10）。
func blockedForKey(stage *store.StageResult, decisions []transcript.Decision, msg string) result {
	return result{
		task: taskFailed, stage: stage, probes: toProbes(decisions),
		state: store.StageState{
			Status: store.StatusBlocked, BlockReason: string(errclass.ClassNeedsAction), ErrorClass: string(errclass.ClassNeedsAction),
			Message: msg, Actions: []string{actionSetKey},
		},
	}
}

// fallbackResult 把 fallback 來源回傳的字幕寫成原始字幕的產出。
func (p *Pipeline) fallbackResult(id string, f fallbackFetcher, it store.Item, lang Language, stage *store.StageResult, decisions []transcript.Decision, got *supadata.Result) (result, error) {
	artLang := transcript.NormalizeLang(got.Lang)
	if artLang == "" {
		// 來源沒回報語言時，用影音的原文語言；都不知道就標為 und。
		if artLang = it.OriginalLang; artLang == "" {
			artLang = "und"
		}
	}
	decisions = append(decisions, transcript.Decision{Source: id, Lang: artLang, State: transcript.StateUsed})
	prov, err := p.provenance(map[string]any{
		"stage": StageTranscript, "impl_version": transcriptImplVersion,
		"source": id, "timing": f.timing,
		"params_sha256": paramsSHA(f.params(lang)),
	}, nil)
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

// isNeedsAction 回報 err 是否為 needs_action 類別。
func isNeedsAction(err error) bool {
	c, ok := errclass.ClassOf(err)
	return ok && c == errclass.ClassNeedsAction
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
func (p *Pipeline) fetchCues(ctx context.Context, videoID string, t transcript.Track) ([]subtitle.Cue, error) {
	tmpRoot := filepath.Join(p.dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tmpRoot, "sub-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	data, err := p.yt.FetchCaptions(ctx, videoID, youtube.Track{Lang: t.Lang, Kind: t.Kind}, dir)
	if err != nil {
		return nil, err
	}
	cues, err := subtitle.ParseVTT(data)
	if err != nil {
		// 平台給的檔案格式不對，重試也不會變好。
		return nil, errPermanent(fmt.Sprintf("字幕檔無法解析：%v", err))
	}
	return cues, nil
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
	}, nil
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
