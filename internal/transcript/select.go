// Package transcript 決定用哪個來源取得原始字幕（ADR-0006、SRC 規格）。
// 這裡是不碰外部工具的純邏輯：依 language.caption_priority 從平台字幕軌中挑一條，並記錄每個來源的結果（SRC-R17）。
package transcript

import "strings"

// 字幕軌與規則的種類。
const (
	KindManual             = "manual"
	KindAuto               = "auto"
	KindPlatformTranslated = "platform-translated"
)

// 字幕來源 ID（steps.md 第 3 節）。
const (
	SourceYouTubeManual = "youtube-manual"
	SourceYouTubeAuto   = "youtube-auto"
)

// OriginalLang 在 caption_priority 中代表影音的原文語言。
const OriginalLang = "original"

// Rule 是 language.caption_priority 的一項。
type Rule struct {
	Lang string `json:"lang"`
	Kind string `json:"kind"`
}

// Track 是平台提供的字幕軌。
type Track struct {
	Lang string
	Kind string
}

// Probe 結果的狀態（source_probes.state）。
const (
	StateUsed        = "used"
	StateRejected    = "rejected"
	StateUnavailable = "unavailable"
)

// Decision 是一個來源在這次選擇中的結果。
type Decision struct {
	Source string
	Lang   string
	Kind   string
	State  string
	Reason string
}

// 沒有採用的原因。
const (
	ReasonNoTrack             = "no_track"
	ReasonOriginalLangUnknown = "original_language_unknown"
	ReasonPlatformTranslated  = "platform_translated"
	ReasonNotConfigured       = "not_configured"
)

// PlatformSource 回傳字幕軌種類對應的來源 ID。機器翻譯的字幕軌由 YouTube 的自動字幕機制產生。
func PlatformSource(kind string) string {
	if kind == KindManual {
		return SourceYouTubeManual
	}
	return SourceYouTubeAuto
}

// NormalizeLang 去掉 yt-dlp 在原文自動字幕後加的 -orig（en-orig → en）。
func NormalizeLang(l string) string {
	return strings.TrimSuffix(l, "-orig")
}

// 比對的分數：越高越符合。
const (
	rankNone = iota
	rankPrimary
	rankExact
)

// rankLang 比較想要的語言 want 與字幕軌的語言 have。
// 完全相同（不分大小寫，忽略 -orig）最高；want 只有主要語言（例如 zh、en）時，
// 同一主要語言的地區或文字變體（zh-Hans、en-US）次之；其他不符合。
func rankLang(want, have string) int {
	want, have = strings.ToLower(NormalizeLang(want)), strings.ToLower(NormalizeLang(have))
	switch {
	case want == "" || have == "":
		return rankNone
	case want == have:
		return rankExact
	case !strings.Contains(want, "-") && strings.HasPrefix(have, want+"-"):
		return rankPrimary
	}
	return rankNone
}

// best 在 tracks 中找出符合 lang 的最佳字幕軌；kinds 依序是可以接受的種類（前面的優先）。
func best(tracks []Track, lang string, kinds ...string) (Track, bool) {
	for _, kind := range kinds {
		var (
			found Track
			rank  = rankNone
		)
		for _, t := range tracks {
			if t.Kind != kind {
				continue
			}
			if r := rankLang(lang, t.Lang); r > rank {
				found, rank = t, r
			}
		}
		if rank > rankNone {
			return found, true
		}
	}
	return Track{}, false
}

// Select 依 rules 的順序尋找平台字幕軌，使用第一個符合的（SRC-R1）。
//
//   - originalLang 是影音的原文語言，rules 中的 original 以它代入；不知道原文語言時，該項視為沒有符合。
//   - 機器翻譯的字幕軌（platform-translated）預設不採用；allowTranslated 為 true 時才可以被 kind 為 auto 的規則選用（SRC-R2）。
//
// decisions 依規則的順序記錄每個被評估的來源：沒有符合的為 unavailable，採用的為 used；
// 排在採用的規則之後的項目沒有被評估，不記錄。
func Select(tracks []Track, originalLang string, rules []Rule, allowTranslated bool) (chosen *Track, decisions []Decision) {
	seen := map[Decision]bool{}
	add := func(d Decision) {
		if !seen[d] {
			seen[d] = true
			decisions = append(decisions, d)
		}
	}
	for _, rule := range rules {
		lang := rule.Lang
		if lang == OriginalLang {
			if originalLang == "" {
				add(Decision{Source: PlatformSource(rule.Kind), Lang: OriginalLang, Kind: rule.Kind, State: StateUnavailable, Reason: ReasonOriginalLangUnknown})
				continue
			}
			lang = originalLang
		}
		kinds := []string{rule.Kind}
		if rule.Kind == KindAuto && allowTranslated {
			kinds = append(kinds, KindPlatformTranslated)
		}
		if t, ok := best(tracks, lang, kinds...); ok {
			add(Decision{Source: PlatformSource(t.Kind), Lang: NormalizeLang(t.Lang), Kind: t.Kind, State: StateUsed})
			return &t, decisions
		}
		add(Decision{Source: PlatformSource(rule.Kind), Lang: lang, Kind: rule.Kind, State: StateUnavailable, Reason: ReasonNoTrack})
	}
	if !allowTranslated {
		for _, t := range tracks {
			if t.Kind == KindPlatformTranslated {
				// 機器翻譯的字幕軌可能有上百個語言，只記錄一筆。
				add(Decision{Source: SourceYouTubeAuto, Kind: KindPlatformTranslated, State: StateRejected, Reason: ReasonPlatformTranslated})
				break
			}
		}
	}
	return nil, decisions
}
