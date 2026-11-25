package benchdata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/search"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// Profile 是一組資料量（nfr.md 第 1.2 節）。
type Profile struct {
	Name string
	// Items 是影音數，Channels 是頻道數（影音依序輪流分到各頻道）。
	Items, Channels int
	// CuesPerItem 是每部影音每個字幕版本的句數。
	CuesPerItem int
	// Variants 是每部影音的字幕版本，依寫入順序；最後寫入的不一定是索引的那一份（索引選順位最高的，SRCH-R1）。
	Variants []subtitle.Variant
	// Seed 決定標題與字幕文字。
	Seed uint64
	// TranslateTo 不為空時，每部影音另有一份這個語言的譯文（translated，時間與原文相同），
	// 給雙語 SRT 與 ASS 的量測使用（OUT-R5）。
	TranslateTo string
}

// lib-10k 的「每部 3 個版本的產出」解讀為 3 個字幕變體 source、normalized、segmented：
// M1 預設設定下一部 YouTube 影片實際會留下的字幕產出（transcript、normalize、segment 各一份）。
// （2026-11-25，依 /goal 由實作者暫定，待 owner 追認；記在 nfr.md 第 10 節。）
var threeVariants = []subtitle.Variant{subtitle.VariantSource, subtitle.VariantNormalized, subtitle.VariantSegmented}

// Lib10k 是 nfr.md 的 lib-10k：10,000 部、500 個頻道、每部約 600 句、每部 3 個版本。
var Lib10k = Profile{Name: "lib-10k", Items: 10000, Channels: 500, CuesPerItem: 600, Variants: threeVariants, Seed: 1}

// ItemLong 是 nfr.md 的 item-long：一部 3 小時、約 3,000 句的影片。
var ItemLong = Profile{Name: "item-long", Items: 1, Channels: 1, CuesPerItem: 3000, Variants: threeVariants, Seed: 2, TranslateTo: "zh-Hant"}

// ItemLongDuration 是 item-long 的片長。
const ItemLongDuration = 3 * time.Hour

// ProfileByName 依名稱（lib-10k、item-long）回傳 Profile。
func ProfileByName(name string) (Profile, bool) {
	switch name {
	case Lib10k.Name:
		return Lib10k, true
	case ItemLong.Name:
		return ItemLong, true
	}
	return Profile{}, false
}

// Scaled 依比例縮小影音數與頻道數（至少 1），每部的句數不變：開發與 PR smoke 用。f >= 1 時原樣回傳。
func (p Profile) Scaled(f float64) Profile {
	if f >= 1 || f <= 0 {
		return p
	}
	p.Items = max(1, int(math.Round(float64(p.Items)*f)))
	p.Channels = max(1, int(math.Round(float64(p.Channels)*f)))
	p.Name = fmt.Sprintf("%s@%g", p.Name, f)
	return p
}

// Options 是產生時的選項。
type Options struct {
	// Clock 決定 created_at；nil 時用固定起點的 fake clock，每部影音推進 1 秒（列表依建立時間排序，不要全部同一毫秒）。
	Clock *clock.Fake
	// Progress 每寫完一批影音呼叫一次。
	Progress func(done, total int)
	// BatchSize 是每個交易寫入的影音數；0 時用 50。
	BatchSize int
}

// Result 是產生的結果。
type Result struct {
	// ItemIDs 依建立順序（舊到新）。
	ItemIDs []string
	// IndexedCues 是寫入全文搜尋索引的句數（每部影音每個語言只索引一份，SRCH-R1）。
	IndexedCues int64
	// SnapshotID 是 stage run 使用的設定快照。
	SnapshotID string
}

// Generate 在 dir 建立（或補寫）一個資料目錄並寫入 p 的資料。dir 不存在時建立（0700）。
func Generate(ctx context.Context, dir string, p Profile, o Options) (Result, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, err
	}
	clk := o.Clock
	if clk == nil {
		clk = DefaultClock()
		o.Clock = clk
	}
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		return Result{}, err
	}
	res, err := Populate(ctx, st, dir, p, o)
	if cerr := st.Close(); err == nil {
		err = cerr
	}
	return res, err
}

// DefaultClock 是產生器預設的時鐘起點。
func DefaultClock() *clock.Fake { return clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) }

// doneStages 是產生的影音完成的步驟；其餘步驟是 skipped（M1 預設設定的樣子）。
var doneStages = []string{pipeline.StageMetadata, pipeline.StageTranscript, pipeline.StageNormalize, pipeline.StageSegment}

// variantStage 是字幕變體對應的步驟（stage run 的結果指向它的產出）。
var variantStage = map[subtitle.Variant]string{
	subtitle.VariantSource:     pipeline.StageTranscript,
	subtitle.VariantNormalized: pipeline.StageNormalize,
	subtitle.VariantSegmented:  pipeline.StageSegment,
}

// Populate 把 p 的資料寫進已經開啟的 st；dir 是 st 的資料目錄（產出檔寫在它底下）。
// o.Clock 必須是 st 使用的時鐘（nil 時不推進時間）。
func Populate(ctx context.Context, st *store.Store, dir string, p Profile, o Options) (Result, error) {
	if p.Items <= 0 || p.Channels <= 0 || p.CuesPerItem <= 0 || len(p.Variants) == 0 {
		return Result{}, errors.New("benchdata: profile 不完整")
	}
	for _, v := range p.Variants[:len(p.Variants)-1] {
		if !search.Better(string(p.Variants[len(p.Variants)-1]), string(v)) {
			return Result{}, errors.New("benchdata: Variants 必須依索引順位由低到高排列（最後一個是索引選中的）")
		}
	}
	batch := o.BatchSize
	if batch <= 0 {
		batch = 50
	}
	snap, err := st.Write().EnsureSnapshot(ctx, "benchdata", "{}")
	if err != nil {
		return Result{}, err
	}
	res := Result{SnapshotID: snap}
	for start := 0; start < p.Items; start += batch {
		end := min(start+batch, p.Items)
		// 檔案先寫好（pipeline 也是先寫檔、再在交易中記錄），交易只做 DB 的部分。
		specs := make([]itemSpec, 0, end-start)
		for i := start; i < end; i++ {
			specs = append(specs, newItemSpec(p, i))
		}
		var pending []rename
		err := st.Tx(ctx, func(r *store.Repo) error {
			pending = pending[:0]
			for k := range specs {
				id, n, err := writeItem(ctx, r, dir, snap, &specs[k], &pending)
				if err != nil {
					return fmt.Errorf("影音 #%d：%w", specs[k].n, err)
				}
				res.ItemIDs = append(res.ItemIDs, id)
				res.IndexedCues += n
				if o.Clock != nil {
					o.Clock.Advance(time.Second)
				}
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		for _, mv := range pending {
			if err := os.Rename(mv.from, mv.to); err != nil {
				return res, err
			}
		}
		if o.Progress != nil {
			o.Progress(end, p.Items)
		}
	}
	return res, nil
}

// itemSpec 是一部影音的決定論內容。
type itemSpec struct {
	n         int
	platform  string
	extID     string
	title     string
	lang      string
	channel   int
	durationS int
	published time.Time
	cues      []subtitle.Cue
	variants  []subtitle.Variant
	failSeg   bool
	// translated 是譯文（沒有時為 nil）。
	translated *subtitle.Transcript
}

// 標題的主題：列表與標題搜尋的 bench 用 PostgreSQL（每 4 部一部）、Agile（每 3 部一部）篩選。
var (
	topicsA = []string{"PostgreSQL", "Kubernetes", "TypeScript", "Rust"}
	topicsB = []string{"Agile", "Observability", "測試策略"}
)

func newItemSpec(p Profile, i int) itemSpec {
	platform := []string{"youtube", "bilibili"}[i%2]
	lang := []string{"en", "zh-Hant"}[i%2]
	cues := Cues(p.CuesPerItem, lang, p.Seed*1_000_003+uint64(i))
	if i%10 == 0 && len(cues) > 3 {
		// 每 10 部有一部含補時間的句子（LIB-R7 的 timing 篩選）。
		cues[1].Timing = subtitle.TimingInterpolated
	}
	dur := int(cues[len(cues)-1].EndMS/1000) + 1
	var translated *subtitle.Transcript
	if p.TranslateTo != "" && p.TranslateTo != lang {
		tc := Cues(len(cues), p.TranslateTo, p.Seed*7_000_001+uint64(i))
		for k := range tc {
			tc[k].StartMS, tc[k].EndMS, tc[k].Timing = cues[k].StartMS, cues[k].EndMS, cues[k].Timing
		}
		translated = &subtitle.Transcript{Lang: p.TranslateTo, Variant: subtitle.VariantTranslated, Cues: tc}
	}
	return itemSpec{
		translated: translated,
		n:          i,
		platform:   platform,
		extID:      fmt.Sprintf("bd%08x%06d", fnv32(p.Name), i),
		title:      fmt.Sprintf("Video %d about %s 與 %s", i, topicsA[i%len(topicsA)], topicsB[i%len(topicsB)]),
		lang:       lang,
		channel:    i % p.Channels,
		durationS:  dur,
		published:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i%900) * time.Hour),
		cues:       cues,
		variants:   p.Variants,
		failSeg:    i%97 == 5 && p.Items > 1,
	}
}

// rename 是交易提交後才就位的產出檔。
type rename struct{ from, to string }

func writeItem(ctx context.Context, r *store.Repo, dir, snap string, s *itemSpec, pending *[]rename) (string, int64, error) {
	it, _, err := r.InsertItemIfAbsent(ctx, store.NewItem{Platform: s.platform, ExternalID: s.extID, URL: itemURL(s.platform, s.extID)})
	if err != nil {
		return "", 0, err
	}
	chID, err := r.UpsertChannel(ctx, store.Channel{Platform: s.platform, ExternalID: fmt.Sprintf("bdch%04d", s.channel),
		Title: fmt.Sprintf("Channel %03d", s.channel)})
	if err != nil {
		return "", 0, err
	}
	pub := s.published
	if err := r.UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: s.title, DurationS: &s.durationS, PublishedAt: &pub,
		OriginalLang: s.lang, LangSource: "metadata", Availability: "public", ChannelID: chID}); err != nil {
		return "", 0, err
	}
	runs := make(map[string]string, len(pipeline.Stages))
	for _, stage := range pipeline.Stages {
		run, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return "", 0, err
		}
		runs[stage] = run.ID
		state := store.StageState{Status: store.StatusSkipped}
		switch {
		case stage == pipeline.StageSegment && s.failSeg:
			state = store.StageState{Status: store.StatusFailed, ErrorClass: "transient", Message: "benchdata: 模擬失敗"}
		case slices.Contains(doneStages, stage), stage == pipeline.StageTranslate && s.translated != nil:
			state = store.StageState{Status: store.StatusDone}
		}
		if err := r.SetStageState(ctx, run.ID, state); err != nil {
			return "", 0, err
		}
	}
	var parents []string
	variants := s.variants
	if s.failSeg {
		// segment 失敗的影音沒有 segmented 版本，索引改用 normalized（SRCH-R1）。
		variants = slices.DeleteFunc(slices.Clone(variants), func(v subtitle.Variant) bool { return v == subtitle.VariantSegmented })
	}
	for k, v := range variants {
		tr := subtitle.Transcript{Lang: s.lang, Variant: v, Cues: s.cues}
		// 只有最後一份（順位最高、索引會選中的）在寫入 DB 時就位；其餘先寫到暫存名稱，交易提交後才 rename。
		// InsertArtifact 每寫一份字幕就重建該語言的索引（SRCH-R2），讀不到檔的版本不建立索引列（SRCH S8），
		// 所以前兩份不會被索引又刪掉：最終的索引與產品依序寫入三份的結果相同，FTS 的寫入只剩 1/5。
		last := k == len(variants)-1
		art, staged, err := writeCues(dir, it.ID, tr, parents, map[string]any{"stage": variantStage[v], "benchdata": true}, !last)
		if err != nil {
			return "", 0, err
		}
		if staged != "" {
			*pending = append(*pending, rename{from: staged, to: filepath.Join(dir, filepath.FromSlash(art.Path))})
		}
		a, err := r.InsertArtifact(ctx, art)
		if err != nil {
			return "", 0, err
		}
		parents = []string{a.ID}
		if err := r.SetStageResult(ctx, runs[variantStage[v]], store.StageResult{Executor: "local", ImplVersion: 1, ArtifactID: a.ID}); err != nil {
			return "", 0, err
		}
	}
	indexed := int64(len(s.cues))
	if s.translated != nil {
		art, err := WriteCuesArtifact(dir, it.ID, *s.translated, parents, map[string]any{"stage": pipeline.StageTranslate, "benchdata": true})
		if err != nil {
			return "", 0, err
		}
		a, err := r.InsertArtifact(ctx, art)
		if err != nil {
			return "", 0, err
		}
		if err := r.SetStageResult(ctx, runs[pipeline.StageTranslate], store.StageResult{Executor: "local", ImplVersion: 1, ArtifactID: a.ID}); err != nil {
			return "", 0, err
		}
		indexed += int64(len(s.translated.Cues))
	}
	return it.ID, indexed, nil
}

func itemURL(platform, ext string) string {
	if platform == "bilibili" {
		return "https://www.bilibili.com/video/" + ext
	}
	return "https://www.youtube.com/watch?v=" + ext
}

// WriteCuesArtifact 照 pipeline 的寫法（artifacts/<item>/<artifact>.json、先寫暫存檔再 rename、0700 目錄、0600 檔）
// 把字幕寫成產出檔，回傳可以交給 Repo.InsertArtifact 的 NewArtifact。prov 是來源紀錄的欄位（executor 等有預設值）。
func WriteCuesArtifact(dir, itemID string, tr subtitle.Transcript, parents []string, prov map[string]any) (store.NewArtifact, error) {
	art, _, err := writeCues(dir, itemID, tr, parents, prov, false)
	return art, err
}

// writeCues 寫出產出檔；staged 為 true 時留在暫存名稱（回傳它的路徑），由呼叫端之後 rename 到 art.Path。
func writeCues(dir, itemID string, tr subtitle.Transcript, parents []string, prov map[string]any, staged bool) (store.NewArtifact, string, error) {
	id := store.NewID()
	adir := filepath.Join(dir, "artifacts", itemID)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		return store.NewArtifact{}, "", err
	}
	tmp, err := os.CreateTemp(adir, ".tmp-"+id+"-*")
	if err != nil {
		return store.NewArtifact{}, "", err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(tmp, h)}
	err = subtitle.RenderCuesJSON(cw, tr)
	if err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	name := id + ".json"
	if err == nil && !staged {
		err = os.Rename(tmp.Name(), filepath.Join(adir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return store.NewArtifact{}, "", err
	}
	fields := map[string]any{"executor": "local", "created_at": "2026-01-01T00:00:00Z", "tool_versions": map[string]string{"notesteme": "benchdata"}}
	for k, v := range prov {
		fields[k] = v
	}
	pj, err := json.Marshal(fields)
	if err != nil {
		return store.NewArtifact{}, "", err
	}
	var timings []string
	for _, c := range tr.Cues {
		if c.Timing != "" && !slices.Contains(timings, string(c.Timing)) {
			timings = append(timings, string(c.Timing))
		}
	}
	slices.Sort(timings)
	art := store.NewArtifact{
		ID: id, ItemID: itemID, Kind: "transcript", Variant: string(tr.Variant), Lang: tr.Lang,
		Format: "notesteme-cues+json", Path: "artifacts/" + itemID + "/" + name, SHA256: hex.EncodeToString(h.Sum(nil)),
		Size: cw.n, Parents: parents, Provenance: pj, Timings: timings,
	}
	if staged {
		return art, tmp.Name(), nil
	}
	return art, "", nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// 詞彙（決定論）：bench 依這些詞的出現比例選擇查詢。
const (
	// CommonTerm 出現在約 5% 的英文句子；ShortTermEN（AI）約 4%，ShortTermZH 約 10% 的中文句子（全文搜尋的「常見詞」）。
	CommonTerm = "database"
	// RareTerm 出現在約 1/20,000 的句子（全文搜尋的「罕見詞」，lib-10k 約 300 句）。
	RareTerm = "zephyrine"
	// ShortTermEN、ShortTermZH 是 2 字的查詢，走 instr 掃描（SRCH-R3、NFR-PERF-9 的 2 字以下）。
	ShortTermEN = "ai"
	ShortTermZH = "資料"
)

var (
	enWords = strings.Fields(`the a to and of in that is it you we this for on with be are have not but so what
		can just like about know they do one all there if or from at going get people think will really because
		more when time right make see then now some how want well our also these which very other here way need
		thing first also look into work data system query index server client model build deploy cache memory
		latency request response error retry queue worker stream buffer file disk network test case feature
		release version update change design review team product user customer value small large fast slow
		simple hard easy problem solution example question answer reason point idea part whole start finish`)
	zhWords = strings.Fields(`我們 今天 這個 那個 其實 就是 因為 所以 如果 可以 一個 大家 問題 方法 系統 伺服器 使用者
		設計 測試 版本 更新 部署 效能 記憶體 網路 檔案 影片 字幕 時間 開始 結束 然後 但是 還是 已經 應該 需要 覺得
		知道 看到 處理 結果 例子 原因 重點 簡單 困難 快速 緩慢 團隊 產品 客戶 價值 功能 錯誤 重試 佇列 串流 模型`)
)

// Cues 產生 n 句決定論的字幕：每句 2～6 秒、句間 0～400ms 的空檔；lang 是 zh 開頭時產生中文句子。
// 平均約 3.6 秒一句，3,000 句約 3 小時（item-long）。
func Cues(n int, lang string, seed uint64) []subtitle.Cue {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	zh := strings.HasPrefix(lang, "zh")
	cues := make([]subtitle.Cue, n)
	var t int64
	var sb strings.Builder
	for i := range cues {
		dur := int64(2000 + rng.IntN(3200))
		sb.Reset()
		words := 6 + rng.IntN(7)
		if zh {
			words = 5 + rng.IntN(5)
		}
		for w := range words {
			if !zh && w > 0 {
				sb.WriteByte(' ')
			}
			switch r := rng.IntN(100_000); {
			case r < 5 && w == 0:
				sb.WriteString(RareTerm)
			case zh && r < 1500:
				sb.WriteString(ShortTermZH)
			case !zh && r < 550:
				sb.WriteString(CommonTerm)
			case !zh && r < 1050:
				sb.WriteString("AI")
			case zh:
				sb.WriteString(zhWords[rng.IntN(len(zhWords))])
			default:
				sb.WriteString(enWords[rng.IntN(len(enWords))])
			}
		}
		if zh {
			sb.WriteString("。")
		} else {
			sb.WriteByte('.')
		}
		cues[i] = subtitle.Cue{Index: i + 1, StartMS: t, EndMS: t + dur, Text: sb.String(), Timing: subtitle.TimingSource}
		t += dur + int64(rng.IntN(400))
	}
	return cues
}

// fnv32 讓不同 profile 的影音代號不重複（同一個資料目錄可以放多個 profile）。
func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
