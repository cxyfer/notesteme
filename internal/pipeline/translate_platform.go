package pipeline

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// TargetPlatform：平台有原文與目標語言的人工字幕軌，直接採用目標語言的字幕軌（TRN-R11）。
// 只在 plan 時由需要 LLM 的目標（translate、localize）改成這一種；PlanTargets 不會產生它。
const TargetPlatform TargetKind = "platform"

// executorPlatform 是採用平台字幕軌時，譯文來源紀錄的 executor（TRN-R11）。stage run 的 executor 仍是 api。
const executorPlatform = "platform"

// reasonNoOverlap 是平台字幕軌沒有任何一句與這個 cue 時間重疊時的退回原因（保留原文）。
const reasonNoOverlap = "no_overlap"

// trnPlatformTracks 檢查需要 LLM 的目標能不能改用平台的人工字幕軌（TRN-R11）：只在原文字幕來自同一平台的人工字幕軌時，
// 重新列出平台的字幕軌（一次 probe），有目標語言的人工字幕軌就改成 TargetPlatform。probe 失敗時照常交給 LLM（記 log）。
func (p *Pipeline) trnPlatformTracks(ctx context.Context, it store.Item, b *trnBase, targets []trnTarget) error {
	if it.Platform != source.PlatformYouTube && it.Platform != source.PlatformBilibili {
		return nil
	}
	need := false
	for _, t := range targets {
		need = need || t.usesLLM()
	}
	if !need || !p.manualSource(ctx, it) {
		return nil
	}
	ad, err := p.adapterFor(it.Platform)
	if err != nil {
		return nil
	}
	info, err := ad.probe(ctx, it)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.log.Warn("列出平台字幕軌失敗，改用 LLM 翻譯", "item", it.ID, "error", err)
		return nil
	}
	for i := range targets {
		if !targets[i].usesLLM() {
			continue
		}
		for _, tr := range info.Tracks {
			if tr.Kind == transcript.KindManual && trackLangMatches(tr.Lang, targets[i].Lang) {
				track := tr
				targets[i].Kind, targets[i].Track, targets[i].Table = TargetPlatform, &track, ""
				break
			}
		}
	}
	return nil
}

// manualSource 回報影音現行的原始字幕是不是平台的人工字幕軌（例如 youtube-manual）。
func (p *Pipeline) manualSource(ctx context.Context, it store.Item) bool {
	arts, err := p.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return false
	}
	want := transcript.PlatformSource(it.Platform, transcript.KindManual)
	for i := len(arts) - 1; i >= 0; i-- {
		if arts[i].Variant != string(subtitle.VariantSource) {
			continue
		}
		var prov struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(arts[i].Provenance, &prov)
		return prov.Source == want
	}
	return false
}

// trackLangMatches 回報字幕軌的語言是不是目標語言：標記相同，或兩者都是中文、字形與地區相同（zh-TW 與 zh-Hant-TW）。
func trackLangMatches(track, target string) bool {
	track = transcript.NormalizeLang(track)
	if langtag.Equal(track, target) {
		return true
	}
	a, b := langtag.Parse(track), langtag.Parse(target)
	return langtag.IsChinese(track) && langtag.IsChinese(target) && a.Region == b.Region && a.Ext == b.Ext &&
		langtag.ChineseScript(track) != "" && langtag.ChineseScript(track) == langtag.ChineseScript(target)
}

// trnPlatform 下載目標語言的平台字幕軌，依時間重疊對齊到原文的 cue（時間沿用原文）。
// 沒有任何字幕軌的句子與它重疊的 cue 保留原文，記在 fallbacks（no_overlap）。
func (p *Pipeline) trnPlatform(ctx context.Context, it store.Item, b *trnBase, tg trnTarget) (*store.NewArtifact, bool, error) {
	if tg.Track == nil {
		return nil, false, errPermanent("平台字幕軌的記錄不完整")
	}
	ad, err := p.adapterFor(it.Platform)
	if err != nil {
		return nil, false, err
	}
	track := *tg.Track
	got, err := p.fetchCues(ctx, ad, it, &track)
	if err != nil {
		return nil, false, err
	}
	if len(got) == 0 {
		return nil, false, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "平台字幕軌是空的 " + track.Lang,
			Message: "平台的 " + track.Lang + " 字幕軌是空的。"}
	}
	sep := " "
	switch langtag.Parse(tg.Lang).Lang {
	case "zh", "ja", "ko":
		sep = ""
	}
	texts := alignByOverlap(b.tr.Cues, got, sep)
	cues := make([]subtitle.Cue, len(b.tr.Cues))
	fallbacks := []trnRecord{}
	for k, c := range b.tr.Cues {
		text := texts[k]
		if text == "" {
			text = c.Text
			fallbacks = append(fallbacks, trnRecord{Cue: k + 1, Reason: reasonNoOverlap})
		}
		cues[k] = subtitle.Cue{Index: c.Index, StartMS: c.StartMS, EndMS: c.EndMS, Text: text, Timing: c.Timing, Speaker: c.Speaker}
	}
	versions, err := ad.toolVersions(ctx)
	if err != nil {
		return nil, false, err
	}
	prov, err := p.provenance(map[string]any{
		"stage": StageTranslate, "impl_version": p.implVersion(StageTranslate), "executor": executorPlatform,
		"source":      transcript.PlatformSource(it.Platform, transcript.KindManual),
		"track":       map[string]string{"lang": transcript.NormalizeLang(track.Lang), "kind": track.Kind},
		"source_lang": b.sourceLang, "target": tg.Lang, "fallbacks": fallbacks,
		"derived_from": derivedFrom(b.input),
	}, versions)
	if err != nil {
		return nil, false, err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: tg.Lang, Variant: subtitle.VariantTranslated, Speakers: b.tr.Speakers, Cues: cues},
		[]string{b.input.ID}, prov)
	return art, len(fallbacks) > 0, err
}

// maxAlignScan 是一句平台字幕最多比對的原文 cue 數：不可信的字幕軌中跨越整部影片的一句不會讓對齊變成 O(n·m)。
const maxAlignScan = 64

// alignByOverlap 把 tgt 的每一句歸到時間重疊最多的原文 cue（同樣多時取較早的），同一個 cue 的多句依時間順序以 sep 串接。
// orig 的時間要單調（字幕產出都是）；回傳每個原文 cue 的文字，沒有任何重疊的是空字串。
func alignByOverlap(orig, tgt []subtitle.Cue, sep string) []string {
	parts := make([][]string, len(orig))
	sorted := make([]subtitle.Cue, len(tgt))
	copy(sorted, tgt)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartMS < sorted[j].StartMS })
	for _, t := range sorted {
		text := strings.TrimSpace(t.Text)
		if t.EndMS <= t.StartMS || text == "" {
			continue
		}
		i := sort.Search(len(orig), func(i int) bool { return orig[i].EndMS > t.StartMS })
		best, bestOv := -1, int64(0)
		for j := i; j < len(orig) && j < i+maxAlignScan && orig[j].StartMS < t.EndMS; j++ {
			ov := min(orig[j].EndMS, t.EndMS) - max(orig[j].StartMS, t.StartMS)
			if int64(ov) > bestOv {
				best, bestOv = j, int64(ov)
			}
		}
		if best >= 0 {
			parts[best] = append(parts[best], text)
		}
	}
	out := make([]string, len(orig))
	for k, ps := range parts {
		out[k] = strings.Join(ps, sep)
	}
	return out
}
