package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
func (r *Repo) reindexSearch(ctx context.Context, itemID, lang string) error {
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
	if err != nil {
		return nil
	}
	for i, c := range tr.Cues {
		if err := r.q.InsertSearchCue(ctx, sqlcdb.InsertSearchCueParams{
			ItemID: itemID, ArtifactID: best.ID, Lang: lang, Variant: best.Variant,
			Cue: int64(i + 1), StartMs: c.StartMS, EndMs: c.EndMS, Text: c.Text, TextFold: search.Fold(c.Text),
		}); err != nil {
			return err
		}
	}
	return nil
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

// RebuildSearchIndex 清空並重建整個全文搜尋索引（SRCH S8）。必須在交易中呼叫。
func (r *Repo) RebuildSearchIndex(ctx context.Context) error {
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
	return r.q.SetSearchIndexBuilt(ctx)
}

// buildSearchIndexIfNeeded 在 migration 建立索引之後的第一次開啟時，以現有的現行字幕建立索引（SRCH S8）。
// 沒有 search_index_state（測試以較舊的 migration 開啟）時什麼都不做。
func (s *Store) buildSearchIndexIfNeeded(ctx context.Context) error {
	var n int
	if err := s.w.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'search_index_state'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	built, err := s.wq.SearchIndexBuilt(ctx)
	if err != nil || built != 0 {
		return err
	}
	return s.Tx(ctx, func(r *Repo) error { return r.RebuildSearchIndex(ctx) })
}

// SearchIndexCheck 回報影音在索引中的列數，並執行 cues_fts 的 integrity-check（外部內容表與索引一致）。
// 只供驗收測試的 harness 使用；integrity-check 是一個寫入陳述句，所以走寫入連線。
func (s *Store) SearchIndexCheck(ctx context.Context, itemID string) (rows int, err error) {
	if _, err := s.w.ExecContext(ctx, `INSERT INTO cues_fts (cues_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		return 0, fmt.Errorf("cues_fts 的 integrity-check 失敗：%w", err)
	}
	c, err := s.rq.CountSearchCuesForItem(ctx, itemID)
	return int(c), err
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

// translationExpr 判斷一列是不是譯文語言：與影音目前的原文語言不同就排在後面（SRCH-R7）。
// 在查詢時計算，原文語言之後才確定（語言偵測）也不必重建索引。
const translationExpr = `(CASE WHEN sc.lang = COALESCE(i.original_lang, '') THEN 0 ELSE 1 END)`

// Search 執行全文搜尋（SRCH-R3、R7、R8、R9）。查詢以參數綁定傳入；3 個以上 code point 走 trigram 索引，
// 否則以 instr 掃描 text_fold，兩條路徑的條件與排序相同。垃圾桶中的影音不列入結果與總數。
func (r *Repo) Search(ctx context.Context, q SearchQuery) (SearchPage, error) {
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

	var page SearchPage
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&page.Total); err != nil {
		return SearchPage{}, err
	}
	if q.After != nil {
		from += ` AND (-i.created_at, i.id, ` + translationExpr + `, sc.lang, sc.cue) > (?, ?, ?, ?, ?)`
		a := q.After
		args = append(args, -a.CreatedAtMs, a.ItemID, a.Translation, a.Lang, a.Cue)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	rows, err := db.QueryContext(ctx, `SELECT sc.item_id, i.platform, i.external_id, i.part, i.title, i.created_at,
  sc.lang, sc.variant, sc.cue, sc.start_ms, sc.end_ms, sc.text,
  (SELECT p.text FROM search_cues p WHERE p.artifact_id = sc.artifact_id AND p.cue = sc.cue - 1),
  (SELECT n.text FROM search_cues n WHERE n.artifact_id = sc.artifact_id AND n.cue = sc.cue + 1),
  `+translationExpr+from+`
ORDER BY i.created_at DESC, i.id, `+translationExpr+`, sc.lang, sc.cue
LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return SearchPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		var before, after sql.NullString
		if err := rows.Scan(&h.ItemID, &h.Platform, &h.ExternalID, &h.Part, &h.Title, &h.Key.CreatedAtMs,
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
	if len(page.Hits) > limit {
		page.Hits = page.Hits[:limit]
		k := page.Hits[limit-1].Key
		page.Next = &k
	}
	return page, nil
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
