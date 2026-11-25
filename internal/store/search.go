package store

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/search"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// kindTranscript 是字幕產出的 kind；只有它會進全文搜尋的索引（SRCH）。
const kindTranscript = "transcript"

// dataDir 是資料目錄（DB 檔所在的目錄）；產出檔的路徑相對於它。
func (s *Store) dataDir() string { return filepath.Dir(s.path) }

// reindexSearch 重建一部影音某個語言的索引列（SRCH-R1、R2）：刪除舊的列，從這個語言現行的字幕產出中
// 選出順位最高的一份寫入。呼叫端在寫入產出的同一個交易中呼叫，所以交易失敗時索引也不變。
//
// 產出檔讀不到、sha256 不符或解析失敗時，這個語言不建立索引列，但不讓寫入失敗：
// 索引是衍生資料，字幕本身的錯誤由讀取字幕的路徑回報（SRCH S8）。
//
// 字幕句的 id 由這裡指定（SRCH S9）：一份字幕的句子是連續的 first_id … first_id+n-1，第 n 句在 first_id+n-1，
// 同時寫入 search_groups 與 search_text（各句 text_fold 接成一列，2 字以下的掃描用）。
func (r *Repo) reindexSearch(ctx context.Context, itemID, lang string) error {
	// search_text 隨 search_groups 以外鍵 cascade 刪除。
	if err := r.q.DeleteSearchGroupsForLang(ctx, sqlcdb.DeleteSearchGroupsForLangParams{ItemID: itemID, Lang: lang}); err != nil {
		return err
	}
	if err := r.q.DeleteSearchCuesForLang(ctx, sqlcdb.DeleteSearchCuesForLangParams{ItemID: itemID, Lang: lang}); err != nil {
		return err
	}
	rows, err := r.q.CurrentTranscriptsForLang(ctx, sqlcdb.CurrentTranscriptsForLangParams{ItemID: itemID, Lang: lang})
	if err != nil {
		return err
	}
	var best *sqlcdb.Artifact
	for i := range rows {
		if !search.Indexable(rows[i].Variant) {
			continue
		}
		// 同一個變體只會有一份現行版本；保險起見，同順位時取較晚建立的（rows 依建立順序）。
		if best == nil || !search.Better(best.Variant, rows[i].Variant) {
			best = &rows[i]
		}
	}
	if best == nil {
		return nil
	}
	tr, err := r.readCues(best.Path, best.Sha256)
	if err != nil || len(tr.Cues) == 0 {
		return nil
	}
	first, err := r.q.NextSearchCueID(ctx)
	if err != nil {
		return err
	}
	var fold strings.Builder
	for i, c := range tr.Cues {
		f := search.Fold(c.Text)
		if err := r.q.InsertSearchCue(ctx, sqlcdb.InsertSearchCueParams{
			ID: first + int64(i), ItemID: itemID, ArtifactID: best.ID, Lang: lang, Variant: best.Variant,
			Cue: int64(i + 1), StartMs: c.StartMS, EndMs: c.EndMS, Text: c.Text, TextFold: f,
		}); err != nil {
			return err
		}
		appendFold(&fold, i, f)
	}
	return r.insertSearchGroup(ctx, sqlcdb.SearchGroup{FirstID: first, Cues: int64(len(tr.Cues)), ItemID: itemID,
		ArtifactID: best.ID, Lang: lang}, fold.String())
}

func (r *Repo) insertSearchGroup(ctx context.Context, g sqlcdb.SearchGroup, fold string) error {
	if err := r.q.InsertSearchGroup(ctx, sqlcdb.InsertSearchGroupParams(g)); err != nil {
		return err
	}
	return r.q.InsertSearchText(ctx, sqlcdb.InsertSearchTextParams{FirstID: g.FirstID, Fold: fold})
}

// search_text.fold 的格式（SRCH S9）：各句的 text_fold 依 cue 順序以 U+001F 接起來，text_fold 中原有的 U+001F
// 寫成 U+001E。查詢不可能含有這兩個字元（SRCH-R5 拒絕 Tab 以外的控制字元），所以命中不會跨句，
// 把句中的 U+001F 換成 U+001E 也不會改變哪些句子含有查詢。
const (
	foldSep    = "\x1f"
	foldSepAlt = "\x1e"
)

func appendFold(b *strings.Builder, i int, fold string) {
	if i > 0 {
		b.WriteString(foldSep)
	}
	b.WriteString(strings.ReplaceAll(fold, foldSep, foldSepAlt))
}

// matchCues 走過 fold 中含有 q 的句子（cue 從 1 起，依序），回傳句數；fn 回傳 false 時停止。
// 與 instr(text_fold, q) > 0 逐句比對的結果相同（q 不含 U+001E、U+001F，見 foldSep）。
func matchCues(fold, q string, fn func(cue int) bool) int {
	n, cue, pos := 0, 1, 0
	for {
		i := strings.Index(fold[pos:], q)
		if i < 0 {
			return n
		}
		cue += strings.Count(fold[pos:pos+i], foldSep)
		n++
		if fn != nil && !fn(cue) {
			return n
		}
		// q 不含分隔字元，所以命中之後的第一個分隔字元就是這一句的結尾。
		j := strings.Index(fold[pos+i:], foldSep)
		if j < 0 {
			return n
		}
		pos += i + j + len(foldSep)
		cue++
	}
}

// buildSearchGroups 從既有的 search_cues 建立 search_groups 與 search_text（migration 00038 之後第一次開啟，
// SRCH S9）：不讀產出檔。id 不符合「第 n 句在 first_id+n-1」的那份字幕（不應該發生）改由產出檔重建。
func (r *Repo) buildSearchGroups(ctx context.Context) error {
	if err := r.q.DeleteAllSearchGroups(ctx); err != nil {
		return err
	}
	type grp struct {
		g      sqlcdb.SearchGroup
		ok     bool
		maxID  int64
		minCue int64
		maxCue int64
		minOff int64
		maxOff int64
		cnt    int64
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT artifact_id, min(item_id), max(item_id), min(lang), max(lang), min(id), max(id),
  count(*), min(cue), max(cue), min(id - cue), max(id - cue) FROM search_cues GROUP BY artifact_id`)
	if err != nil {
		return err
	}
	var groups []grp
	for rows.Next() {
		var x grp
		var item2, lang2 string
		if err := rows.Scan(&x.g.ArtifactID, &x.g.ItemID, &item2, &x.g.Lang, &lang2, &x.g.FirstID, &x.maxID,
			&x.cnt, &x.minCue, &x.maxCue, &x.minOff, &x.maxOff); err != nil {
			rows.Close()
			return err
		}
		x.g.Cues = x.cnt
		x.ok = item2 == x.g.ItemID && lang2 == x.g.Lang && x.minCue == 1 && x.maxCue == x.cnt && x.minOff == x.maxOff &&
			x.maxID-x.g.FirstID+1 == x.cnt
		groups = append(groups, x)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// 同一個 (item, lang) 只能有一份字幕在索引中。
	seen := map[[2]string]int{}
	for _, x := range groups {
		seen[[2]string{x.g.ItemID, x.g.Lang}]++
	}
	redo := map[[2]string]bool{}
	for _, x := range groups {
		k := [2]string{x.g.ItemID, x.g.Lang}
		if !x.ok || seen[k] > 1 {
			redo[k] = true
			continue
		}
		fold, err := r.foldOfRange(ctx, x.g.FirstID, x.g.FirstID+x.g.Cues-1)
		if err != nil {
			return err
		}
		if err := r.insertSearchGroup(ctx, x.g, fold); err != nil {
			return err
		}
	}
	keys := make([][2]string, 0, len(redo))
	for k := range redo {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	for _, k := range keys {
		if err := r.reindexSearch(ctx, k[0], k[1]); err != nil {
			return err
		}
	}
	return r.q.SetSearchGroupsBuilt(ctx)
}

// foldOfRange 以 search_cues 中 id 在 [lo, hi] 的各句組出 search_text.fold。
func (r *Repo) foldOfRange(ctx context.Context, lo, hi int64) (string, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT text_fold FROM search_cues WHERE id BETWEEN ? AND ? ORDER BY id`, lo, hi)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for i := 0; rows.Next(); i++ {
		var f string
		if err := rows.Scan(&f); err != nil {
			return "", err
		}
		appendFold(&b, i, f)
	}
	return b.String(), rows.Err()
}

// readCues 讀取字幕產出並驗證 sha256。
func (r *Repo) readCues(rel, wantSHA string) (subtitle.Transcript, error) {
	if r.s == nil {
		return subtitle.Transcript{}, errors.New("store: 沒有資料目錄")
	}
	b, err := os.ReadFile(filepath.Join(r.s.dataDir(), filepath.FromSlash(rel)))
	if err != nil {
		return subtitle.Transcript{}, err
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != wantSHA {
		return subtitle.Transcript{}, fmt.Errorf("%s 的 sha256 不符", rel)
	}
	return subtitle.DecodeCuesJSON(bytes.NewReader(b))
}

// RebuildSearchIndex 清空並重建整個全文搜尋索引（SRCH S8、S9）。必須在交易中呼叫。
func (r *Repo) RebuildSearchIndex(ctx context.Context) error {
	if err := r.q.DeleteAllSearchGroups(ctx); err != nil {
		return err
	}
	if err := r.q.DeleteAllSearchCues(ctx); err != nil {
		return err
	}
	keys, err := r.q.CurrentTranscriptKeys(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := r.reindexSearch(ctx, k.ItemID, k.Lang); err != nil {
			return err
		}
	}
	if err := r.q.SetSearchGroupsBuilt(ctx); err != nil {
		return err
	}
	return r.q.SetSearchIndexBuilt(ctx)
}

// buildSearchIndexIfNeeded 在 migration 建立索引之後的第一次開啟時，以現有的現行字幕建立索引（SRCH S8）；
// 索引已經建好、但 search_groups 還沒建立時（migration 00038 之後第一次開啟），從 search_cues 建立（SRCH S9）。
// 沒有 search_index_state（測試以較舊的 migration 開啟）時什麼都不做。
func (s *Store) buildSearchIndexIfNeeded(ctx context.Context) error {
	var n, groups int
	if err := s.w.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'search_index_state'),
  (SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'search_groups')`).Scan(&n, &groups); err != nil {
		return err
	}
	if n == 0 || groups == 0 {
		return nil
	}
	built, err := s.wq.SearchIndexBuilt(ctx)
	if err != nil {
		return err
	}
	if built == 0 {
		return s.Tx(ctx, func(r *Repo) error { return r.RebuildSearchIndex(ctx) })
	}
	gb, err := s.wq.SearchGroupsBuilt(ctx)
	if err != nil || gb != 0 {
		return err
	}
	return s.Tx(ctx, func(r *Repo) error { return r.buildSearchGroups(ctx) })
}

// SearchIndexCheck 回報影音在索引中的列數，並執行 cues_fts 的 integrity-check（外部內容表與索引一致），
// 同時檢查 search_groups、search_text 與 search_cues 一致（SRCH S9）。只供驗收測試的 harness 與單元測試使用；
// integrity-check 是一個寫入陳述句，所以走寫入連線。
func (s *Store) SearchIndexCheck(ctx context.Context, itemID string) (rows int, err error) {
	if _, err := s.w.ExecContext(ctx, `INSERT INTO cues_fts (cues_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		return 0, fmt.Errorf("cues_fts 的 integrity-check 失敗：%w", err)
	}
	if err := s.CheckSearchGroups(ctx); err != nil {
		return 0, err
	}
	c, err := s.rq.CountSearchCuesForItem(ctx, itemID)
	return int(c), err
}

// CheckSearchGroups 確認每一句都在它那份字幕的 group 中（第 n 句在 first_id+n-1、影音與語言相同），
// 每個 group 的句數正確，search_text 是各句 text_fold 的串接（SRCH S9）。只供測試、驗收 harness 與 NFR-CMP-5 的 bench 使用。
func (s *Store) CheckSearchGroups(ctx context.Context) error {
	var orphans, wrong, noText int
	if err := s.w.QueryRowContext(ctx, `SELECT
  (SELECT COUNT(*) FROM search_cues sc WHERE NOT EXISTS (SELECT 1 FROM search_groups g WHERE g.artifact_id = sc.artifact_id
     AND g.item_id = sc.item_id AND g.lang = sc.lang AND sc.cue BETWEEN 1 AND g.cues AND sc.id = g.first_id + sc.cue - 1)),
  (SELECT COUNT(*) FROM search_groups g WHERE g.cues < 1 OR g.cues <> (SELECT COUNT(*) FROM search_cues sc
     WHERE sc.id BETWEEN g.first_id AND g.first_id + g.cues - 1 AND sc.artifact_id = g.artifact_id)),
  (SELECT COUNT(*) FROM search_groups g WHERE NOT EXISTS (SELECT 1 FROM search_text t WHERE t.first_id = g.first_id))
   + (SELECT COUNT(*) FROM search_text t WHERE NOT EXISTS (SELECT 1 FROM search_groups g WHERE g.first_id = t.first_id))`).
		Scan(&orphans, &wrong, &noText); err != nil {
		return err
	}
	if orphans+wrong+noText > 0 {
		return fmt.Errorf("search_groups 與 search_cues 不一致：不屬於任何 group 的句子 %d、句數不符的 group %d、缺少或多餘的 search_text %d",
			orphans, wrong, noText)
	}
	rows, err := s.w.QueryContext(ctx, `SELECT g.first_id, g.cues, t.fold FROM search_groups g JOIN search_text t ON t.first_id = g.first_id`)
	if err != nil {
		return err
	}
	type gt struct {
		first, cues int64
		fold        string
	}
	var all []gt
	for rows.Next() {
		var x gt
		if err := rows.Scan(&x.first, &x.cues, &x.fold); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.Tx(ctx, func(r *Repo) error {
		for _, x := range all {
			want, err := r.foldOfRange(ctx, x.first, x.first+x.cues-1)
			if err != nil {
				return err
			}
			if want != x.fold {
				return fmt.Errorf("search_text %d 與 search_cues 的 text_fold 不一致", x.first)
			}
		}
		return nil
	})
}

// SearchQuery 是全文搜尋的條件（SRCH-R3、R5、R7、R9）。
type SearchQuery struct {
	// Folded 是已驗證並摺疊大小寫的查詢。
	Folded string
	// Lang 不為空時只搜尋這個語言（完全相等）。
	Lang  string
	Limit int
	After *SearchCursor
}

// SearchCursor 是排序鍵（SRCH-R7）：影音的 created_at（新到舊）與 id、原文語言在前（Translation 0／1）、語言代碼、cue。
type SearchCursor struct {
	CreatedAtMs int64
	ItemID      string
	Translation int
	Lang        string
	Cue         int
}

// SearchHit 是一個命中的字幕句。
type SearchHit struct {
	ItemID, Platform, ExternalID, Title string
	Part                                int
	Lang, Variant                       string
	Cue                                 int
	StartMS, EndMS                      int64
	Text                                string
	Before, After                       *string
	Key                                 SearchCursor
}

// SearchPage 是一頁結果；Next 在最後一頁為 nil。
type SearchPage struct {
	Hits  []SearchHit
	Total int
	Next  *SearchCursor
}

// searchGroup 是一份有命中的字幕（一部影音的一個語言）與它的命中句數。
type searchGroup struct {
	firstID, cues int64
	hits          int
	itemID, lang  string
	createdAt     int64
	// tr 是 SRCH-R7 的「譯文語言在後」：語言與影音目前的原文語言不同時為 1。在查詢時計算，
	// 原文語言之後才確定（語言偵測）也不必重建索引。
	tr int
}

// cmpKey 依 SRCH-R7 比較 group 與游標的前四個排序鍵（created_at 新到舊、影音 id、原文／譯文、語言代碼）。
// 字串以位元組比較，與 SQLite 的 BINARY 定序相同。
func (g *searchGroup) cmpKey(createdAt int64, itemID string, tr int, lang string) int {
	switch {
	case g.createdAt != createdAt:
		if g.createdAt > createdAt {
			return -1
		}
		return 1
	case g.itemID != itemID:
		return strings.Compare(g.itemID, itemID)
	case g.tr != tr:
		return g.tr - tr
	}
	return strings.Compare(g.lang, lang)
}

// Search 執行全文搜尋（SRCH-R3、R7、R8、R9）。查詢以參數綁定傳入；3 個以上 code point 走 trigram 索引，
// 否則掃描 search_text（各句的 text_fold），兩條路徑的條件與排序相同。垃圾桶中的影音不列入結果與總數。
//
// 做法（NFR-PERF-9、SRCH S9）：先算出每一份字幕（search_groups）的命中句數，總數是它們的和，排序在 group 層級做；
// 只有要回傳的那一頁才逐句找出命中並讀取內容與前後句。原本每個命中句都要 join items、計數、排序，命中很多時很慢。
func (r *Repo) Search(ctx context.Context, q SearchQuery) (SearchPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	index := search.UseIndex(q.Folded)
	var groups []searchGroup
	var err error
	if index {
		groups, err = r.searchGroupsFTS(ctx, q)
	} else {
		groups, err = r.searchGroupsScan(ctx, q)
	}
	if err != nil {
		return SearchPage{}, err
	}
	slices.SortFunc(groups, func(a, b searchGroup) int { return a.cmpKey(b.createdAt, b.itemID, b.tr, b.lang) })

	var page SearchPage
	for _, g := range groups {
		page.Total += g.hits
	}
	// 這一頁要的句子：從游標之後開始，多取一句判斷有沒有下一頁。
	type pick struct {
		g   *searchGroup
		cue int
	}
	// 先依各 group 的命中句數決定這一頁會用到哪些 group：游標所在的那一份（命中在游標之後的有幾句未知）
	// 加上之後的 group，直到命中句數足夠。
	need := limit + 1
	var cand []*searchGroup
	after := 0
	for i, sum := 0, 0; i < len(groups) && sum < need; i++ {
		g := &groups[i]
		if a := q.After; a != nil {
			c := g.cmpKey(a.CreatedAtMs, a.ItemID, a.Translation, a.Lang)
			if c < 0 {
				continue
			}
			if c == 0 {
				after = a.Cue
				cand = append(cand, g)
				continue
			}
		}
		cand = append(cand, g)
		sum += g.hits
	}
	cues, err := r.pageCues(ctx, q.Folded, index, cand, after, need)
	if err != nil {
		return SearchPage{}, err
	}
	var picks []pick
	for i, g := range cand {
		for _, c := range cues[i] {
			if len(picks) == need {
				break
			}
			picks = append(picks, pick{g, c})
		}
	}
	if len(picks) == 0 {
		return page, nil
	}
	ids := make([]int64, len(picks))
	for i, p := range picks {
		ids[i] = p.g.firstID + int64(p.cue) - 1
	}
	byID, err := r.searchHitRows(ctx, ids)
	if err != nil {
		return SearchPage{}, err
	}
	for i, p := range picks {
		h, ok := byID[ids[i]]
		if !ok || h.Cue != p.cue || h.ItemID != p.g.itemID || h.Lang != p.g.lang {
			return SearchPage{}, fmt.Errorf("store: 全文搜尋索引不一致（search_groups %d 的第 %d 句）", p.g.firstID, p.cue)
		}
		h.Key = SearchCursor{CreatedAtMs: p.g.createdAt, ItemID: p.g.itemID, Translation: p.g.tr, Lang: p.g.lang, Cue: p.cue}
		page.Hits = append(page.Hits, h)
	}
	if len(page.Hits) > limit {
		page.Hits = page.Hits[:limit]
		k := page.Hits[limit-1].Key
		page.Next = &k
	}
	return page, nil
}

// groupFilter 是兩條路徑共用的條件：垃圾桶中的影音不列入（SRCH-R8），lang 完全相等（SRCH-R9）。
func groupFilter(q SearchQuery) (string, []any) {
	w := ` AND i.deleted_at IS NULL`
	var args []any
	if q.Lang != "" {
		w += ` AND g.lang = ?`
		args = append(args, q.Lang)
	}
	return w, args
}

// searchGroupsFTS 以 trigram 索引算出每份字幕的命中句數。FTS 依 rowid 回傳命中，每個 rowid 以 search_groups 的
// INTEGER PRIMARY KEY 找到它所在的 group（first_id 不大於 rowid 的最大值，SRCH S9 保證範圍連續、不重疊），
// 所以命中句不必回到 search_cues 或 items；join 與篩選只在 group 層級做。
func (r *Repo) searchGroupsFTS(ctx context.Context, q SearchQuery) ([]searchGroup, error) {
	w, args := groupFilter(q)
	return r.scanGroups(ctx, `WITH h AS (
  SELECT (SELECT g.first_id FROM search_groups g WHERE g.first_id <= f.rowid ORDER BY g.first_id DESC LIMIT 1) AS gid, COUNT(*) AS n
  FROM cues_fts f WHERE cues_fts MATCH ? GROUP BY gid)
SELECT g.first_id, g.cues, h.n, g.item_id, g.lang, i.created_at, COALESCE(i.original_lang, '')
FROM h JOIN search_groups g ON g.first_id = h.gid JOIN items i ON i.id = g.item_id WHERE 1`+w,
		append([]any{search.Phrase(q.Folded)}, args...), "")
}

// searchGroupsScan 是 2 字以下的掃描（SRCH-R3）：逐份讀 search_text，在 Go 中找出含有查詢的句子。
// 比逐句掃描 search_cues 少了每一列的讀取與函式呼叫（lib-10k 約 600 萬列對 2 萬列）。
func (r *Repo) searchGroupsScan(ctx context.Context, q SearchQuery) ([]searchGroup, error) {
	w, args := groupFilter(q)
	return r.scanGroups(ctx, `SELECT g.first_id, g.cues, t.fold, g.item_id, g.lang, i.created_at, COALESCE(i.original_lang, '')
FROM search_groups g JOIN search_text t ON t.first_id = g.first_id JOIN items i ON i.id = g.item_id WHERE 1`+w, args, q.Folded)
}

// scanGroups 讀取 group 的列：第三欄是命中句數（FTS），或是 search_text（scan 不為空時在這裡比對）。
func (r *Repo) scanGroups(ctx context.Context, query string, args []any, scan string) ([]searchGroup, error) {
	rows, err := r.s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []searchGroup
	var fold string
	for rows.Next() {
		var g searchGroup
		var orig string
		var dest any = &g.hits
		if scan != "" {
			dest = &fold
		}
		if err := rows.Scan(&g.firstID, &g.cues, dest, &g.itemID, &g.lang, &g.createdAt, &orig); err != nil {
			return nil, err
		}
		if scan != "" {
			if g.hits = matchCues(fold, scan, nil); g.hits == 0 {
				continue
			}
		}
		if g.lang != orig {
			g.tr = 1
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// pageCues 找出 cand 中每個 group 的命中句（依 cue 排序；第一個 group 若是游標所在的那一份，只取 cue 大於 after 的），
// 每個 group 最多 need 句。
//
// FTS：group 不多時逐份以 rowid 範圍查詢；超過兩份時只查一次涵蓋它們的 rowid 範圍再分配，
// 因為每次查詢都要重新走一次每個 trigram 的 doclist（命中稀疏、一頁跨很多份字幕時，逐份查詢反而比較慢）。
func (r *Repo) pageCues(ctx context.Context, folded string, index bool, cand []*searchGroup, after, need int) ([][]int, error) {
	out := make([][]int, len(cand))
	from := func(i int) int {
		if i == 0 {
			return after
		}
		return 0
	}
	if !index {
		for i, g := range cand {
			var fold string
			if err := r.s.r.QueryRowContext(ctx, `SELECT fold FROM search_text WHERE first_id = ?`, g.firstID).Scan(&fold); err != nil {
				return nil, err
			}
			lo := from(i)
			matchCues(fold, folded, func(cue int) bool {
				if cue > lo {
					out[i] = append(out[i], cue)
				}
				return len(out[i]) < need
			})
		}
		return out, nil
	}
	phrase := search.Phrase(folded)
	if len(cand) <= 2 {
		for i, g := range cand {
			ids, err := r.ftsRowids(ctx, `SELECT rowid FROM cues_fts WHERE cues_fts MATCH ? AND rowid BETWEEN ? AND ? ORDER BY rowid LIMIT ?`,
				phrase, g.firstID+int64(from(i)), g.firstID+g.cues-1, need)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				out[i] = append(out[i], int(id-g.firstID)+1)
			}
		}
		return out, nil
	}
	byFirst := make([]int, len(cand)) // cand 的索引，依 first_id 排序
	lo, hi := cand[0].firstID, cand[0].firstID+cand[0].cues-1
	for i, g := range cand {
		byFirst[i] = i
		lo, hi = min(lo, g.firstID), max(hi, g.firstID+g.cues-1)
	}
	slices.SortFunc(byFirst, func(a, b int) int { return cmp.Compare(cand[a].firstID, cand[b].firstID) })
	ids, err := r.ftsRowids(ctx, `SELECT rowid FROM cues_fts WHERE cues_fts MATCH ? AND rowid BETWEEN ? AND ? ORDER BY rowid`, phrase, lo, hi)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		// first_id 不大於 id 的最後一份；id 可能落在不在 cand 中的 group（其他語言、垃圾桶、這一頁用不到的）。
		k := sort.Search(len(byFirst), func(k int) bool { return cand[byFirst[k]].firstID > id })
		if k == 0 {
			continue
		}
		i := byFirst[k-1]
		g := cand[i]
		if cue := int(id-g.firstID) + 1; cue <= int(g.cues) && cue > from(i) && len(out[i]) < need {
			out[i] = append(out[i], cue)
		}
	}
	return out, nil
}

func (r *Repo) ftsRowids(ctx context.Context, query string, args ...any) ([]int64, error) {
	rows, err := r.s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// searchHitRows 讀取這一頁的字幕句、影音與前後句。
func (r *Repo) searchHitRows(ctx context.Context, ids []int64) (map[int64]SearchHit, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.s.r.QueryContext(ctx, `SELECT sc.id, sc.item_id, i.platform, i.external_id, i.part, i.title,
  sc.lang, sc.variant, sc.cue, sc.start_ms, sc.end_ms, sc.text,
  (SELECT b.text FROM search_cues b WHERE b.artifact_id = sc.artifact_id AND b.cue = sc.cue - 1),
  (SELECT n.text FROM search_cues n WHERE n.artifact_id = sc.artifact_id AND n.cue = sc.cue + 1)
FROM search_cues sc JOIN items i ON i.id = sc.item_id WHERE sc.id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]SearchHit, len(ids))
	for rows.Next() {
		var id int64
		var h SearchHit
		var before, after sql.NullString
		if err := rows.Scan(&id, &h.ItemID, &h.Platform, &h.ExternalID, &h.Part, &h.Title,
			&h.Lang, &h.Variant, &h.Cue, &h.StartMS, &h.EndMS, &h.Text, &before, &after); err != nil {
			return nil, err
		}
		if before.Valid {
			h.Before = &before.String
		}
		if after.Valid {
			h.After = &after.String
		}
		out[id] = h
	}
	return out, rows.Err()
}

// SeedItemCreatedAt 改寫影音的 created_at。只供驗收測試的 harness 使用：同一個場景中依序建立的影音，
// created_at 要嚴格遞增，SRCH-R7 的「新的在前」才能斷言（steps.md 的 search library 句型）。
func (s *Store) SeedItemCreatedAt(ctx context.Context, itemID string, at time.Time) error {
	n, err := s.wq.SeedItemCreatedAt(ctx, sqlcdb.SeedItemCreatedAtParams{CreatedAt: at.UnixMilli(), ID: itemID})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
