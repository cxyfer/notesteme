package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/search"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// writeCues 把字幕寫成產出檔並新增產出列，回傳產出。
func writeCues(t *testing.T, s *Store, itemID, variant, lang string, texts ...string) Artifact {
	t.Helper()
	cues := make([]subtitle.Cue, len(texts))
	for i, tx := range texts {
		cues[i] = subtitle.Cue{Index: i + 1, StartMS: int64(i) * 1000, EndMS: int64(i+1) * 1000, Text: tx, Timing: subtitle.TimingSource}
	}
	var buf bytes.Buffer
	if err := subtitle.RenderCuesJSON(&buf, subtitle.Transcript{Lang: lang, Variant: subtitle.Variant(variant), Cues: cues}); err != nil {
		t.Fatal(err)
	}
	id := NewID()
	rel := filepath.ToSlash(filepath.Join("artifacts", itemID, id+".json"))
	abs := filepath.Join(s.dataDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	var a Artifact
	err := s.Tx(context.Background(), func(r *Repo) error {
		var err error
		a, err = r.InsertArtifact(context.Background(), NewArtifact{
			ID: id, ItemID: itemID, Kind: "transcript", Variant: variant, Lang: lang, Format: "notesteme-cues+json",
			Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(buf.Len()), Provenance: json.RawMessage(`{"stage":"test"}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func runSearch(t *testing.T, s *Store, q, lang string, limit int, after *SearchCursor) SearchPage {
	t.Helper()
	page, err := s.Read().Search(context.Background(), SearchQuery{Folded: search.Fold(q), Lang: lang, Limit: limit, After: after})
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return page
}

type hitKey struct {
	Item, Lang, Variant string
	Cue                 int
}

func keys(p SearchPage) []hitKey {
	out := []hitKey{}
	for _, h := range p.Hits {
		out = append(out, hitKey{h.ItemID, h.Lang, h.Variant, h.Cue})
	}
	return out
}

func checkIndex(t *testing.T, s *Store, itemID string, want int) {
	t.Helper()
	n, err := s.SearchIndexCheck(context.Background(), itemID)
	if err != nil || n != want {
		t.Fatalf("索引列數 = %d, %v；want %d", n, err, want)
	}
}

func TestSearchIndexKeepsOneVariantPerLanguage(t *testing.T) {
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	writeCues(t, s, it.ID, "source", "en", "the quick brown fox")
	if p := runSearch(t, s, "brown", "", 0, nil); p.Total != 1 || p.Hits[0].Variant != "source" {
		t.Fatalf("source：%+v", keys(p))
	}
	writeCues(t, s, it.ID, "normalized", "en", "the quick brown fox", "jumps")
	writeCues(t, s, it.ID, "corrected", "en", "The quick red fox.")
	if p := runSearch(t, s, "brown", "", 0, nil); p.Total != 0 {
		t.Fatalf("順位較低的變體仍在索引中：%+v", keys(p))
	}
	// 順位較低的新版本不會取代順位較高的。
	writeCues(t, s, it.ID, "segmented", "en", "brown again")
	if p := runSearch(t, s, "red fox", "", 0, nil); p.Total != 1 || p.Hits[0].Variant != "corrected" {
		t.Fatalf("corrected：%+v", keys(p))
	}
	if p := runSearch(t, s, "brown", "", 0, nil); p.Total != 0 {
		t.Fatalf("segmented 不應被索引：%+v", keys(p))
	}
	// 譯文是另一個語言，各自索引；edited 優先於 translated。
	writeCues(t, s, it.ID, "translated", "zh-Hant", "敏捷的紅狐狸")
	if p := runSearch(t, s, "紅狐狸", "zh-Hant", 0, nil); p.Total != 1 {
		t.Fatalf("譯文：%+v", keys(p))
	}
	writeCues(t, s, it.ID, "edited", "zh-Hant", "敏捷的赤狐")
	if p := runSearch(t, s, "紅狐狸", "", 0, nil); p.Total != 0 {
		t.Fatalf("被 edited 取代的譯文仍在索引中：%+v", keys(p))
	}
	if p := runSearch(t, s, "赤狐", "en", 0, nil); p.Total != 0 {
		t.Fatalf("lang 篩選沒有作用：%+v", keys(p))
	}
	checkIndex(t, s, it.ID, 2)
}

func TestSearchIndexFollowsRestoreAndWithdraw(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	first := writeCues(t, s, it.ID, "corrected", "en", "alpha version")
	writeCues(t, s, it.ID, "corrected", "en", "beta version")
	if p := runSearch(t, s, "alpha", "", 0, nil); p.Total != 0 {
		t.Fatal("被取代的版本仍在索引中")
	}
	if err := s.Tx(ctx, func(r *Repo) error { return r.RestoreArtifact(ctx, first.ID) }); err != nil {
		t.Fatal(err)
	}
	if p := runSearch(t, s, "alpha", "", 0, nil); p.Total != 1 {
		t.Fatal("恢復的版本沒有回到索引")
	}
	if p := runSearch(t, s, "beta", "", 0, nil); p.Total != 0 {
		t.Fatal("恢復之後舊的現行版本仍在索引中")
	}
	writeCues(t, s, it.ID, "translated", "fr", "version gamma")
	if err := s.Tx(ctx, func(r *Repo) error {
		return r.WithdrawArtifacts(ctx, it.ID, "transcript", "translated", []string{"fr"})
	}); err != nil {
		t.Fatal(err)
	}
	if p := runSearch(t, s, "gamma", "", 0, nil); p.Total != 0 {
		t.Fatal("撤下的譯文仍在索引中")
	}
	checkIndex(t, s, it.ID, 1)
}

func TestSearchIndexRollsBackWithTheWrite(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	a := writeCues(t, s, it.ID, "corrected", "en", "old text")
	b := writeCues(t, s, it.ID, "corrected", "en", "new text")
	err := s.Tx(ctx, func(r *Repo) error {
		if err := r.RestoreArtifact(ctx, a.ID); err != nil {
			return err
		}
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("交易應該失敗")
	}
	if p := runSearch(t, s, "new text", "", 0, nil); p.Total != 1 || p.Hits[0].Variant != "corrected" {
		t.Fatalf("rollback 之後索引改變了：%+v（%s）", keys(p), b.ID)
	}
	if p := runSearch(t, s, "old text", "", 0, nil); p.Total != 0 {
		t.Fatal("rollback 之後舊內容出現在索引中")
	}
}

func TestSearchSkipsUnreadableArtifacts(t *testing.T) {
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	a := writeCues(t, s, it.ID, "corrected", "en", "hello world")
	// 內容被改動（sha256 不符）的產出不建立索引列，也不讓寫入失敗。
	if err := os.WriteFile(filepath.Join(s.dataDir(), filepath.FromSlash(a.Path)), []byte(`{"cues":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(context.Background(), func(r *Repo) error { return r.RebuildSearchIndex(context.Background()) }); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, s, it.ID, 0)
	// 檔案不存在的產出同樣略過（既有測試會寫入沒有檔案的產出）。
	if _, err := s.Write().InsertArtifact(context.Background(), NewArtifact{ItemID: it.ID, Kind: "transcript", Variant: "edited", Lang: "en",
		Format: "notesteme-cues+json", Path: "artifacts/missing.json", SHA256: "x", Provenance: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, s, it.ID, 0)
}

func TestSearchExcludesTrashAndPurgeRemovesRows(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a, _ := seedItem(t, s, "a")
	b, _ := seedItem(t, s, "b")
	writeCues(t, s, a.ID, "corrected", "en", "shared phrase one", "x")
	writeCues(t, s, b.ID, "corrected", "en", "shared phrase two")
	if _, _, err := s.Write().TrashItem(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"shared", "sh"} { // 兩條路徑
		if p := runSearch(t, s, q, "", 0, nil); p.Total != 1 || p.Hits[0].ItemID != b.ID {
			t.Fatalf("%q：垃圾桶中的影音出現在結果中：%+v", q, keys(p))
		}
	}
	if _, _, err := s.Write().RestoreItem(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if p := runSearch(t, s, "shared", "", 0, nil); p.Total != 2 {
		t.Fatalf("還原之後：%+v", keys(p))
	}
	if _, _, err := s.Write().TrashItem(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write().PurgeItem(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, s, a.ID, 0)
	checkIndex(t, s, b.ID, 1)
}

func TestSearchOrderingBeforeAfterAndPaging(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	old, _ := seedItem(t, s, "old")
	clk.Advance(time.Second)
	newer, _ := seedItem(t, s, "new")
	// 原文語言是 en：en 在前，其他語言依代碼排序。
	for _, it := range []Item{old, newer} {
		if err := s.Write().UpdateItemMeta(ctx, it.ID, ItemMeta{OriginalLang: "en", LangSource: "metadata"}); err != nil {
			t.Fatal(err)
		}
	}
	writeCues(t, s, old.ID, "corrected", "en", "kafka one", "nothing", "kafka three")
	writeCues(t, s, newer.ID, "translated", "de", "kafka de")
	writeCues(t, s, newer.ID, "translated", "ab", "kafka ab")
	writeCues(t, s, newer.ID, "corrected", "en", "intro", "Kafka two")

	all := runSearch(t, s, "kafka", "", 0, nil)
	want := []hitKey{
		{newer.ID, "en", "corrected", 2}, {newer.ID, "ab", "translated", 1}, {newer.ID, "de", "translated", 1},
		{old.ID, "en", "corrected", 1}, {old.ID, "en", "corrected", 3},
	}
	if !reflect.DeepEqual(keys(all), want) || all.Total != 5 || all.Next != nil {
		t.Fatalf("排序 = %+v（total %d）", keys(all), all.Total)
	}
	h := all.Hits[0]
	if h.Before == nil || *h.Before != "intro" || h.After != nil || h.StartMS != 1000 || h.EndMS != 2000 {
		t.Fatalf("前後文或時間不正確：%+v", h)
	}
	// 以游標逐頁讀，結果與一次讀完相同。
	var got []hitKey
	var after *SearchCursor
	for i := 0; i < 10; i++ {
		p := runSearch(t, s, "kafka", "", 2, after)
		if p.Total != 5 {
			t.Fatalf("total = %d", p.Total)
		}
		got = append(got, keys(p)...)
		if p.Next == nil {
			break
		}
		after = p.Next
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("分頁 = %+v", got)
	}
	// 游標在最後一筆（例如結果在兩次請求之間變少）：沒有結果，total 仍是全部的數量。
	last := all.Hits[len(all.Hits)-1].Key
	if p := runSearch(t, s, "kafka", "", 2, &last); len(p.Hits) != 0 || p.Total != 5 || p.Next != nil {
		t.Fatalf("最後一筆之後 = %+v（total %d）", keys(p), p.Total)
	}
}

// TestSearchPathsAgree 是差分測試（SRCH-R3）：同一個語料，3 個字以上的查詢走 trigram 索引與走 instr 掃描，
// 結果與排序必須完全相同。
func TestSearchPathsAgree(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("abAB 索引資料庫𠮷İi\"*()-:^")
	word := func(n int) string {
		r := make([]rune, n)
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(r)
	}
	for i := 0; i < 6; i++ {
		it, _ := seedItem(t, s, fmt.Sprintf("p%d", i))
		var texts []string
		for j := 0; j < 30; j++ {
			texts = append(texts, word(3+rng.Intn(20)))
		}
		writeCues(t, s, it.ID, "corrected", "en", texts...)
	}
	scan := func(folded string) []hitKey {
		rows, err := s.r.QueryContext(ctx, `SELECT sc.item_id, sc.lang, sc.variant, sc.cue FROM search_cues sc JOIN items i ON i.id = sc.item_id
WHERE instr(sc.text_fold, ?) > 0 AND i.deleted_at IS NULL ORDER BY i.created_at DESC, i.id, sc.lang, sc.cue`, folded)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []hitKey{}
		for rows.Next() {
			var k hitKey
			if err := rows.Scan(&k.Item, &k.Lang, &k.Variant, &k.Cue); err != nil {
				t.Fatal(err)
			}
			out = append(out, k)
		}
		return out
	}
	hits := 0
	for i := 0; i < 300; i++ {
		q := word(3 + rng.Intn(3))
		if strings.TrimSpace(q) != q {
			continue
		}
		folded := search.Fold(q)
		p := runSearch(t, s, q, "", search.MaxLimit, nil)
		if want := scan(folded); !reflect.DeepEqual(keys(p), want[:min(len(want), search.MaxLimit)]) || p.Total != len(want) {
			t.Fatalf("查詢 %q：索引 %+v（%d），掃描 %+v", q, keys(p), p.Total, want)
		}
		hits += p.Total
	}
	if hits == 0 {
		t.Fatal("差分測試沒有任何命中，語料或查詢的產生方式要調整")
	}
}

func TestSearchIndexBuiltOnFirstOpen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	s, err := Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	it, _ := seedItem(t, s, "a")
	writeCues(t, s, it.ID, "corrected", "en", "backfill me")
	// 模擬 migration 剛建立索引：清空索引列並把 built 設回 0。
	if _, err := s.w.ExecContext(ctx, `DELETE FROM search_cues; UPDATE search_index_state SET built = 0`); err != nil {
		t.Fatal(err)
	}
	checkIndex(t, s, it.ID, 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkIndex(t, s, it.ID, 1)
	if p := runSearch(t, s, "backfill", "", 0, nil); p.Total != 1 {
		t.Fatal("第一次開啟沒有建立索引")
	}
}

func TestSeedItemCreatedAt(t *testing.T) {
	s, _ := openTest(t)
	if err := s.SeedItemCreatedAt(context.Background(), "nope", time.Now()); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	it, _ := seedItem(t, s, "a")
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SeedItemCreatedAt(context.Background(), it.ID, at); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read().ItemByID(context.Background(), it.ID)
	if !got.CreatedAt.Equal(at) {
		t.Fatalf("created_at = %v", got.CreatedAt)
	}
}
