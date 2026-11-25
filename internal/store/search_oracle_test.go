package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/search"
)

// 這個檔案是 NFR-PERF-9 第二輪最佳化（SRCH S9）的差分測試：oracleSearch 是改寫之前的 Search，原封不動
// （每個命中句 join items、COUNT(*) OVER ()、排序），新的 Search 對同一個語料的每一頁結果、total 與 next_cursor
// 都必須與它完全相同。

const oracleTranslationExpr = `(CASE WHEN sc.lang = COALESCE(i.original_lang, '') THEN 0 ELSE 1 END)`

func oracleSearch(r *Repo, ctx context.Context, q SearchQuery) (SearchPage, error) {
	db := r.s.r
	var where []string
	var args []any
	if search.UseIndex(q.Folded) {
		where = append(where, `sc.id IN (SELECT rowid FROM cues_fts WHERE cues_fts MATCH ?)`)
		args = append(args, search.Phrase(q.Folded))
	} else {
		where = append(where, `instr(sc.text_fold, ?) > 0`)
		args = append(args, q.Folded)
	}
	where = append(where, `i.deleted_at IS NULL`)
	if q.Lang != "" {
		where = append(where, `sc.lang = ?`)
		args = append(args, q.Lang)
	}
	from := ` FROM search_cues sc JOIN items i ON i.id = sc.item_id WHERE ` + strings.Join(where, ` AND `)

	// 只掃一次符合的字幕句（NFR-PERF-9）：m 列出每個符合的句子與排序鍵，COUNT(*) OVER () 是總數（在游標過濾之前，
	// 所以每一頁的 total 都相同）；p 依游標過濾、排序、取一頁；最後才為這一頁的列讀取內容與前後句。
	// 原本 COUNT 與分頁各掃一次，2 字以下的掃描路徑等於掃兩次整個索引表。
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	inner := `SELECT sc.id AS sid, i.created_at AS created_at, i.id AS iid, ` + oracleTranslationExpr + ` AS tr, sc.lang AS lang, sc.cue AS cue,
  COUNT(*) OVER () AS total` + from
	cursor := ""
	pageArgs := slices.Clone(args)
	if q.After != nil {
		cursor = ` WHERE (-m.created_at, m.iid, m.tr, m.lang, m.cue) > (?, ?, ?, ?, ?)`
		a := q.After
		pageArgs = append(pageArgs, -a.CreatedAtMs, a.ItemID, a.Translation, a.Lang, a.Cue)
	}
	rows, err := db.QueryContext(ctx, `SELECT p.total, sc.item_id, i.platform, i.external_id, i.part, i.title, i.created_at,
  sc.lang, sc.variant, sc.cue, sc.start_ms, sc.end_ms, sc.text,
  (SELECT b.text FROM search_cues b WHERE b.artifact_id = sc.artifact_id AND b.cue = sc.cue - 1),
  (SELECT n.text FROM search_cues n WHERE n.artifact_id = sc.artifact_id AND n.cue = sc.cue + 1),
  p.tr
FROM (SELECT * FROM (`+inner+`) m`+cursor+` ORDER BY m.created_at DESC, m.iid, m.tr, m.lang, m.cue LIMIT ?) p
JOIN search_cues sc ON sc.id = p.sid JOIN items i ON i.id = p.iid
ORDER BY p.created_at DESC, p.iid, p.tr, p.lang, p.cue`, append(pageArgs, limit+1)...)
	if err != nil {
		return SearchPage{}, err
	}
	var page SearchPage
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		var before, after sql.NullString
		if err := rows.Scan(&page.Total, &h.ItemID, &h.Platform, &h.ExternalID, &h.Part, &h.Title, &h.Key.CreatedAtMs,
			&h.Lang, &h.Variant, &h.Cue, &h.StartMS, &h.EndMS, &h.Text, &before, &after, &h.Key.Translation); err != nil {
			return SearchPage{}, err
		}
		h.Key.ItemID, h.Key.Lang, h.Key.Cue = h.ItemID, h.Lang, h.Cue
		if before.Valid {
			h.Before = &before.String
		}
		if after.Valid {
			h.After = &after.String
		}
		page.Hits = append(page.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return SearchPage{}, err
	}
	// 游標已經在最後一筆之後：這一頁沒有列可以帶出總數，另外數一次（只在翻過最後一頁時發生）。
	if len(page.Hits) == 0 && q.After != nil {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&page.Total); err != nil {
			return SearchPage{}, err
		}
	}
	if len(page.Hits) > limit {
		page.Hits = page.Hits[:limit]
		k := page.Hits[limit-1].Key
		page.Next = &k
	}
	return page, nil
}

// searchCorpus 建立隨機語料：多部影音（部分 created_at 相同）、原文與多個譯文語言、空字串的句子，
// 字幕中含有 U+001E、U+001F（search_text 的分隔字元）、Tab、引號與 ToLower 會改變長度的 İ。
func searchCorpus(t *testing.T, s *Store, rng *rand.Rand) []Item {
	t.Helper()
	ctx := context.Background()
	alphabet := []rune("abAB 索引資料庫𠮷İi\"\t\x1f\x1e.*")
	word := func(n int) string {
		r := make([]rune, n)
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(r)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var items []Item
	for i := 0; i < 14; i++ {
		it, _ := seedItem(t, s, fmt.Sprintf("o%02d", i))
		// 每兩部共用一個 created_at（排序改由影音 id 決定）。
		if err := s.SeedItemCreatedAt(ctx, it.ID, base.Add(time.Duration(i/2)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if i%3 != 0 {
			if err := s.Write().UpdateItemMeta(ctx, it.ID, ItemMeta{OriginalLang: "en", LangSource: "metadata"}); err != nil {
				t.Fatal(err)
			}
		}
		for li, l := range []struct{ variant, lang string }{{"corrected", "en"}, {"translated", "zh-Hant"}, {"translated", "ab"}, {"edited", "fr"}} {
			if li > 0 && rng.Intn(2) == 0 {
				continue
			}
			texts := make([]string, 1+rng.Intn(40))
			for j := range texts {
				if rng.Intn(10) > 0 {
					texts[j] = word(rng.Intn(14))
				}
			}
			writeCues(t, s, it.ID, l.variant, l.lang, texts...)
		}
		items = append(items, it)
	}
	// 一部影音的字幕之後換成新版本（舊 group 刪除、新的接在後面）。
	writeCues(t, s, items[3].ID, "edited", "en", "new aa", "bb ab", "\x1fab\x1f")
	return items
}

// compareWithOracle 對一組查詢逐頁比對新舊兩個實作；回傳所有查詢的命中總數。
func compareWithOracle(t *testing.T, s *Store, rng *rand.Rand, rounds int) int {
	t.Helper()
	ctx := context.Background()
	alphabet := []rune("abAB 索引資料庫𠮷İi\"\t.*")
	hits := 0
	for n := 0; n < rounds; n++ {
		r := make([]rune, 1+rng.Intn(4))
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		norm, err := search.NormalizeQuery(string(r))
		if err != nil {
			continue
		}
		q := SearchQuery{Folded: search.Fold(norm), Lang: []string{"", "", "en", "zh-Hant", "xx"}[rng.Intn(5)],
			Limit: []int{0, 1, 3, 7, 100}[rng.Intn(5)]}
		for pageNo := 0; ; pageNo++ {
			got, err := s.Read().Search(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			want, err := oracleSearch(s.Read(), ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("查詢 %q（lang %q、limit %d）第 %d 頁：\n新 %+v\n舊 %+v", q.Folded, q.Lang, q.Limit, pageNo, describe(got), describe(want))
			}
			if pageNo == 0 {
				hits += got.Total
			}
			if got.Next == nil {
				// 再多翻一頁：游標在最後一筆之後，沒有結果，total 不變。
				if len(got.Hits) > 0 {
					last := got.Hits[len(got.Hits)-1].Key
					q.After = &last
					g2, err := s.Read().Search(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					w2, err := oracleSearch(s.Read(), ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(g2, w2) {
						t.Fatalf("查詢 %q 最後一頁之後：新 %+v 舊 %+v", q.Folded, describe(g2), describe(w2))
					}
				}
				break
			}
			q.After = got.Next
		}
	}
	return hits
}

func describe(p SearchPage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d next=%+v hits=[", p.Total, p.Next)
	for _, h := range p.Hits {
		fmt.Fprintf(&b, "{%s %s %s #%d %q key=%+v} ", h.ItemID[len(h.ItemID)-4:], h.Lang, h.Variant, h.Cue, h.Text, h.Key)
	}
	return b.String() + "]"
}

func TestSearchMatchesOracle(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	rng := rand.New(rand.NewSource(7))
	items := searchCorpus(t, s, rng)
	checkIndex(t, s, items[0].ID, mustCount(t, s, items[0].ID))
	hits := compareWithOracle(t, s, rng, 150)
	// 原文語言改變（譯文／原文的排序在查詢時計算）、垃圾桶、永久刪除之後再比一次。
	if err := s.Write().UpdateItemMeta(ctx, items[1].ID, ItemMeta{OriginalLang: "zh-Hant", LangSource: "metadata"}); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 5} {
		if _, _, err := s.Write().TrashItem(ctx, items[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Write().PurgeItem(ctx, items[5].ID, nil); err != nil {
		t.Fatal(err)
	}
	hits += compareWithOracle(t, s, rng, 150)
	if hits == 0 {
		t.Fatal("差分測試沒有任何命中，語料或查詢的產生方式要調整")
	}
	checkIndex(t, s, items[0].ID, mustCount(t, s, items[0].ID))
}

func mustCount(t *testing.T, s *Store, itemID string) int {
	t.Helper()
	n, err := s.rq.CountSearchCuesForItem(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// TestSearchGroupsBuiltFromExistingIndex 模擬 migration 00038 之後的第一次開啟：search_groups 是空的、groups_built = 0，
// 開啟時從 search_cues 建立；id 不連續的那份字幕（舊版不應該產生，這裡手動弄亂）改由產出檔重建。
func TestSearchGroupsBuiltFromExistingIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	s, err := Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(11))
	items := searchCorpus(t, s, rng)
	var broken string
	if err := s.w.QueryRowContext(ctx, `SELECT artifact_id FROM search_groups WHERE cues >= 3 ORDER BY first_id LIMIT 1`).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `DELETE FROM search_groups; UPDATE search_index_state SET groups_built = 0;
UPDATE search_cues SET id = id + 100000 WHERE artifact_id = ? AND cue = 2`, broken); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var built, n int
	if err := s.r.QueryRowContext(ctx, `SELECT groups_built, (SELECT COUNT(*) FROM search_groups) FROM search_index_state`).Scan(&built, &n); err != nil {
		t.Fatal(err)
	}
	if built != 1 || n == 0 {
		t.Fatalf("groups_built = %d、search_groups = %d 列", built, n)
	}
	checkIndex(t, s, items[0].ID, mustCount(t, s, items[0].ID))
	if compareWithOracle(t, s, rng, 120) == 0 {
		t.Fatal("沒有任何命中")
	}
}

func TestMatchCues(t *testing.T) {
	var b strings.Builder
	texts := []string{"ab", "", "xab ab", "a\x1fb", "b", "aab"}
	for i, f := range texts {
		appendFold(&b, i, f)
	}
	fold := b.String()
	for _, q := range []string{"a", "ab", "b", "x", "zz", "a\x1eb"} {
		var got []int
		n := matchCues(fold, q, func(c int) bool { got = append(got, c); return true })
		var want []int
		for i, f := range texts {
			if strings.Contains(strings.ReplaceAll(f, "\x1f", "\x1e"), q) {
				want = append(want, i+1)
			}
		}
		if n != len(want) || !slices.Equal(got, want) {
			t.Fatalf("%q：%v（%d），want %v", q, got, n, want)
		}
	}
	// fn 回傳 false 時停止。
	if n := matchCues(fold, "a", func(int) bool { return false }); n != 1 {
		t.Fatalf("n = %d", n)
	}
}
