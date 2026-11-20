// Package transcript 決定用哪個來源取得原始字幕（ADR-0006、SRC 規格）。
// 這裡是不碰外部工具的純邏輯：依 language.caption_priority 從平台字幕軌中挑一條，並記錄每個來源的結果（SRC-R17）。
package transcript

import "strings"

// 字幕軌與規則的種類。
const (
	KindManual             = "manual"
	KindAuto               = "auto"
	KindPlatformTranslated = "platform-translated"
	// 本機影音的字幕軌種類：外掛字幕檔、容器內嵌字幕。
	KindSidecar  = "sidecar"
	KindEmbedded = "embedded"
	// Podcast 的字幕軌種類：feed 中的 <podcast:transcript>。
	KindPodcast = "podcast"
)

// 字幕來源 ID（steps.md 第 3 節）。
const (
	SourceYouTubeManual = "youtube-manual"
	SourceYouTubeAuto   = "youtube-auto"
	// Bilibili：人工上傳的 CC 字幕，與平台產生的 AI 字幕。
	SourceBilibiliCC = "bilibili-cc"
	SourceBilibiliAI = "bilibili-ai"
	// 本機影音：與影音同目錄的外掛字幕檔，與容器內嵌的文字字幕（SRC-R14）。
	SourceLocalSidecar  = "local-sidecar"
	SourceLocalEmbedded = "local-embedded"
	// Podcast：feed 中 <podcast:transcript> 指向的字幕檔（SRC-R14）。
	SourcePodcastTranscript = "podcast-transcript"
)

// 平台名稱（與 items.platform 相同；這裡不引用 source 套件，因為它依賴本套件）。
const (
	PlatformYouTube  = "youtube"
	PlatformBilibili = "bilibili"
	PlatformLocal    = "local"
	PlatformPodcast  = "podcast"
)

// OriginalLang 在 caption_priority 中代表影音的原文語言。
const OriginalLang = "original"

// Rule 是 language.caption_priority 的一項。
type Rule struct {
	Lang string `json:"lang"`
	Kind string `json:"kind"`
}

// Track 是平台提供的字幕軌。
//
// Track 會被 marshal 進 transcript 的快取鍵（沒有 json tag，欄位名稱就是 key）：新增的欄位一律 omitempty，
// 讓 YouTube 與 Bilibili 的快取鍵不變。
type Track struct {
	Lang string
	Kind string
	// Handle 是本機字幕軌的位置：外掛字幕檔的路徑，或內嵌字幕的串流 index；Podcast 字幕是它的網址。
	Handle string `json:",omitempty"`
	// Format 是 Podcast 字幕的格式（vtt、srt、json）。
	Format string `json:",omitempty"`
	// Default 與 Forced 是內嵌字幕的旗標（容器的 disposition）。
	Default bool `json:",omitempty"`
	Forced  bool `json:",omitempty"`
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
	// ReasonUnsupported 是來源不支援這個平台（例如 Supadata 不處理本機檔案），略過，不送出任何資料。
	ReasonUnsupported = "unsupported"
	// ReasonUnreadable 是選中的本機字幕（外掛或內嵌）無法使用：不在允許的目錄、不是一般檔案、超過大小上限、
	// 解析失敗或抽取失敗。這條字幕記為 rejected，改用下一個字幕來源。
	ReasonUnreadable = "unreadable"
)

// PlatformSource 回傳平台上字幕軌種類對應的來源 ID。
// YouTube：機器翻譯的字幕軌由自動字幕機制產生，所以和 auto 同一個來源。
// Bilibili：auto 是平台的 AI 字幕（bilibili-ai），其餘是人工上傳的 CC 字幕（bilibili-cc）。
// 本機：sidecar 是 local-sidecar，embedded 是 local-embedded。
// Podcast：一律是 podcast-transcript。
// 沒有指定平台時視為 YouTube。
func PlatformSource(platform, kind string) string {
	if platform == PlatformPodcast {
		return SourcePodcastTranscript
	}
	if platform == PlatformLocal {
		if kind == KindEmbedded {
			return SourceLocalEmbedded
		}
		return SourceLocalSidecar
	}
	if platform == PlatformBilibili {
		if kind == KindAuto {
			return SourceBilibiliAI
		}
		return SourceBilibiliCC
	}
	if kind == KindManual {
		return SourceYouTubeManual
	}
	return SourceYouTubeAuto
}

// bilibiliAIPrefix 是 yt-dlp 的 BiliBili extractor 在 AI 字幕鍵前面加的前綴（ai-<lang>）。
const bilibiliAIPrefix = "ai-"

// bilibiliDanmaku 是 yt-dlp 一律附上的彈幕（xml）；不是字幕，選軌時忽略。
const bilibiliDanmaku = "danmaku"

// BilibiliTrack 把 yt-dlp 回報的 Bilibili 字幕軌鍵對應到字幕軌：
// ai-<lang> 是 kind auto（來源 bilibili-ai），語言去掉前綴；其他鍵是 kind manual（來源 bilibili-cc）。
// danmaku 與空鍵不是字幕，ok 為 false。
func BilibiliTrack(key string) (Track, bool) {
	if key == "" || key == bilibiliDanmaku {
		return Track{}, false
	}
	if lang, ok := strings.CutPrefix(key, bilibiliAIPrefix); ok {
		if lang == "" {
			return Track{}, false
		}
		return Track{Lang: lang, Kind: KindAuto}, true
	}
	return Track{Lang: key, Kind: KindManual}, true
}

// BilibiliKey 是 BilibiliTrack 的反向：回傳字幕軌在 yt-dlp 的原始鍵（下載時用）。
func BilibiliKey(t Track) string {
	if t.Kind == KindAuto {
		return bilibiliAIPrefix + t.Lang
	}
	return t.Lang
}

// BilibiliTracks 把一組原始鍵轉成字幕軌，略過彈幕；順序與輸入相同。
func BilibiliTracks(keys []string) []Track {
	out := make([]Track, 0, len(keys))
	for _, k := range keys {
		if t, ok := BilibiliTrack(k); ok {
			out = append(out, t)
		}
	}
	return out
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
	return SelectFor(PlatformYouTube, tracks, originalLang, rules, allowTranslated)
}

// SelectFor 與 Select 相同，但決策中的來源 ID 依 platform 決定（見 PlatformSource）。
func SelectFor(platform string, tracks []Track, originalLang string, rules []Rule, allowTranslated bool) (chosen *Track, decisions []Decision) {
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
				add(Decision{Source: PlatformSource(platform, rule.Kind), Lang: OriginalLang, Kind: rule.Kind, State: StateUnavailable, Reason: ReasonOriginalLangUnknown})
				continue
			}
			lang = originalLang
		}
		kinds := []string{rule.Kind}
		if rule.Kind == KindAuto && allowTranslated {
			kinds = append(kinds, KindPlatformTranslated)
		}
		if t, ok := best(tracks, lang, kinds...); ok {
			add(Decision{Source: PlatformSource(platform, t.Kind), Lang: NormalizeLang(t.Lang), Kind: t.Kind, State: StateUsed})
			return &t, decisions
		}
		add(Decision{Source: PlatformSource(platform, rule.Kind), Lang: lang, Kind: rule.Kind, State: StateUnavailable, Reason: ReasonNoTrack})
	}
	if !allowTranslated {
		for _, t := range tracks {
			if t.Kind == KindPlatformTranslated {
				// 機器翻譯的字幕軌可能有上百個語言，只記錄一筆。
				add(Decision{Source: PlatformSource(platform, KindAuto), Kind: KindPlatformTranslated, State: StateRejected, Reason: ReasonPlatformTranslated})
				break
			}
		}
	}
	return nil, decisions
}
