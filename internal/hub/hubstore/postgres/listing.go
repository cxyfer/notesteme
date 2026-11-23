package postgres

import (
	"context"
	"math"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres/sqlcdb"
)

// 列表與 ASR 前查詢（同 sqlite/listing.go）。可見度清單以 text[] 參數傳入（不用 sqlc.slice），查詢不拼接字串。

func visibilities(s hubstore.SeenBy) []string {
	if s.Visibilities == nil {
		return []string{}
	}
	return s.Visibilities
}

func clamp32(v int) int32 {
	return int32(max(math.MinInt32, min(v, math.MaxInt32)))
}

// ListVersions 實作 hubstore.Store。
func (s *Store) ListVersions(ctx context.Context, f hubstore.VersionFilter) ([]hubstore.ListedVersion, error) {
	p := sqlcdb.ListVersionsParams{
		Platform: f.Platform, Channel: f.Channel, Lang: f.Lang, Kind: f.Kind, Variant: f.Variant,
		TranscribeModel: f.TranscribeModel, CorrectModel: f.CorrectModel, TranslateModel: f.TranslateModel,
		ExcludeUnverified: f.ExcludeUnverified, SeeAll: f.Seen.SeeAll, Visibilities: visibilities(f.Seen), ViewerID: f.Seen.UserID,
		ByScore: f.ByScore, Lim: clamp32(max(f.Limit, 0)),
	}
	if f.Video != nil {
		p.HasVideo, p.VideoPlatform, p.VideoExternalID, p.VideoPart = true, f.Video.Platform, f.Video.ExternalID, clamp32(f.Video.Part)
	}
	if c := f.After; c != nil {
		p.HasCursor, p.CursorScore, p.CursorAt, p.CursorID = true, clamp32(c.Score), ts(c.CreatedAt), c.ID
	}
	rows, err := s.q.ListVersions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.ListedVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.ListedVersion{Version: toVersion(r.Version), Video: toVideo(r.Video), SortScore: int(r.SortScore)})
	}
	return out, nil
}

// LookupVersions 實作 hubstore.Store。範圍查詢 [prefix, prefix+"g")：雜湊只有小寫十六進位，"g" 大於任何一個字元
// （key_sha256 是 COLLATE "C"，以位元組比較）。
func (s *Store) LookupVersions(ctx context.Context, platform, prefix string, seen hubstore.SeenBy) ([]hubstore.LookupVersion, error) {
	rows, err := s.q.LookupVersions(ctx, sqlcdb.LookupVersionsParams{
		Platform: platform, Lo: prefix, Hi: prefix + "g", SeeAll: seen.SeeAll, Visibilities: visibilities(seen), ViewerID: seen.UserID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.LookupVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.LookupVersion{Version: toVersion(r.Version), VideoKeySHA256: r.KeySha256})
	}
	return out, nil
}
