package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// listEnv 是列表測試的環境：只有 store 與 pipeline，不需要 worker。
type listEnv struct {
	tb   testing.TB
	p    *Pipeline
	st   *store.Store
	clk  *clock.Fake
	snap string
	dir  string
}

func newListEnv(tb testing.TB) *listEnv {
	tb.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := tb.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, Version: "test"})
	snap, err := st.Write().EnsureSnapshot(context.Background(), "sha", "{}")
	if err != nil {
		tb.Fatal(err)
	}
	return &listEnv{tb: tb, p: p, st: st, clk: clk, snap: snap, dir: dir}
}

// trash 把影音移到垃圾桶（設定 deleted_at）；目前還沒有刪除功能，直接改資料庫。
func (e *listEnv) trash(itemID string) {
	e.tb.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		e.tb.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET deleted_at = ? WHERE id = ?`, e.clk.Now().UnixMilli(), itemID); err != nil {
		e.tb.Fatal(err)
	}
}

// 垃圾桶中的影音不在列表，也不計入 total（LIB 規格：由單元測試守住）。
func TestListItemsExcludesTrashed(t *testing.T) {
	e := newListEnv(t)
	items := e.seed(
		lItem{platform: "youtube", ext: "a", title: "A", stages: done(StageNormalize)},
		lItem{platform: "youtube", ext: "b", title: "B", stages: done(StageNormalize)},
		lItem{platform: "youtube", ext: "c", title: "C", stages: done(StageNormalize)},
	)
	if page, err := e.p.ListItems(context.Background(), ItemQuery{}); err != nil || page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("前置條件：移到垃圾桶前 total=%d items=%d err=%v", page.Total, len(page.Items), err)
	}
	e.trash(items[1].ID)
	queries := map[string]ItemQuery{
		"沒有條件":      {},
		"搭配 status": {Status: map[string][]string{StageNormalize: {"done"}}},
		"搭配 q":      {Q: "b"},
	}
	for name, q := range queries {
		page, err := e.p.ListItems(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			if it.ID == items[1].ID {
				t.Errorf("%s：垃圾桶中的影音不該出現在列表", name)
			}
		}
		want := 2
		if name == "搭配 q" {
			want = 0
		}
		if len(page.Items) != want || page.Total != want {
			t.Errorf("%s：items=%d total=%d，預期 %d", name, len(page.Items), page.Total, want)
		}
	}
}

// lItem 描述一部要建立的影音。
type lItem struct {
	platform, ext string
	title, lang   string
	channel       string
	dur           *int
	published     *time.Time
	// stages 是 stage → 狀態（建立一筆 stage run）。
	stages   map[string]string
	errClass map[string]string
	timings  []string
}

func ip(n int) *int { return &n }

func tp(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

// add 建立影音，並讓時鐘前進 1 秒，讓 created_at 各不相同。
func (e *listEnv) add(ctx context.Context, tx *store.Repo, in lItem) store.Item {
	e.tb.Helper()
	it, _, err := tx.InsertItemIfAbsent(ctx, store.NewItem{Platform: in.platform, ExternalID: in.ext, URL: "https://example.test/" + in.ext})
	if err != nil {
		e.tb.Fatal(err)
	}
	meta := store.ItemMeta{Title: in.title, DurationS: in.dur, PublishedAt: in.published, OriginalLang: in.lang}
	if in.lang != "" {
		meta.LangSource = "metadata"
	}
	if in.channel != "" {
		chID, err := tx.UpsertChannel(ctx, store.Channel{Platform: in.platform, ExternalID: in.channel, Title: in.channel})
		if err != nil {
			e.tb.Fatal(err)
		}
		meta.ChannelID = chID
	}
	if err := tx.UpdateItemMeta(ctx, it.ID, meta); err != nil {
		e.tb.Fatal(err)
	}
	for stage, status := range in.stages {
		run, err := tx.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: e.snap})
		if err != nil {
			e.tb.Fatal(err)
		}
		if status != store.StatusPending {
			if err := tx.SetStageState(ctx, run.ID, store.StageState{Status: status, ErrorClass: in.errClass[stage]}); err != nil {
				e.tb.Fatal(err)
			}
		}
	}
	if in.timings != nil {
		e.addTranscript(ctx, tx, it.ID, in.timings)
	}
	e.clk.Advance(time.Second)
	return it
}

func (e *listEnv) addTranscript(ctx context.Context, tx *store.Repo, itemID string, timings []string) {
	e.tb.Helper()
	if _, err := tx.InsertArtifact(ctx, store.NewArtifact{
		ItemID: itemID, Kind: "transcript", Variant: "source", Lang: "en", Format: "cues", Path: "x", SHA256: "s",
		Provenance: json.RawMessage(`{"a":1}`), Timings: timings,
	}); err != nil {
		e.tb.Fatal(err)
	}
}

func (e *listEnv) seed(items ...lItem) []store.Item {
	e.tb.Helper()
	var out []store.Item
	err := e.st.Tx(context.Background(), func(tx *store.Repo) error {
		for _, in := range items {
			out = append(out, e.add(context.Background(), tx, in))
		}
		return nil
	})
	if err != nil {
		e.tb.Fatal(err)
	}
	return out
}

func done(stages ...string) map[string]string {
	m := map[string]string{}
	for _, s := range stages {
		m[s] = store.StatusDone
	}
	return m
}

// libraryFixture 對應 LIB 場景的三部影音，另加幾部讓各種篩選有差異。
func libraryFixture(e *listEnv) map[string]store.Item {
	items := e.seed(
		lItem{platform: "youtube", ext: "pg100", title: "PostgreSQL in 100 Seconds", lang: "en", channel: "fireship", dur: ip(100),
			published: tp("2026-01-10T00:00:00Z"), stages: done(StageMetadata, StageTranscript, StageNormalize), timings: []string{"source"}},
		lItem{platform: "youtube", ext: "agile", title: "我们都只是在假装着做Agile", lang: "zh", channel: "zhchan", dur: ip(600),
			published: tp("2026-03-01T00:00:00Z"),
			stages:    map[string]string{StageMetadata: "done", StageTranscript: "failed", StageNormalize: "pending"},
			errClass:  map[string]string{StageTranscript: "permanent"}, timings: []string{"source", "interpolated"}},
		lItem{platform: "bilibili", ext: "BV1xx", title: "", stages: map[string]string{StageMetadata: "pending"}},
	)
	return map[string]store.Item{"pg": items[0], "agile": items[1], "bili": items[2]}
}

func ids(page ItemPage) []string {
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestListItemsFilters(t *testing.T) {
	e := newListEnv(t)
	fx := libraryFixture(e)
	pg, ag, bi := fx["pg"].ID, fx["agile"].ID, fx["bili"].ID
	// 建立時還沒有頻道；重新讀取才有 channel_id。
	pgItem, err := e.st.Read().ItemByID(context.Background(), pg)
	if err != nil || pgItem.ChannelID == "" {
		t.Fatalf("前置條件：影音應有頻道：%+v, %v", pgItem, err)
	}

	tests := []struct {
		name string
		q    ItemQuery
		want []string // 新到舊
	}{
		{"沒有條件", ItemQuery{}, []string{bi, ag, pg}},
		{"platform", ItemQuery{Platform: "bilibili"}, []string{bi}},
		{"channel", ItemQuery{Channel: pgItem.ChannelID}, []string{pg}},
		{"lang 不分大小寫", ItemQuery{Lang: "ZH"}, []string{ag}},
		{"error_class", ItemQuery{ErrorClass: "permanent"}, []string{ag}},
		{"error_class 沒有符合", ItemQuery{ErrorClass: "transient"}, nil},
		{"timing interpolated", ItemQuery{Timing: "interpolated"}, []string{ag}},
		{"timing source", ItemQuery{Timing: "source"}, []string{ag, pg}},
		{"timing 沒有符合", ItemQuery{Timing: "model"}, nil},
		{"published_from", ItemQuery{PublishedFrom: tp("2026-02-01T00:00:00Z")}, []string{ag}},
		{"published_to", ItemQuery{PublishedTo: tp("2026-02-01T00:00:00Z")}, []string{pg}},
		{"published 區間含邊界", ItemQuery{PublishedFrom: tp("2026-01-10T00:00:00Z"), PublishedTo: tp("2026-03-01T00:00:00Z")}, []string{ag, pg}},
		{"duration_min", ItemQuery{DurationMin: ip(101)}, []string{ag}},
		{"duration_max", ItemQuery{DurationMax: ip(100)}, []string{pg}},
		{"duration 區間", ItemQuery{DurationMin: ip(100), DurationMax: ip(600)}, []string{ag, pg}},
		{"status", ItemQuery{Status: map[string][]string{StageNormalize: {"done"}}}, []string{pg}},
		{"status 同步驟 OR", ItemQuery{Status: map[string][]string{StageNormalize: {"done", "pending"}}}, []string{bi, ag, pg}},
		{"status 不同步驟 AND", ItemQuery{Status: map[string][]string{StageMetadata: {"done"}, StageTranscript: {"failed"}}}, []string{ag}},
		{"status AND 沒有符合", ItemQuery{Status: map[string][]string{StageMetadata: {"done"}, StageTranscript: {"pending"}}}, nil},
		{"status 沒有 run 的步驟視為 pending", ItemQuery{Status: map[string][]string{StageNormalize: {"pending"}}}, []string{bi, ag}},
		{"AND：platform + status", ItemQuery{Platform: "youtube", Status: map[string][]string{StageNormalize: {"done"}}}, []string{pg}},
		{"AND：platform + lang 沒有符合", ItemQuery{Platform: "bilibili", Lang: "en"}, nil},
		{"AND：q + timing", ItemQuery{Q: "agile", Timing: "interpolated"}, []string{ag}},
		{"AND：q + timing 沒有符合", ItemQuery{Q: "postgresql", Timing: "interpolated"}, nil},
		{"q 小寫", ItemQuery{Q: "postgresql"}, []string{pg}},
		{"q 大寫", ItemQuery{Q: "POSTGRESQL"}, []string{pg}},
		{"q 大小寫混合", ItemQuery{Q: "PostgreSQL"}, []string{pg}},
		{"q 英文部分字串", ItemQuery{Q: "Agile"}, []string{ag}},
		{"q 中文 2 字", ItemQuery{Q: "假装"}, []string{ag}},
		{"q 中文 1 字", ItemQuery{Q: "装"}, []string{ag}},
		{"q 中文與英文相連", ItemQuery{Q: "做agile"}, []string{ag}},
		{"q 簡繁不互通", ItemQuery{Q: "假裝"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := e.p.ListItems(context.Background(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(page); !slices.Equal(got, nonNilStrings(tt.want)) {
				t.Errorf("items = %v，預期 %v", got, tt.want)
			}
			if page.Total != len(tt.want) {
				t.Errorf("total = %d，預期 %d", page.Total, len(tt.want))
			}
			if page.Next != nil {
				t.Errorf("單頁就能放下，不該有 Next：%+v", page.Next)
			}
		})
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// q 中的 %、_、' 照字面比對，不是萬用字元，也不會破壞 SQL。
func TestListItemsQueryIsLiteral(t *testing.T) {
	e := newListEnv(t)
	items := e.seed(
		lItem{platform: "youtube", ext: "a", title: "100% real"},
		lItem{platform: "youtube", ext: "b", title: "snake_case tips"},
		lItem{platform: "youtube", ext: "c", title: "it's fine"},
		lItem{platform: "youtube", ext: "d", title: "plain title"},
	)
	tests := []struct {
		q    string
		want string
	}{
		{"%", items[0].ID}, {"_", items[1].ID}, {"'", items[2].ID},
		{"100%", items[0].ID}, {"e_c", items[1].ID}, {"it's", items[2].ID},
		{"' OR 1=1 --", ""}, {"\\", ""},
	}
	for _, tt := range tests {
		page, err := e.p.ListItems(context.Background(), ItemQuery{Q: tt.q})
		if err != nil {
			t.Fatalf("q=%q：%v", tt.q, err)
		}
		var want []string
		if tt.want != "" {
			want = []string{tt.want}
		}
		if got := ids(page); !slices.Equal(got, nonNilStrings(want)) || page.Total != len(want) {
			t.Errorf("q=%q：items=%v total=%d，預期 %v", tt.q, got, page.Total, want)
		}
	}
}

// 步驟的狀態取最新的一次 run；重跑之後舊的 run 不再計入。
func TestListItemsStatusUsesLatestRun(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(lItem{platform: "youtube", ext: "a", title: "A", stages: done(StageNormalize)})[0]
	f := ItemQuery{Status: map[string][]string{StageNormalize: {"done"}}}
	if page, _ := e.p.ListItems(context.Background(), f); page.Total != 1 {
		t.Fatalf("重跑前 total = %d", page.Total)
	}
	e.clk.Advance(time.Minute)
	if _, err := e.st.Write().InsertStageRun(context.Background(), store.NewStageRun{
		ItemID: it.ID, Stage: StageNormalize, Status: store.StatusPending, ConfigSnapshotID: e.snap,
	}); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.p.ListItems(context.Background(), f); page.Total != 0 {
		t.Errorf("重跑後最新的是 pending，done 不該符合：total = %d", page.Total)
	}
	f.Status[StageNormalize] = []string{"pending"}
	if page, _ := e.p.ListItems(context.Background(), f); page.Total != 1 {
		t.Errorf("重跑後 pending 應符合：total = %d", page.Total)
	}
	// 同一毫秒建立的兩筆 run 以 rowid 決定哪一筆是最新的。
	e2 := newListEnv(t)
	it2 := e2.seed(lItem{platform: "youtube", ext: "b", title: "B"})[0]
	for _, st := range []string{store.StatusFailed, store.StatusDone} {
		if _, err := e2.st.Write().InsertStageRun(context.Background(), store.NewStageRun{
			ItemID: it2.ID, Stage: StageTranscript, Status: st, ConfigSnapshotID: e2.snap,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if page, _ := e2.p.ListItems(context.Background(), ItemQuery{Status: map[string][]string{StageTranscript: {"done"}}}); page.Total != 1 {
		t.Errorf("同一毫秒時最後插入的 run 為最新：total = %d", page.Total)
	}
}

// 沒有任何 run 的步驟視為 pending（LIB 規格待決問題）：只處理到 metadata 的影音，
// normalize:pending 要列出它，normalize:done 不列出；列表與 total 一致。
func TestListItemsStageWithoutRunIsPending(t *testing.T) {
	e := newListEnv(t)
	meta := e.seed(lItem{platform: "youtube", ext: "m", title: "M", stages: done(StageMetadata)})[0]
	full := e.seed(lItem{platform: "youtube", ext: "f", title: "F", stages: done(StageMetadata, StageNormalize)})[0]
	tests := []struct {
		name   string
		status map[string][]string
		want   []string
	}{
		{"normalize:pending", map[string][]string{StageNormalize: {"pending"}}, []string{meta.ID}},
		{"normalize:done", map[string][]string{StageNormalize: {"done"}}, []string{full.ID}},
		{"normalize:done 或 pending", map[string][]string{StageNormalize: {"done", "pending"}}, []string{full.ID, meta.ID}},
		{"normalize:failed", map[string][]string{StageNormalize: {"failed"}}, nil},
		{"metadata:done 且 normalize:pending", map[string][]string{StageMetadata: {"done"}, StageNormalize: {"pending"}}, []string{meta.ID}},
	}
	for _, tt := range tests {
		page, err := e.p.ListItems(context.Background(), ItemQuery{Status: tt.status})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(page); !slices.Equal(got, nonNilStrings(tt.want)) || page.Total != len(tt.want) {
			t.Errorf("%s：items=%v total=%d，預期 %v", tt.name, got, page.Total, tt.want)
		}
	}
}

// timing 只看目前（未被取代）的 transcript 產出。
func TestListItemsTimingUsesCurrentArtifact(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(lItem{platform: "youtube", ext: "a", title: "A", timings: []string{"interpolated", "source"}})[0]
	f := ItemQuery{Timing: "interpolated"}
	if page, _ := e.p.ListItems(context.Background(), f); page.Total != 1 {
		t.Fatalf("取代前 total = %d", page.Total)
	}
	// 同一組 (kind, variant, lang) 的新版本取代舊版本；新版本沒有 interpolated。
	e.addTranscript(context.Background(), e.st.Write(), it.ID, []string{"source"})
	if page, _ := e.p.ListItems(context.Background(), f); page.Total != 0 {
		t.Errorf("舊版本已被取代，不該符合：total = %d", page.Total)
	}
}

func TestCueTimingsSortedAndDistinct(t *testing.T) {
	cues := []subtitle.Cue{
		{Timing: subtitle.TimingSource}, {Timing: subtitle.TimingInterpolated}, {Timing: subtitle.TimingSource}, {Timing: ""},
		{Timing: subtitle.TimingMerged}, {Timing: subtitle.TimingInterpolated},
	}
	if got, want := cueTimings(cues), []string{"interpolated", "merged", "source"}; !slices.Equal(got, want) {
		t.Errorf("cueTimings = %v，預期 %v", got, want)
	}
	if got := cueTimings(nil); len(got) != 0 {
		t.Errorf("沒有 cue：%v", got)
	}
}

// newCuesArtifact 寫入 timings，列表可以依它篩選。
func TestNewCuesArtifactRecordsTimings(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(lItem{platform: "youtube", ext: "a", title: "A"})[0]
	art, err := e.p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource, Cues: []subtitle.Cue{
		{Index: 1, StartMS: 0, EndMS: 1000, Text: "a", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 1000, EndMS: 2000, Text: "b", Timing: subtitle.TimingSilence},
	}}, nil, json.RawMessage(`{"source":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(art.Timings, []string{"silence", "source"}) {
		t.Fatalf("Timings = %v", art.Timings)
	}
	if _, err := e.st.Write().InsertArtifact(context.Background(), *art); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Timing: "silence"}); page.Total != 1 {
		t.Errorf("timing=silence total = %d", page.Total)
	}
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Timing: "interpolated"}); page.Total != 0 {
		t.Errorf("timing=interpolated total = %d", page.Total)
	}
}

// 標題只在有值時更新 title_fold；空標題不清掉原值。
func TestUpdateItemMetaWritesTitleFold(t *testing.T) {
	e := newListEnv(t)
	it := e.seed(lItem{platform: "youtube", ext: "a", title: "Hello WORLD"})[0]
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Q: "hello world"}); page.Total != 1 {
		t.Fatalf("total = %d", page.Total)
	}
	if err := e.st.Write().UpdateItemMeta(context.Background(), it.ID, store.ItemMeta{Title: ""}); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Q: "world"}); page.Total != 1 {
		t.Errorf("空標題不該清掉 title_fold：total = %d", page.Total)
	}
	if err := e.st.Write().UpdateItemMeta(context.Background(), it.ID, store.ItemMeta{Title: "Renamed ÄÖ"}); err != nil {
		t.Fatal(err)
	}
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Q: "world"}); page.Total != 0 {
		t.Errorf("改名後舊標題不該符合：total = %d", page.Total)
	}
	// 非 ASCII 的大小寫也要符合（SQLite 的 lower() 做不到）。
	if page, _ := e.p.ListItems(context.Background(), ItemQuery{Q: "äö"}); page.Total != 1 {
		t.Errorf("非 ASCII 大小寫：total = %d", page.Total)
	}
}

// limit 生效、keyset 走完所有資料、最後一頁沒有 Next、total 不受分頁影響。
func TestListItemsPaging(t *testing.T) {
	e := newListEnv(t)
	var specs []lItem
	for i := range 7 {
		specs = append(specs, lItem{platform: "youtube", ext: fmt.Sprintf("v%d", i), title: fmt.Sprintf("T%d", i)})
	}
	items := e.seed(specs...)
	// 讓最後兩筆有相同的 created_at，驗證同一毫秒時依 id 排序且不漏不重。
	_, err := e.st.Write().InsertStageRun(context.Background(), store.NewStageRun{ItemID: items[0].ID, Stage: StageMetadata, Status: store.StatusDone, ConfigSnapshotID: e.snap})
	if err != nil {
		t.Fatal(err)
	}

	var all []string
	var cur *ItemCursor
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("分頁沒有結束")
		}
		page, err := e.p.ListItems(context.Background(), ItemQuery{Limit: 3, After: cur})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 7 {
			t.Errorf("第 %d 頁 total = %d，預期 7", pages, page.Total)
		}
		if len(page.Items) > 3 {
			t.Fatalf("limit 沒有生效：%d 筆", len(page.Items))
		}
		all = append(all, ids(page)...)
		if page.Next == nil {
			if len(page.Items) == 0 {
				t.Error("最後一頁不該是空的")
			}
			break
		}
		if len(page.Items) != 3 {
			t.Errorf("有下一頁時這頁要滿 3 筆：%d", len(page.Items))
		}
		cur = page.Next
	}
	want := make([]string, 0, 7)
	for i := len(items) - 1; i >= 0; i-- {
		want = append(want, items[i].ID)
	}
	if !slices.Equal(all, want) {
		t.Errorf("分頁結果 = %v\n預期 %v", all, want)
	}
}

// 相同 created_at 的影音依 id 由大到小，分頁邊界落在其中也不漏不重。
func TestListItemsPagingSameTimestamp(t *testing.T) {
	e := newListEnv(t)
	var made []store.Item
	err := e.st.Tx(context.Background(), func(tx *store.Repo) error {
		for i := range 5 {
			it, _, err := tx.InsertItemIfAbsent(context.Background(), store.NewItem{Platform: "youtube", ExternalID: fmt.Sprintf("s%d", i), URL: "u"})
			if err != nil {
				return err
			}
			made = append(made, it)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(made))
	for _, it := range made {
		want = append(want, it.ID)
	}
	slices.Sort(want)
	slices.Reverse(want)

	var got []string
	var cur *ItemCursor
	for range 10 {
		page, err := e.p.ListItems(context.Background(), ItemQuery{Limit: 2, After: cur})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(page)...)
		if page.Next == nil {
			break
		}
		cur = page.Next
	}
	if !slices.Equal(got, want) {
		t.Errorf("同一毫秒的排序 = %v\n預期 %v", got, want)
	}
}

func TestListItemsLimitDefaultsAndClamps(t *testing.T) {
	e := newListEnv(t)
	var specs []lItem
	for i := range 55 {
		specs = append(specs, lItem{platform: "youtube", ext: fmt.Sprintf("v%d", i)})
	}
	e.seed(specs...)
	page, err := e.p.ListItems(context.Background(), ItemQuery{})
	if err != nil || len(page.Items) != DefaultItemLimit || page.Next == nil || page.Total != 55 {
		t.Errorf("預設 limit：%d 筆 next=%v total=%d err=%v", len(page.Items), page.Next, page.Total, err)
	}
	page, err = e.p.ListItems(context.Background(), ItemQuery{Limit: 1000})
	if err != nil || len(page.Items) != 55 || page.Next != nil {
		t.Errorf("limit 超過上限：%d 筆 next=%v err=%v", len(page.Items), page.Next, err)
	}
}

func TestListItemsValidate(t *testing.T) {
	e := newListEnv(t)
	bad := []struct {
		name string
		q    ItemQuery
	}{
		{"platform", ItemQuery{Platform: "vimeo"}},
		{"stage", ItemQuery{Status: map[string][]string{"bogus": {"done"}}}},
		{"status", ItemQuery{Status: map[string][]string{StageNormalize: {"bogus"}}}},
		{"timing", ItemQuery{Timing: "bogus"}},
		{"duration_min", ItemQuery{DurationMin: ip(-1)}},
		{"duration_max", ItemQuery{DurationMax: ip(-1)}},
		{"q 太長", ItemQuery{Q: strings.Repeat("字", MaxQueryLen+1)}},
	}
	for _, tt := range bad {
		_, err := e.p.ListItems(context.Background(), tt.q)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s：預期 InvalidError，得到 %v", tt.name, err)
		}
	}
	if _, err := e.p.ListItems(context.Background(), ItemQuery{Q: strings.Repeat("字", MaxQueryLen)}); err != nil {
		t.Errorf("剛好 %d 個字元應該可以：%v", MaxQueryLen, err)
	}
}

// 10k 筆影音的列表耗時，對照 NFR-PERF-1（200ms）。
func BenchmarkListItems10k(b *testing.B) {
	e := newListEnv(b)
	const n = 10000
	err := e.st.Tx(context.Background(), func(tx *store.Repo) error {
		ctx := context.Background()
		for i := range n {
			it, _, err := tx.InsertItemIfAbsent(ctx, store.NewItem{Platform: []string{"youtube", "bilibili"}[i%2], ExternalID: fmt.Sprintf("v%05d", i), URL: "u"})
			if err != nil {
				return err
			}
			title := fmt.Sprintf("Video %d about PostgreSQL 與 Agile", i)
			// lib-10k：500 個頻道（LIB-R4 的分組）。
			chID, err := tx.UpsertChannel(ctx, store.Channel{Platform: "youtube", ExternalID: fmt.Sprintf("ch%03d", i%500), Title: fmt.Sprintf("Channel %03d", i%500)})
			if err != nil {
				return err
			}
			pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i%900) * time.Hour)
			if err := tx.UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: title, DurationS: ip(i % 3600), OriginalLang: []string{"en", "zh"}[i%2], LangSource: "metadata",
				ChannelID: chID, PublishedAt: &pub}); err != nil {
				return err
			}
			for _, stage := range Stages {
				run, err := tx.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: e.snap})
				if err != nil {
					return err
				}
				if err := tx.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusDone}); err != nil {
					return err
				}
			}
			timings := []string{"source"}
			if i%10 == 0 {
				timings = append(timings, "interpolated")
			}
			if _, err := tx.InsertArtifact(ctx, store.NewArtifact{ItemID: it.ID, Kind: "transcript", Variant: "source", Lang: "en", Format: "cues",
				Path: "x", SHA256: "s", Provenance: json.RawMessage(`{"a":1}`), Timings: timings}); err != nil {
				return err
			}
			if i%7 == 0 {
				e.clk.Advance(time.Millisecond)
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}

	cases := []struct {
		name string
		q    ItemQuery
	}{
		{"first_page", ItemQuery{Limit: 50}},
		{"max_page", ItemQuery{Limit: 200}},
		{"platform", ItemQuery{Platform: "youtube", Limit: 50}},
		{"status", ItemQuery{Status: map[string][]string{StageNormalize: {"done"}, StageTranscript: {"done", "failed"}}, Limit: 50}},
		{"q", ItemQuery{Q: "agile", Limit: 50}},
		{"q_rare", ItemQuery{Q: "video 9999 ", Limit: 50}},
		{"timing", ItemQuery{Timing: "interpolated", Limit: 50}},
		{"all_filters", ItemQuery{Platform: "youtube", Lang: "en", Timing: "interpolated", Q: "postgresql",
			Status: map[string][]string{StageNormalize: {"done"}}, DurationMin: ip(10), Limit: 50}},
		{"group_channel", ItemQuery{GroupBy: store.GroupChannel, Limit: 200}},
		{"group_published", ItemQuery{GroupBy: store.GroupPublished, Limit: 200}},
		{"group_status", ItemQuery{GroupBy: store.GroupStatus, Limit: 200}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				page, err := e.p.ListItems(context.Background(), c.q)
				if err != nil {
					b.Fatal(err)
				}
				if page.Total == 0 && c.name != "q_rare" {
					b.Fatal("沒有資料")
				}
			}
		})
	}
	// 深頁：游標在最舊的一端，檢查 keyset 不會變慢。
	b.Run("deep_page", func(b *testing.B) {
		first, err := e.p.ListItems(context.Background(), ItemQuery{Limit: 200})
		if err != nil {
			b.Fatal(err)
		}
		cur := first.Next
		for range 40 {
			page, err := e.p.ListItems(context.Background(), ItemQuery{Limit: 200, After: cur})
			if err != nil || page.Next == nil {
				b.Fatal(err)
			}
			cur = page.Next
		}
		b.ResetTimer()
		for b.Loop() {
			if _, err := e.p.ListItems(context.Background(), ItemQuery{Limit: 50, After: cur}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
