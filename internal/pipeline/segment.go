package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// segment 步驟（SEG）：LLM 只決定斷點（llm.SegmentSpec），時間一律由這裡依來源計算（ADR-0003）。

// timing.split_timing、timing.split_timing_audio 與 stages.segment.on_manual 的值。
const (
	SplitForbid     = "forbid"
	SplitWhenNeeded = "when_needed"
	SplitFree       = "free"

	SplitAudioAuto  = "auto"
	SplitAudioNever = "never"

	OnManualSkip = "skip"
	OnManualRun  = "run"
)

// maxForcedPieces 是 SEG-R10 強制切分時一句最多切成幾段。
const maxForcedPieces = 20

// TimingSettings 是 timing 區段（SEG-R7、R9）；沒有設定時是 when_needed 與 auto。
type TimingSettings struct {
	SplitTiming      string `json:"split_timing"`
	SplitTimingAudio string `json:"split_timing_audio"`
}

// Timing 取出 timing 區段，沒有設定的欄位用預設值。
func (e Effective) Timing() (TimingSettings, error) {
	t := TimingSettings{SplitTiming: SplitWhenNeeded, SplitTimingAudio: SplitAudioAuto}
	m, _ := e["timing"].(map[string]any)
	if v, ok := m["split_timing"].(string); ok && v != "" {
		t.SplitTiming = v
	}
	if v, ok := m["split_timing_audio"].(string); ok && v != "" {
		t.SplitTimingAudio = v
	}
	if !slices.Contains([]string{SplitForbid, SplitWhenNeeded, SplitFree}, t.SplitTiming) {
		return t, invalidf("timing.split_timing 不支援 %q。", t.SplitTiming)
	}
	if !slices.Contains([]string{SplitAudioAuto, SplitAudioNever}, t.SplitTimingAudio) {
		return t, invalidf("timing.split_timing_audio 不支援 %q。", t.SplitTimingAudio)
	}
	return t, nil
}

// SubtitleRules 取出 subtitle_rules.<script>，沒有設定的欄位用 plan 第 8.5 節的預設值（subtitle.DefaultRules）。
func (e Effective) SubtitleRules(s subtitle.Script) (subtitle.Rules, error) {
	r := subtitle.DefaultRules(s)
	all, _ := e["subtitle_rules"].(map[string]any)
	m, _ := all[string(s)].(map[string]any)
	ints := map[string]*int{"max_chars_per_line": &r.MaxCharsPerLine, "max_lines": &r.MaxLines}
	for k, dst := range ints {
		switch v := m[k].(type) {
		case nil:
		case float64:
			if v < 1 || v != math.Trunc(v) || v > 10000 {
				return r, invalidf("subtitle_rules.%s.%s 必須是大於 0 的整數。", s, k)
			}
			*dst = int(v)
		case int:
			*dst = v
		default:
			return r, invalidf("subtitle_rules.%s.%s 必須是大於 0 的整數。", s, k)
		}
	}
	durs := map[string]*int64{"min_duration": &r.MinDurationMS, "max_duration": &r.MaxDurationMS}
	for k, dst := range durs {
		v, ok := m[k].(string)
		if !ok {
			continue
		}
		d, err := auth.ParseTokenDuration(v)
		if err != nil {
			return r, invalidf("subtitle_rules.%s.%s 必須是有效期間字串，例如 7s。", s, k)
		}
		*dst = d.Milliseconds()
	}
	return r, nil
}

// segmentOnManual 是 stages.segment.on_manual（SEG-R1）：預設 skip。
func (e Effective) segmentOnManual() string {
	all, _ := e.stages()
	entry, _ := all[StageSegment].(map[string]any)
	if v, _ := entry["on_manual"].(string); v == OnManualRun {
		return OnManualRun
	}
	return OnManualSkip
}

// segmentParams 是 segment 進 params_sha256 的設定：時間策略與兩種文字系統的字幕規則。
func segmentParams(eff Effective) (any, error) {
	t, err := eff.Timing()
	if err != nil {
		return nil, err
	}
	rules := map[string]subtitle.Rules{}
	for _, s := range []subtitle.Script{subtitle.ScriptCJK, subtitle.ScriptLatin} {
		if rules[string(s)], err = eff.SubtitleRules(s); err != nil {
			return nil, err
		}
	}
	return map[string]any{"timing": t, "subtitle_rules": rules}, nil
}

// manualCaptions 回報影音採用的字幕來源是不是人工字幕（kind manual：YouTube 人工字幕、Bilibili CC 字幕）。
func manualCaptions(ctx context.Context, r *store.Repo, itemID string) (bool, error) {
	probes, err := r.LatestProbes(ctx, itemID)
	if err != nil {
		return false, err
	}
	for _, pr := range probes {
		if pr.State == transcript.StateUsed {
			return pr.Kind == transcript.KindManual, nil
		}
	}
	return false, nil
}

// planContext 是前進與重跑解析步驟時用的 StageContext：stageContext（translate 的原文語言），
// 再加上 segment 要的「採用的是人工字幕」（SEG-R1）。
func (p *Pipeline) planContext(ctx context.Context, r *store.Repo, it store.Item, from string) (StageContext, error) {
	sc, err := p.stageContext(ctx, r, it, from)
	if err != nil {
		return sc, err
	}
	sc.ManualCaptions, err = manualCaptions(ctx, r, it.ID)
	return sc, err
}

// segmentWarning 是產出來源紀錄 warnings[] 的一項（SEG-R10）：斷句後仍違反的字幕規則，或時間只能退而求其次的原因。
type segmentWarning struct {
	Cue    int    `json:"cue,omitempty"`
	Rule   string `json:"rule"`
	Value  int64  `json:"value,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// assembleSegment 是 segment 的 llmStage.assemble：合併各 chunk 的斷點、依來源計算時間、套用字幕規則。
func (p *Pipeline) assembleSegment(ctx context.Context, it store.Item, job *llmJob, outputs []json.RawMessage) (llmAssembled, error) {
	marks, err := mergeSegmentMarks(job, outputs)
	if err != nil {
		return llmAssembled{}, errPermanent(err.Error())
	}
	ts, err := job.eff.Timing()
	if err != nil {
		return llmAssembled{}, err
	}
	lang := job.tr.Lang
	if lang == "" {
		lang = it.OriginalLang
	}
	texts := make([]string, len(job.tr.Cues))
	var untilMS int64
	for k, c := range job.tr.Cues {
		texts[k] = c.Text
		untilMS = max(untilMS, c.EndMS)
	}
	rules, err := job.eff.SubtitleRules(subtitle.ScriptOf(lang, texts...))
	if err != nil {
		return llmAssembled{}, err
	}
	finder := newSilenceFinder(p, it, ts, untilMS)
	defer finder.close()
	b := &segBuilder{ctx: ctx, cues: job.tr.Cues, split: ts.SplitTiming, rules: rules, silences: finder.get}
	cues, err := b.build(marks)
	if err != nil {
		return llmAssembled{}, err
	}
	// SEG-R4 的不變式：產出的文字一律從輸入切出，去掉空白與標點後必須與輸入相同。不成立是程式錯誤，不能寫出改過的字幕。
	if !sameSegmentKeys(job.tr.Cues, cues) {
		return llmAssembled{}, errPermanent("斷句後的文字與輸入不同")
	}
	warnings := []segmentWarning{}
	if finder.warn != "" {
		warnings = append(warnings, segmentWarning{Rule: "silence_unavailable", Detail: finder.warn})
	}
	for _, c := range cues {
		for _, v := range rules.Check(c) {
			warnings = append(warnings, segmentWarning{Cue: c.Index, Rule: v.Rule, Value: v.Value, Limit: v.Limit})
		}
	}
	out := subtitle.Transcript{Lang: job.tr.Lang, Speakers: job.tr.Speakers, Cues: cues}
	return llmAssembled{
		tr:   out,
		prov: map[string]any{"warnings": warnings, "timing": ts},
		// 音訊或 ffmpeg 無法使用而改用插值時，不寫入步驟層的快取：之後的重跑要重新找停頓點。
		noCache: finder.warn != "",
	}, nil
}

// mergeSegmentMarks 依 chunk 順序合併斷點：每句的結果取自第一個包含它的 chunk（重疊區以前一個 chunk 為準，LLM-R1）。
// 退回的 chunk（outputs[k] 是 nil）保留原本的切法（SEG-R3）。
func mergeSegmentMarks(job *llmJob, outputs []json.RawMessage) ([]llm.CueMarks, error) {
	if len(outputs) != len(job.chunks) {
		return nil, fmt.Errorf("chunk 數 %d 與輸出數 %d 不同", len(job.chunks), len(outputs))
	}
	out := make([]llm.CueMarks, 0, len(job.cues))
	next := 1
	for k, c := range job.chunks {
		in := job.chunkInput(c)
		marks := llm.KeepMarks(in.Cues)
		if outputs[k] != nil {
			var verr *llm.ValidationError
			if marks, verr = llm.SegmentMarks(in, outputs[k]); verr != nil {
				return nil, fmt.Errorf("chunk %d 的輸出無法對回：%v", c.Index, verr)
			}
		}
		for j, m := range marks {
			n := c.From + j
			if n < next {
				continue
			}
			if n != next || m.I != n {
				return nil, fmt.Errorf("chunk %d 從第 %d 句開始，前面缺少第 %d 句", c.Index, n, next)
			}
			out = append(out, m)
			next++
		}
	}
	if len(out) != len(job.cues) {
		return nil, fmt.Errorf("合併後有 %d 句，輸入有 %d 句", len(out), len(job.cues))
	}
	return out, nil
}

// timingRank 是合併時間來源時的強弱：數字越大越不可靠，一句字幕取它各個邊界中最不可靠的。
var timingRank = map[subtitle.TimingOrigin]int{
	subtitle.TimingSource: 0, subtitle.TimingMerged: 1, subtitle.TimingSilence: 2, subtitle.TimingInterpolated: 3, subtitle.TimingModel: 4,
}

func weaker(a, b subtitle.TimingOrigin) subtitle.TimingOrigin {
	if timingRank[b] > timingRank[a] {
		return b
	}
	return a
}

// segCut 是一句字幕內的切點：前一段結束在 leftEnd，後一段從 rightStart 開始。
type segCut struct {
	at                  int // 後一段在句子文字中開始的字元位置
	leftEnd, rightStart int64
	origin              subtitle.TimingOrigin
}

// segPiece 是一句輸入字幕的一段：[from, to) 是字元範圍，時間與兩端的時間來源。
type segPiece struct {
	cue                    int // 輸入字幕的索引（從 0 起算）
	from, to               int
	start, end             int64
	startOrigin, endOrigin subtitle.TimingOrigin
}

// segBuilder 把斷點套用到輸入字幕（SEG-R5～R10）。
type segBuilder struct {
	ctx   context.Context
	cues  []subtitle.Cue
	split string
	rules subtitle.Rules
	// silences 回傳整段音訊的靜音；nil、或回傳 nil 代表不能用 silencedetect（直接插值，SEG-R9）。
	silences func(ctx context.Context) ([]media.Silence, error)
}

// build 依斷點組出斷句後的字幕。
func (b *segBuilder) build(marks []llm.CueMarks) ([]subtitle.Cue, error) {
	var segs [][]segPiece
	for j := range b.cues {
		cuts, err := b.timeCuts(j, marks[j].Cuts, b.split)
		if err != nil {
			return nil, err
		}
		for k, piece := range b.pieces(j, cuts) {
			if k == 0 && j > 0 && marks[j].Join && len(segs) > 0 {
				segs[len(segs)-1] = append(segs[len(segs)-1], piece)
				continue
			}
			segs = append(segs, []segPiece{piece})
		}
	}
	// SEG-R10：forbid 不切分；when_needed 與 free 時超過上限的字幕一定切分。
	if b.split != SplitForbid {
		var err error
		if segs, err = b.enforce(segs); err != nil {
			return nil, err
		}
	}
	out := make([]subtitle.Cue, 0, len(segs))
	for _, s := range segs {
		c := b.cueOf(s)
		c.Index = len(out) + 1
		out = append(out, c)
	}
	return out, nil
}

// enforce 切開超過長度或時長上限的字幕（SEG-R10）：
//   - 由多句合併而成的，在原本的句子邊界切回去（時間仍來自來源）；
//   - 沒有被切開過的一整句，由程式在字詞之間切分，時間依 word 級時間、停頓點或插值；
//   - LLM 在句內切出的片段仍超過上限時不再切分，只在 warnings 記錄（避免在插值的時間上再插值）。
func (b *segBuilder) enforce(segs [][]segPiece) ([][]segPiece, error) {
	var out [][]segPiece
	for _, s := range segs {
		if !b.rules.TooLong(b.cueOf(s)) {
			out = append(out, s)
			continue
		}
		groups := [][]segPiece{s}
		if len(s) > 1 {
			groups = b.regroup(s)
		}
		for _, g := range groups {
			if len(g) != 1 || !b.whole(g[0]) || !b.rules.TooLong(b.cueOf(g)) {
				out = append(out, g)
				continue
			}
			split, err := b.forceSplit(g[0].cue)
			if err != nil {
				return nil, err
			}
			out = append(out, split...)
		}
	}
	return out, nil
}

// regroup 把合併後超過上限的片段，依原本的邊界重新貪婪地分組：加入下一段會超過上限時另起一組。
// 只累積目前這一組的文字與時間（一組不會比上限長太多），整體是線性時間：LLM 把很長的 chunk 合併成一句時也不會變慢。
func (b *segBuilder) regroup(s []segPiece) [][]segPiece {
	var out [][]segPiece
	cur := []segPiece{s[0]}
	text := b.pieceText(s[0])
	for _, piece := range s[1:] {
		part := b.pieceText(piece)
		cand := joinText(text, part)
		if b.rules.TooLong(subtitle.Cue{StartMS: cur[0].start, EndMS: piece.end, Text: cand}) {
			out = append(out, cur)
			cur, text = []segPiece{piece}, part
			continue
		}
		cur, text = append(cur, piece), cand
	}
	return append(out, cur)
}

// pieceText 是片段的文字（去掉前後空白）。
func (b *segBuilder) pieceText(p segPiece) string {
	return strings.TrimSpace(string([]rune(b.cues[p.cue].Text)[p.from:p.to]))
}

// joinText 接起兩段文字：CJK 之間不加空白（needsSpace）；空的一邊直接略過。
func joinText(left, right string) string {
	switch {
	case right == "":
		return left
	case left == "":
		return right
	case needsSpace(left, right):
		return left + " " + right
	}
	return left + right
}

// forceSplit 由程式切開一整句（SEG-R10）：在字詞之間（CJK 是字元之間）找切點，優先在標點之後，
// 取最少、而且估計每段都符合上限的段數。
func (b *segBuilder) forceSplit(j int) ([][]segPiece, error) {
	c := b.cues[j]
	text := []rune(c.Text)
	cands := splitCandidates(text)
	if len(cands) == 0 {
		return [][]segPiece{b.pieces(j, nil)}, nil
	}
	weights := nonSpacePrefix(text)
	var at []int
	for n := 2; n <= min(len(cands)+1, maxForcedPieces); n++ {
		at = pickCuts(cands, text, weights, n)
		if b.estimateFits(c, text, weights, at) {
			break
		}
	}
	cuts, err := b.timeCuts(j, at, SplitFree)
	if err != nil {
		return nil, err
	}
	var out [][]segPiece
	for _, piece := range b.pieces(j, cuts) {
		out = append(out, []segPiece{piece})
	}
	return out, nil
}

// estimateFits 以字數比例估計切開後每一段的時長，檢查每段都不超過上限。
func (b *segBuilder) estimateFits(c subtitle.Cue, text []rune, weights []int, at []int) bool {
	bounds := append(append([]int{0}, at...), len(text))
	total := max(weights[len(text)], 1)
	for k := 0; k+1 < len(bounds); k++ {
		from, to := bounds[k], bounds[k+1]
		d := (c.EndMS - c.StartMS) * int64(weights[to]-weights[from]) / int64(total)
		piece := subtitle.Cue{StartMS: 0, EndMS: d, Text: strings.TrimSpace(string(text[from:to]))}
		if b.rules.TooLong(piece) {
			return false
		}
	}
	return true
}

// splitCandidates 回傳句子中可以切開的位置（後一段開始的字元位置）：空白的開頭，或 CJK 字元之前（前面不是空白）。
// 兩邊都要有字。
func splitCandidates(text []rune) []int {
	var out []int
	firstKey, lastKey := -1, -1
	for i, r := range text {
		if llm.IsSegmentKey(r) {
			if firstKey < 0 {
				firstKey = i
			}
			lastKey = i
		}
	}
	for i := 1; i < len(text); i++ {
		if i <= firstKey || i > lastKey {
			continue
		}
		r, prev := text[i], text[i-1]
		switch {
		case unicode.IsSpace(r) && !unicode.IsSpace(prev):
			out = append(out, i)
		case llm.IsSegmentKey(r) && !unicode.IsSpace(prev) && (subtitle.IsCJK(r) || subtitle.IsCJK(lastKeyBefore(text, i))):
			out = append(out, i)
		}
	}
	return out
}

func lastKeyBefore(text []rune, i int) rune {
	for k := i - 1; k >= 0; k-- {
		if llm.IsSegmentKey(text[k]) {
			return text[k]
		}
	}
	return 0
}

// pickCuts 從候選位置選 n-1 個切點：最接近等分的位置（依非空白字元數），附近有標點結尾的候選時優先。
func pickCuts(cands []int, text []rune, weights []int, n int) []int {
	total := weights[len(text)]
	var out []int
	last := -1 // 已選的候選索引
	for k := 1; k < n; k++ {
		target := total * k / n
		window := max(total/(2*n), 1)
		best, bestScore := -1, 0
		for ci := last + 1; ci < len(cands)-(n-1-k); ci++ {
			d := weights[cands[ci]] - target
			if d < 0 {
				d = -d
			}
			score := d
			if endsWithPunct(text, cands[ci]) && d <= window {
				score -= window // 標點之後優先
			}
			if best < 0 || score < bestScore {
				best, bestScore = ci, score
			}
		}
		if best < 0 {
			break
		}
		out = append(out, cands[best])
		last = best
	}
	return out
}

// endsWithPunct 回報切點 at 之前的最後一個非空白字元是不是標點。
func endsWithPunct(text []rune, at int) bool {
	for k := at - 1; k >= 0; k-- {
		if !unicode.IsSpace(text[k]) {
			return unicode.IsPunct(text[k])
		}
	}
	return false
}

// nonSpacePrefix 回傳 w[i] = text[:i] 中非空白字元的數目（插值與等分的權重：依字數比例）。
func nonSpacePrefix(text []rune) []int {
	w := make([]int, len(text)+1)
	for i, r := range text {
		w[i+1] = w[i]
		if !unicode.IsSpace(r) {
			w[i+1]++
		}
	}
	return w
}

// keyPrefix 回傳 k[i] = text[:i] 中比對字元（llm.IsSegmentKey）的數目。
func keyPrefix(text []rune) []int {
	k := make([]int, len(text)+1)
	for i, r := range text {
		k[i+1] = k[i]
		if llm.IsSegmentKey(r) {
			k[i+1]++
		}
	}
	return k
}

// wordStarts 回傳每個字詞在句子比對字元中的起點；字詞的文字與句子對不上時回傳 nil（當成沒有 word 級時間）。
func wordStarts(c subtitle.Cue) []int {
	if len(c.Words) == 0 {
		return nil
	}
	var want []rune
	for _, r := range c.Text {
		if llm.IsSegmentKey(r) {
			want = append(want, r)
		}
	}
	starts := make([]int, len(c.Words))
	n := 0
	for w, word := range c.Words {
		starts[w] = n
		for _, r := range word.Text {
			if !llm.IsSegmentKey(r) {
				continue
			}
			if n >= len(want) || want[n] != r {
				return nil
			}
			n++
		}
	}
	if n != len(want) {
		return nil
	}
	return starts
}

// timeCuts 決定一句字幕內每個切點的時間（SEG-R6～R9）；policy 不允許的切點略過。
//   - 有 word 級時間、而且切點剛好在字詞之間：前一段結束在前一個字詞的 end，後一段從後一個字詞的 start 開始（source）。
//   - 否則依 policy：forbid 略過；when_needed 只在這句超過上限時切；free 一律切。
//     要切的先在這句的範圍內找停頓點（silence），找不到才依字數比例插值（interpolated）。
func (b *segBuilder) timeCuts(j int, at []int, policy string) ([]segCut, error) {
	if len(at) == 0 {
		return nil, nil
	}
	c := b.cues[j]
	text := []rune(c.Text)
	keys, weights := keyPrefix(text), nonSpacePrefix(text)
	starts := wordStarts(c)
	var sils []media.Silence
	silLoaded := false
	var out []segCut
	lower := c.StartMS
	for _, a := range at {
		if a <= 0 || a >= len(text) {
			continue
		}
		if w := wordAt(starts, keys[a]); w > 0 {
			left, right := c.Words[w-1].EndMS, c.Words[w].StartMS
			if left >= lower && right >= left && right < c.EndMS {
				out = append(out, segCut{at: a, leftEnd: left, rightStart: right, origin: subtitle.TimingSource})
				lower = right
				continue
			}
		}
		switch {
		case policy == SplitForbid:
			continue
		case policy == SplitWhenNeeded && !b.rules.TooLong(c):
			continue
		}
		est := c.StartMS + (c.EndMS-c.StartMS)*int64(weights[a])/int64(max(weights[len(text)], 1))
		if !silLoaded && b.silences != nil {
			var err error
			if sils, err = b.silences(b.ctx); err != nil {
				return nil, err
			}
			silLoaded = true
		}
		if s, ok := nearestSilence(sils, lower, c.EndMS, est); ok {
			out = append(out, segCut{at: a, leftEnd: s[0], rightStart: s[1], origin: subtitle.TimingSilence})
			lower = s[1]
			continue
		}
		t := min(max(est, lower), c.EndMS)
		out = append(out, segCut{at: a, leftEnd: t, rightStart: t, origin: subtitle.TimingInterpolated})
		lower = t
	}
	return out, nil
}

// wordAt 回傳在比對字元 key 開始的字詞索引；沒有字詞剛好從那裡開始時回傳 -1。
func wordAt(starts []int, key int) int {
	for w, s := range starts {
		if s == key && (w == 0 || starts[w-1] < key) {
			return w
		}
	}
	return -1
}

// nearestSilence 在 (lower, end) 之內找中點最接近 est 的靜音，回傳毫秒的 [start, end]。
func nearestSilence(sils []media.Silence, lower, end, est int64) ([2]int64, bool) {
	var best [2]int64
	found := false
	var bestD int64
	for _, s := range sils {
		a, z := int64(math.Round(s.StartS*1000)), int64(math.Round(s.EndS*1000))
		if a <= lower || z >= end || z < a {
			continue
		}
		d := (a+z)/2 - est
		if d < 0 {
			d = -d
		}
		if !found || d < bestD {
			best, bestD, found = [2]int64{a, z}, d, true
		}
	}
	return best, found
}

// pieces 依切點把第 j 句切成片段；沒有切點時是一整句。
func (b *segBuilder) pieces(j int, cuts []segCut) []segPiece {
	c := b.cues[j]
	base := c.Timing
	if base == "" {
		base = subtitle.TimingSource
	}
	n := len([]rune(c.Text))
	out := make([]segPiece, 0, len(cuts)+1)
	cur := segPiece{cue: j, from: 0, start: c.StartMS, startOrigin: base}
	for _, cut := range cuts {
		cur.to, cur.end, cur.endOrigin = cut.at, cut.leftEnd, cut.origin
		out = append(out, cur)
		cur = segPiece{cue: j, from: cut.at, start: cut.rightStart, startOrigin: cut.origin}
	}
	cur.to, cur.end, cur.endOrigin = n, c.EndMS, base
	return append(out, cur)
}

// whole 回報片段是不是沒有被切開的一整句。
func (b *segBuilder) whole(p segPiece) bool {
	return p.from == 0 && p.to == len([]rune(b.cues[p.cue].Text))
}

// cueOf 把一組片段組成一句字幕：時間取第一段的 start 到最後一段的 end；
// 多段時是合併（merged），時間來源取兩端與合併中最不可靠的。
func (b *segBuilder) cueOf(s []segPiece) subtitle.Cue {
	first, last := s[0], s[len(s)-1]
	c := subtitle.Cue{StartMS: first.start, EndMS: last.end, Timing: weaker(first.startOrigin, last.endOrigin)}
	if len(s) > 1 {
		c.Timing = weaker(c.Timing, subtitle.TimingMerged)
	}
	var text strings.Builder
	prev := "" // 上一段非空的文字
	speaker := b.cues[first.cue].Speaker
	for _, piece := range s {
		src := b.cues[piece.cue]
		if part := b.pieceText(piece); part != "" {
			if text.Len() > 0 && needsSpace(prev, part) {
				text.WriteByte(' ')
			}
			text.WriteString(part)
			prev = part
		}
		c.Words = append(c.Words, b.wordsOf(piece)...)
		if !sameSpeaker(speaker, src.Speaker) {
			speaker = nil
		}
	}
	c.Text = text.String()
	if speaker != nil {
		v := *speaker
		c.Speaker = &v
	}
	return c
}

func sameSpeaker(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// wordsOf 回傳片段範圍內的字詞：一整句時是全部；切開時依比對字元的範圍，字詞對不上句子時沒有。
func (b *segBuilder) wordsOf(p segPiece) []subtitle.Word {
	c := b.cues[p.cue]
	if b.whole(p) {
		return slices.Clone(c.Words)
	}
	starts := wordStarts(c)
	if starts == nil {
		return nil
	}
	keys := keyPrefix([]rune(c.Text))
	from, to := keys[p.from], keys[p.to]
	var out []subtitle.Word
	for w, s := range starts {
		if s >= from && s < to {
			out = append(out, c.Words[w])
		}
	}
	return out
}

// needsSpace 回報接起兩段文字時中間要不要空白：任一邊是 CJK 字元或全形標點時不要。
func needsSpace(left, right string) bool {
	var l rune
	for _, r := range left {
		l = r
	}
	var r rune
	for _, x := range right {
		r = x
		break
	}
	return !wideRune(l) && !wideRune(r)
}

func wideRune(r rune) bool {
	return subtitle.IsCJK(r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// sameSegmentKeys 回報兩份字幕去掉空白與標點後的文字相同（SEG-R4）。
func sameSegmentKeys(a, b []subtitle.Cue) bool {
	keysOf := func(cues []subtitle.Cue) []rune {
		var out []rune
		for _, c := range cues {
			for _, r := range c.Text {
				if llm.IsSegmentKey(r) {
					out = append(out, r)
				}
			}
		}
		return out
	}
	return slices.Equal(keysOf(a), keysOf(b))
}

// silenceFinder 在第一次需要時下載音訊（只下載音訊，SEG-R9）並對整段跑一次 silencedetect（SEG-R8）。
// 下載與暫存走既有的音訊來源（yt-dlp、本機檔、Podcast 音檔），暫存目錄在 close 時刪除，不留下產出。
type silenceFinder struct {
	p       *Pipeline
	it      store.Item
	enabled bool
	untilMS int64

	done bool
	sils []media.Silence
	dir  string
	// warn 是不能使用 silencedetect 的原因（改用插值）；空字串代表沒有問題。
	warn string
}

// newSilenceFinder 依 split_timing_audio 建立：never 時永遠不下載音訊，段內切分直接插值（SEG-R9）。
func newSilenceFinder(p *Pipeline, it store.Item, ts TimingSettings, untilMS int64) *silenceFinder {
	return &silenceFinder{p: p, it: it, enabled: ts.SplitTimingAudio == SplitAudioAuto, untilMS: untilMS}
}

func (f *silenceFinder) get(ctx context.Context) ([]media.Silence, error) {
	if !f.enabled || f.done {
		return f.sils, nil
	}
	f.done = true
	if f.p.media == nil {
		f.warn = "沒有 ffmpeg，段內切分改用字數比例插值。"
		return nil, nil
	}
	p := f.p
	src := audioSourceFor(Deps{Store: p.st, DataDir: p.dataDir, YouTube: p.yt, HTTP: p.http, Media: p.media, Files: p.files, Version: p.version}, f.it.Platform)
	if src == nil {
		f.warn = "這個平台還不支援下載音訊，段內切分改用字數比例插值。"
		return nil, nil
	}
	tmpRoot, err := filepath.Abs(filepath.Join(p.dataDir, "tmp"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(tmpRoot, dirPerm); err != nil {
		return nil, err
	}
	if f.dir, err = os.MkdirTemp(tmpRoot, "segment-"); err != nil {
		return nil, err
	}
	path, _, err := src.fetch(ctx, f.it, f.dir)
	if err == nil {
		f.sils, err = p.media.Silences(ctx, path, 0, float64(f.untilMS)/1000+1)
	}
	if err != nil {
		if ctx.Err() != nil || !degradable(err) {
			return nil, err
		}
		p.log.Warn("無法取得停頓點，段內切分改用插值", "item", f.it.ID, "error", err)
		f.warn = "無法取得音訊的停頓點，段內切分改用字數比例插值。"
		f.sils = nil
	}
	return f.sils, nil
}

// degradable 回報取得停頓點的錯誤可以改用插值：permanent 與 needs_action（重試也不會好）；
// 其他（retryable、wait、不明的錯誤）回傳給任務重試。
func degradable(err error) bool {
	class, ok := errclass.ClassOf(err)
	return ok && (class == errclass.ClassPermanent || class == errclass.ClassNeedsAction)
}

func (f *silenceFinder) close() {
	if f.dir != "" {
		_ = os.RemoveAll(f.dir)
	}
}
