package transcript

// ReasonLanguageMismatch 是 Podcast 的字幕軌語言都和影音的原文語言不同（source_probes.reason）。
const ReasonLanguageMismatch = "language_mismatch"

// formatRank 是 Podcast 字幕格式的優先順序：vtt 先於 srt 先於 json；其他格式不會出現（見 podcast.TranscriptFormat）。
func formatRank(format string) int {
	switch format {
	case "vtt":
		return 0
	case "srt":
		return 1
	case "json":
		return 2
	}
	return 3
}

// SelectPodcast 為 Podcast 單集選字幕軌（SRC-R14）。tracks 是 feed 中的 <podcast:transcript>，
// 軌的語言是 language 屬性，省略時由呼叫端代入 feed 的語言；仍然未知時為空字串。
//
// 依 rules 的語言順序挑選（rules 的 kind 不適用於 Podcast，只看語言；original 以 originalLang 代入，
// 不知道原文語言時該項略過）。語言都不符合時，改選語言未知、或主要語言與原文相同的軌。
// 同一層之內依格式 vtt > srt > json，再依出現順序。tracks 中 Kind 不是 podcast 的軌被忽略。
//
// decisions 只有一筆：採用為 used；沒有字幕軌為 unavailable（no_track）；有字幕軌但語言都不符為 rejected（language_mismatch）。
func SelectPodcast(tracks []Track, originalLang string, rules []Rule) (chosen *Track, decisions []Decision) {
	var cands []Track
	for _, t := range tracks {
		if t.Kind == KindPodcast {
			cands = append(cands, t)
		}
	}
	decide := func(t Track) (*Track, []Decision) {
		return &t, []Decision{{Source: SourcePodcastTranscript, Lang: NormalizeLang(t.Lang), Kind: KindPodcast, State: StateUsed}}
	}
	if len(cands) == 0 {
		return nil, []Decision{{Source: SourcePodcastTranscript, Kind: KindPodcast, State: StateUnavailable, Reason: ReasonNoTrack}}
	}

	for _, rule := range rules {
		lang := rule.Lang
		if lang == OriginalLang {
			if originalLang == "" {
				continue
			}
			lang = originalLang
		}
		var (
			found Track
			rank  = rankNone
		)
		for _, t := range cands {
			r := rankLang(lang, t.Lang)
			if r == rankNone {
				continue
			}
			if r > rank || (r == rank && formatRank(t.Format) < formatRank(found.Format)) {
				found, rank = t, r
			}
		}
		if rank > rankNone {
			return decide(found)
		}
	}

	// 語言都不符合 caption_priority：語言未知的軌，或與原文同主要語言的軌，仍然可以用；
	// 原文語言也未知時，任何一軌都可以用（呼叫端之後用它的語言推斷原文語言）。
	want := PrimaryLang(originalLang)
	var (
		found Track
		ok    bool
	)
	for _, t := range cands {
		have := PrimaryLang(t.Lang)
		if have != "" && want != "" && have != want {
			continue
		}
		if !ok || formatRank(t.Format) < formatRank(found.Format) {
			found, ok = t, true
		}
	}
	if ok {
		return decide(found)
	}
	return nil, []Decision{{Source: SourcePodcastTranscript, Lang: NormalizeLang(cands[0].Lang), Kind: KindPodcast, State: StateRejected, Reason: ReasonLanguageMismatch}}
}
