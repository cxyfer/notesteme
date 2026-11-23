package sqlite

import (
	"context"
	"encoding/json"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite/sqlcdb"
)

// 列表與 ASR 前查詢（M5-S4，HREP-R16、R19）。

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func seenParams(s hubstore.SeenBy) (int64, string, error) {
	vis := s.Visibilities
	if vis == nil {
		vis = []string{}
	}
	j, err := json.Marshal(vis)
	return b2i(s.SeeAll), string(j), err
}

// ListVersions 實作 hubstore.Store。
func (s *Store) ListVersions(ctx context.Context, f hubstore.VersionFilter) ([]hubstore.ListedVersion, error) {
	seeAll, vis, err := seenParams(f.Seen)
	if err != nil {
		return nil, err
	}
	p := sqlcdb.ListVersionsParams{
		Platform: f.Platform, Channel: f.Channel, Lang: f.Lang, Kind: f.Kind, Variant: f.Variant,
		TranscribeModel: f.TranscribeModel, CorrectModel: f.CorrectModel, TranslateModel: f.TranslateModel,
		ExcludeUnverified: b2i(f.ExcludeUnverified), SeeAll: seeAll, VisibilitiesJson: vis, ViewerID: f.Seen.UserID,
		ByScore: b2i(f.ByScore), Lim: int64(max(f.Limit, 0)),
	}
	if f.Video != nil {
		p.HasVideo, p.VideoPlatform, p.VideoExternalID, p.VideoPart = 1, f.Video.Platform, f.Video.ExternalID, int64(f.Video.Part)
	}
	if c := f.After; c != nil {
		p.HasCursor, p.CursorScore, p.CursorAt, p.CursorID = 1, int64(c.Score), c.CreatedAt.UnixMilli(), c.ID
	}
	rows, err := s.rq.ListVersions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.ListedVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.ListedVersion{Version: toVersion(r.Version), Video: toVideo(r.Video), SortScore: int(r.SortScore)})
	}
	return out, nil
}

// LookupVersions 實作 hubstore.Store。範圍查詢 [prefix, prefix+"g")：雜湊只有小寫十六進位，"g" 大於任何一個字元。
func (s *Store) LookupVersions(ctx context.Context, platform, prefix string, seen hubstore.SeenBy) ([]hubstore.LookupVersion, error) {
	seeAll, vis, err := seenParams(seen)
	if err != nil {
		return nil, err
	}
	rows, err := s.rq.LookupVersions(ctx, sqlcdb.LookupVersionsParams{
		Platform: platform, Lo: prefix, Hi: prefix + "g", SeeAll: seeAll, VisibilitiesJson: vis, ViewerID: seen.UserID,
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
