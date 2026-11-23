package hubstore

import (
	"context"
	"time"
)

// 列表、篩選與 ASR 前查詢（HREP-R16、R17、R19，M5-S4）。

// SeenBy 是 SQL 預先過濾用的可見度條件：SeeAll（moderate）時不過濾；否則 visibility 在 Visibilities 中，
// 或 UserID 有這個版本未撤回的分享。呼叫端之後仍以 hubrepo.CanSee 逐筆確認。
type SeenBy struct {
	SeeAll       bool
	Visibilities []string
	UserID       string
}

// VideoKey 是 (platform, external_id, part)；沒有分P時 Part 是 0。
type VideoKey struct {
	Platform   string
	ExternalID string
	Part       int
}

// VersionCursor 是列表的 keyset 位置：上一頁最後一列的 (排序分數, 建立時間, ID)。
type VersionCursor struct {
	Score     int
	CreatedAt time.Time
	ID        string
}

// VersionFilter 是 GET /api/v1/versions 的條件；空字串代表不限。只會列出 visible 的版本。
type VersionFilter struct {
	Platform, Channel                             string
	Video                                         *VideoKey
	Lang, Kind, Variant                           string
	TranscribeModel, CorrectModel, TranslateModel string
	ExcludeUnverified                             bool
	// ByScore 為 true 時依分數排序（同分依新到舊），否則依新到舊。
	ByScore bool
	After   *VersionCursor
	Limit   int
	Seen    SeenBy
}

// ListedVersion 是列表中的一個版本與它的影片；SortScore 是 cursor 用的排序分數（依新到舊時是 0）。
type ListedVersion struct {
	Version
	Video     Video
	SortScore int
}

// LookupVersion 是 ASR 前查詢的一個版本與它的影片雜湊。
type LookupVersion struct {
	Version
	VideoKeySHA256 string
}

// ListingStore 是列表與查詢的方法（嵌入 Store）。
type ListingStore interface {
	// ListVersions 依條件、可見度與 keyset 順序列出 visible 的版本，最多 f.Limit 筆。
	ListVersions(ctx context.Context, f VersionFilter) ([]ListedVersion, error)
	// LookupVersions 回傳 platform 中影片雜湊以 prefix 開頭、visible 的 source／normalized 字幕版本，
	// 依影片雜湊、分數（高到低）、建立時間（新到舊）排序。prefix 由呼叫端驗證（小寫十六進位）。
	LookupVersions(ctx context.Context, platform, prefix string, seen SeenBy) ([]LookupVersion, error)
}
