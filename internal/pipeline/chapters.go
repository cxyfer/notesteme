package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 章節的產出（CHAP-R7、ADR-0020 第 4 點）：kind 與 variant 同名，平台與 LLM 的章節是同一組，互相取代。
const (
	KindChapters = "chapters"
	chaptersFmt  = "notesteme-chapters+json"
	// 章節的來源（CHAP-R7）。
	ChapterOriginPlatform = "platform"
	ChapterOriginLLM      = "llm"
	// minPlatformChapters 是直接採用平台章節的門檻（CHAP-R1、H2）。
	minPlatformChapters = 2
	// maxPlatformChapterTitle 是平台章節標題保留的字元數：平台的資料一樣不可信（H5），只截斷不重試。
	maxPlatformChapterTitle  = 200
	actionRegenerateChapters = "重新產生章節"
)

// Chapter 是一個章節；時間是毫秒（CHAP-R5）。Cue 只在 LLM 的章節有值（起點的句號）。
type Chapter struct {
	Title   string `json:"title"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Cue     int    `json:"cue,omitempty"`
}

// Chapters 是 chapters 產出檔的內容：來源與章節（依時間排序）。沒有章節時 Chapters 是空的（CHAP-R4、R8）。
type Chapters struct {
	Origin   string    `json:"origin"`
	Chapters []Chapter `json:"chapters"`
}

// platformChapter 是 items.metadata_json 的一個章節（yt-dlp 的 chapters，秒）。
type platformChapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// platformChapters 讀出影音 metadata 中的平台章節；格式不對時當成沒有。
func platformChapters(it store.Item) []platformChapter {
	if len(it.Metadata) == 0 {
		return nil
	}
	var md struct {
		Chapters []platformChapter `json:"chapters"`
	}
	if json.Unmarshal(it.Metadata, &md) != nil {
		return nil
	}
	return md.Chapters
}

// platformChapterCount 是 StageContext.PlatformChapters：平台章節的數量（CHAP-R1）。
func platformChapterCount(it store.Item) int { return len(platformChapters(it)) }

// durationMS 是影音的片長（毫秒）；不知道時是 0。
func durationMS(it store.Item) int64 {
	if it.DurationS != nil && *it.DurationS > 0 {
		return int64(*it.DurationS) * 1000
	}
	return 0
}

// fillChapterEnds 依 CHAP-R5 補上時間：第一個章節從 0 開始，每個章節到下一個章節的起點結束，最後一個到片長
// （不知道片長、或片長不晚於最後一個起點時用 lastEnd，再不行就和起點相同）。list 必須依起點排序。
func fillChapterEnds(list []Chapter, durMS, lastEnd int64) {
	for k := range list {
		if k == 0 {
			list[k].StartMS = 0
		}
		switch {
		case k+1 < len(list):
			list[k].EndMS = list[k+1].StartMS
		case durMS > list[k].StartMS:
			list[k].EndMS = durMS
		case lastEnd > list[k].StartMS:
			list[k].EndMS = lastEnd
		default:
			list[k].EndMS = list[k].StartMS
		}
	}
}

// sanitizePlatformTitle 去掉平台章節標題的控制字元與頭尾空白，並截斷到 maxPlatformChapterTitle 個字元。
func sanitizePlatformTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '‍' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxPlatformChapterTitle {
		s = strings.TrimSpace(string([]rune(s)[:maxPlatformChapterTitle]))
	}
	return s
}

// platformChapterList 把平台章節換成 Chapter（依起點排序，時間依 CHAP-R5 補齊）。
func platformChapterList(it store.Item) []Chapter {
	src := platformChapters(it)
	list := make([]Chapter, 0, len(src))
	var lastEnd int64
	for _, c := range src {
		start := secondsToMS(c.Start)
		list = append(list, Chapter{Title: sanitizePlatformTitle(c.Title), StartMS: start})
		lastEnd = max(lastEnd, secondsToMS(c.End))
	}
	slices.SortStableFunc(list, func(a, b Chapter) int { return int(a.StartMS - b.StartMS) })
	fillChapterEnds(list, durationMS(it), lastEnd)
	return list
}

// secondsToMS 把秒換成毫秒；負數、NaN 與過大的值視為 0。
func secondsToMS(s float64) int64 {
	if math.IsNaN(s) || s <= 0 || s > 1e9 {
		return 0
	}
	return int64(math.Round(s * 1000))
}

// runPlatformChapters 直接採用平台章節（CHAP-R1）：寫入 origin platform 的 chapters 產出，步驟是 skipped（platform_chapters），
// 不呼叫 LLM。內容與現行產出相同時沿用現行產出，不建立新版本。
func (p *Pipeline) runPlatformChapters(ctx context.Context, it store.Item) (result, error) {
	doc := Chapters{Origin: ChapterOriginPlatform, Chapters: platformChapterList(it)}
	stage := &store.StageResult{Executor: ExecutorLocal, ImplVersion: p.implVersion(StageChapters)}
	state := skippedState(SkipPlatformChapters)
	body, err := json.Marshal(doc)
	if err != nil {
		return result{}, err
	}
	if cur, ok, err := p.currentChaptersArtifact(ctx, it.ID); err != nil {
		return result{}, err
	} else if ok && cur.SHA256 == sha256Hex(body) {
		stage.ArtifactID = cur.ID
		return result{task: taskDone, advance: true, stage: stage, state: state}, nil
	}
	prov, err := p.provenance(map[string]any{
		"stage": StageChapters, "impl_version": p.implVersion(StageChapters), "origin": ChapterOriginPlatform,
	}, nil)
	if err != nil {
		return result{}, err
	}
	art, err := p.newChaptersArtifact(it.ID, body, nil, prov)
	if err != nil {
		return result{}, err
	}
	return result{task: taskDone, advance: true, stage: stage, artifact: art, state: state}, nil
}

// newChaptersArtifact 寫出 chapters 產出檔，回傳要寫入的產出記錄。
func (p *Pipeline) newChaptersArtifact(itemID string, body []byte, parents []string, prov json.RawMessage) (*store.NewArtifact, error) {
	id := store.NewID()
	rel, sha, size, err := writeArtifactFile(p.dataDir, itemID, id, func(w io.Writer) error {
		_, werr := w.Write(body)
		return werr
	})
	if err != nil {
		return nil, fmt.Errorf("寫入產出檔：%w", err)
	}
	return &store.NewArtifact{
		ID: id, ItemID: itemID, Kind: KindChapters, Variant: KindChapters, Format: chaptersFmt,
		Path: rel, SHA256: sha, Size: size, Parents: parents, Provenance: prov,
	}, nil
}

// currentChaptersArtifact 回傳影音現行的 chapters 產出。
func (p *Pipeline) currentChaptersArtifact(ctx context.Context, itemID string) (store.Artifact, bool, error) {
	arts, err := p.st.Read().CurrentArtifacts(ctx, itemID, KindChapters)
	if err != nil || len(arts) == 0 {
		return store.Artifact{}, false, err
	}
	return arts[len(arts)-1], true, nil
}

// ItemChapters 回傳影音現行的章節（CHAP-R7）；chapters 步驟沒有產生過產出時 ok 為 false。
// 產出檔讀不到或內容與 sha256 不符時回傳錯誤，由呼叫端決定是否略過。
func (p *Pipeline) ItemChapters(ctx context.Context, itemID string) (doc Chapters, ok bool, err error) {
	art, ok, err := p.currentChaptersArtifact(ctx, itemID)
	if err != nil || !ok {
		return Chapters{}, false, err
	}
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(art.Path)))
	if err != nil {
		return Chapters{}, false, err
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != art.SHA256 {
		return Chapters{}, false, fmt.Errorf("%s：%w", art.Path, ErrCorruptArtifact)
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		return Chapters{}, false, err
	}
	if doc.Chapters == nil {
		doc.Chapters = []Chapter{}
	}
	return doc, true, nil
}

// ---- LLM 產生的章節（CHAP-R2～R6） ----

// chaptersStage 是 chapters 在 LLM 引擎中的登記：輸入排除片段內的 cue（prepareLLM），輸出寫成 chapters 產出（writeChapters）。
var chaptersStage = llmStage{
	spec: func(extras map[string]any) *llm.Spec {
		return llm.ChaptersSpec(int(extraFloat(extras, "max_chapters", llm.DefaultMaxChapters)))
	},
	write: (*Pipeline).writeChapters,
}

// cueInSponsor 回報 cue 是不是片段內的 cue（SPB-R7、B2）：一半以上的時間落在片段內。
// 沒有長度的 cue 看起點是否在片段內。segs 是 sponsorblock 合併過、互不重疊的片段。
func cueInSponsor(c subtitle.Cue, segs []sponsorblock.Segment) bool {
	dur := c.EndMS - c.StartMS
	if dur <= 0 {
		return slices.ContainsFunc(segs, func(s sponsorblock.Segment) bool { return c.StartMS >= s.StartMS && c.StartMS < s.EndMS })
	}
	var overlap int64
	for _, s := range segs {
		lo, hi := max(c.StartMS, s.StartMS), min(c.EndMS, s.EndMS)
		if hi > lo {
			overlap += hi - lo
		}
	}
	return overlap*2 > dur
}

// chaptersSponsor 讀取 chapters 的 SponsorBlock 片段：沒有現行的 sponsor_segments 產出時回傳 nil（沒有片段可以排除）。
func (p *Pipeline) chaptersSponsor(ctx context.Context, itemID string) (*store.Artifact, []sponsorblock.Segment, error) {
	art, ok, err := p.currentSponsorArtifact(ctx, itemID)
	if err != nil || !ok {
		return nil, nil, err
	}
	segs, _, err := p.SponsorSegments(ctx, itemID)
	if err != nil {
		return nil, nil, errPermanent("讀取 SponsorBlock 片段失敗：" + err.Error())
	}
	return &art, segs, nil
}

// mergeChapters 依 chunk 順序合併各 chunk 的章節（CHAP-R2）：重疊區的句子屬於前一個 chunk，
// 後面的 chunk 起點不晚於前一個 chunk 最後一句的章節捨棄；總數超過 max 時只保留前面的。
func mergeChapters(job *llmJob, outputs []json.RawMessage, maxChapters int) ([]llm.Chapter, error) {
	var merged []llm.Chapter
	last := 0 // 前一個 chunk 最後一句的句號
	for k, raw := range outputs {
		c := job.chunks[k]
		in := job.chunkInput(c)
		list, verr := llm.CheckChapters(in, raw, maxChapters)
		if verr != nil {
			return nil, fmt.Errorf("chunk %d 的輸出不正確：%s", c.Index, verr.Message)
		}
		for _, ch := range list {
			if ch.Cue > last {
				merged = append(merged, ch)
			}
		}
		if n := len(in.Cues); n > 0 {
			last = max(last, in.Cues[n-1].I)
		}
	}
	if len(merged) > maxChapters {
		merged = merged[:maxChapters]
	}
	return merged, nil
}

// chaptersFallbackMessage 是重試用盡時的訊息（CHAP-R4）：說明第一個退回的 chunk 最後一次的驗證錯誤。
func chaptersFallbackMessage(fallbacks []fallbackRecord) string {
	reason := "格式不正確"
	if len(fallbacks) > 0 {
		if s, ok := fallbacks[0].Detail["error"].(string); ok && s != "" {
			reason = s
		} else if fallbacks[0].Reason != "" {
			reason = fallbacks[0].Reason
		}
	}
	return fmt.Sprintf("無法產生章節：模型回傳的內容沒有通過驗證（%s）。筆記不會分章節。", reason)
}

// writeChapters 是 chapters 的 llmStage.write：把通過驗證的章節換算成時間（CHAP-R5），寫成 origin llm 的 chapters 產出。
// 有任何 chunk 重試用盡時改為沒有章節（CHAP-R4、H3）：步驟仍是 done，存空的產出，附上訊息與動作。
func (p *Pipeline) writeChapters(ctx context.Context, it store.Item, job *llmJob, outputs []json.RawMessage, fallbacks []fallbackRecord, fields map[string]any) (*store.NewArtifact, store.StageState, error) {
	state := store.StageState{Status: store.StatusDone}
	doc := Chapters{Origin: ChapterOriginLLM, Chapters: []Chapter{}}
	if len(fallbacks) > 0 {
		state.Message = chaptersFallbackMessage(fallbacks)
		state.Actions = []string{actionRegenerateChapters, actionOpenPrompt}
	} else {
		maxN := llm.DefaultMaxChapters
		if extras, err := stageExtras(job.eff, StageChapters); err == nil {
			maxN = int(extraFloat(extras, "max_chapters", llm.DefaultMaxChapters))
		}
		list, err := mergeChapters(job, outputs, maxN)
		if err != nil {
			return nil, state, errPermanent(err.Error())
		}
		var lastEnd int64
		for _, ch := range list {
			if ch.Cue < 1 || ch.Cue > len(job.tr.Cues) {
				return nil, state, errPermanent(fmt.Sprintf("章節的句號 %d 超出字幕範圍", ch.Cue))
			}
			cue := job.tr.Cues[ch.Cue-1]
			doc.Chapters = append(doc.Chapters, Chapter{Title: ch.Title, StartMS: cue.StartMS, Cue: ch.Cue})
		}
		if n := len(job.tr.Cues); n > 0 {
			lastEnd = job.tr.Cues[n-1].EndMS
		}
		fillChapterEnds(doc.Chapters, durationMS(it), lastEnd)
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, state, err
	}
	fields["origin"] = ChapterOriginLLM
	parents := []string{job.input.ID}
	if job.sponsor != nil {
		parents = append(parents, job.sponsor.ID)
		if df, ok := fields["derived_from"].([]map[string]string); ok {
			fields["derived_from"] = append(df, map[string]string{"artifact": job.sponsor.ID, "variant": job.sponsor.Variant, "sha256": job.sponsor.SHA256})
		}
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return nil, state, err
	}
	art, err := p.newChaptersArtifact(it.ID, body, parents, prov)
	return art, state, err
}
