package transcript

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// SelectLocal 為本機影音選字幕軌（SRC-R14）：先看外掛字幕檔（sidecar），沒有才看容器內嵌的文字字幕（embedded）。
//
// 同一種之內，優先 rules 中的語言（rules 的 kind 不適用於本機，只看語言；original 以 originalLang 代入，
// 不知道原文語言時該項略過）；沒有符合的語言時，依固定順序取第一條，不會因為語言不在清單中就放棄：
//   - sidecar：.srt 先於 .vtt，再依檔名。
//   - embedded：default 的先，非 forced 的先，再依串流 index。
//
// decisions 記錄被評估的來源：某一種完全沒有字幕軌時記為 unavailable（no_track），採用的為 used；
// 排在採用的來源之後的不記錄。tracks 中 Kind 不是 sidecar 或 embedded 的軌被忽略。
func SelectLocal(tracks []Track, originalLang string, rules []Rule) (chosen *Track, decisions []Decision) {
	groups := []struct {
		kind, source string
		tracks       []Track
	}{
		{KindSidecar, SourceLocalSidecar, nil},
		{KindEmbedded, SourceLocalEmbedded, nil},
	}
	for _, t := range tracks {
		for i := range groups {
			if t.Kind == groups[i].kind {
				groups[i].tracks = append(groups[i].tracks, t)
			}
		}
	}
	sortSidecars(groups[0].tracks)
	sortEmbedded(groups[1].tracks)

	for _, g := range groups {
		if len(g.tracks) == 0 {
			decisions = append(decisions, Decision{Source: g.source, Kind: g.kind, State: StateUnavailable, Reason: ReasonNoTrack})
			continue
		}
		t := pickLocal(g.tracks, originalLang, rules)
		decisions = append(decisions, Decision{Source: g.source, Lang: NormalizeLang(t.Lang), Kind: g.kind, State: StateUsed})
		return &t, decisions
	}
	return nil, decisions
}

// pickLocal 在已排序的 tracks 中依 rules 的語言挑一條；都不符合時取第一條。
func pickLocal(tracks []Track, originalLang string, rules []Rule) Track {
	for _, rule := range rules {
		lang := rule.Lang
		if lang == OriginalLang {
			if originalLang == "" {
				continue
			}
			lang = originalLang
		}
		found, rank := Track{}, rankNone
		for _, t := range tracks {
			if r := rankLang(lang, t.Lang); r > rank {
				found, rank = t, r
			}
		}
		if rank > rankNone {
			return found
		}
	}
	return tracks[0]
}

// sortSidecars 依固定順序排序：.srt 先於 .vtt，再依檔名。
func sortSidecars(ts []Track) {
	rank := func(t Track) int {
		if strings.EqualFold(filepath.Ext(t.Handle), ".srt") {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(ts, func(a, b Track) int {
		return cmp.Or(
			cmp.Compare(rank(a), rank(b)),
			strings.Compare(filepath.Base(a.Handle), filepath.Base(b.Handle)),
			strings.Compare(a.Handle, b.Handle),
		)
	})
}

// sortEmbedded 依固定順序排序：default 的先，非 forced 的先，再依串流 index。
func sortEmbedded(ts []Track) {
	idx := func(t Track) int {
		n, err := strconv.Atoi(t.Handle)
		if err != nil {
			return int(^uint(0) >> 1)
		}
		return n
	}
	flag := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(ts, func(a, b Track) int {
		return cmp.Or(
			cmp.Compare(flag(b.Default), flag(a.Default)),
			cmp.Compare(flag(a.Forced), flag(b.Forced)),
			cmp.Compare(idx(a), idx(b)),
		)
	})
}
