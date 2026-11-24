package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres/sqlcdb"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
)

// 內容倉庫（同 sqlite/repository.go）。

func toVideo(v sqlcdb.Video) hubstore.Video {
	return hubstore.Video{
		ID: v.ID, Platform: v.Platform, ExternalID: v.ExternalID, Part: int(v.Part), Title: v.Title,
		ChannelExternalID: v.ChannelExternalID, ChannelTitle: v.ChannelTitle, DurationS: v.DurationS,
		OriginalLang: v.OriginalLang, PublishedAt: v.PublishedAt, KeySHA256: v.KeySha256, FirstSharedAt: utc(v.FirstSharedAt),
	}
}

func toVersion(v sqlcdb.Version) hubstore.Version {
	return hubstore.Version{
		ID: v.ID, VideoID: v.VideoID, Kind: v.Kind, Variant: v.Variant, Lang: v.Lang, Format: v.Format,
		SHA256: v.Sha256, Size: v.Size, Provenance: []byte(v.ProvenanceJson),
		TranscribeModel: v.TranscribeModel, CorrectModel: v.CorrectModel, TranslateModel: v.TranslateModel,
		ModelVerified: v.ModelVerified, Visibility: v.Visibility, License: v.License, State: v.State,
		Score: int(v.Score), CreatedAt: utc(v.CreatedAt),
	}
}

// parentsOf 從白名單過濾後的來源紀錄取出 parents_sha256。
func parentsOf(prov string) []string {
	var p struct {
		Parents []string `json:"parents_sha256"`
	}
	_ = json.Unmarshal([]byte(prov), &p)
	return p.Parents
}

// lockVersion 鎖住版本的列；版本不存在時回 ErrNotFound。
func lockVersion(ctx context.Context, q *sqlcdb.Queries, id string) error {
	_, err := q.LockVersion(ctx, id)
	return notFound(err)
}

// CreateShare 實作 hubstore.Store。先鎖上傳者：同一個人並行上傳同一個 bundle 時，第二次會看到第一次（冪等，HREP-R5）。
// 不同人並行建立同一部影片或同一個版本時撞到唯一鍵，整筆重試後走「已存在」的分支。
func (s *Store) CreateShare(ctx context.Context, in hubstore.NewShare) (hubstore.ShareResult, error) {
	var res hubstore.ShareResult
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		res = hubstore.ShareResult{}
		now := s.now()
		if err := lockUser(ctx, q, in.UserID); err != nil && !errors.Is(err, hubstore.ErrNotFound) {
			return err
		}
		if sh, err := q.ActiveShareByBundle(ctx, sqlcdb.ActiveShareByBundleParams{UserID: in.UserID, BundleSha256: in.BundleSHA256}); err == nil {
			return existingShare(ctx, q, sh, &res)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		video, err := ensureVideo(ctx, q, in.Video, now)
		if err != nil {
			return err
		}
		res.Video = toVideo(video)
		// 版本的鍵不唯一（安全修補 A）：鎖住影片的列，同一部影片的分享依序判斷去重，並行時不會各建一列。
		if _, err := q.LockVideo(ctx, video.ID); err != nil {
			return err
		}
		for _, nv := range in.Versions {
			n, err := q.IsRemovedHash(ctx, sqlcdb.IsRemovedHashParams{VideoID: video.ID, Sha256: nv.Version.SHA256})
			if err != nil {
				return err
			}
			if n > 0 {
				return hubstore.ErrRemovedContent
			}
		}
		for _, b := range in.Blobs {
			if err := q.AddBlob(ctx, sqlcdb.AddBlobParams{Sha256: b.SHA256, Size: b.Size, MediaType: b.MediaType, CreatedAt: now}); err != nil {
				return err
			}
		}
		res.ShareID = newID()
		if err := q.CreateShare(ctx, sqlcdb.CreateShareParams{
			ID: res.ShareID, UserID: in.UserID, VideoID: video.ID, License: in.License, AttestedAt: now,
			BundleSha256: in.BundleSHA256, BundleSize: in.BundleSize, CreatedAt: now,
		}); err != nil {
			return err
		}
		type created struct {
			v  sqlcdb.Version
			nv hubstore.NewVersion
		}
		var fresh []created
		for i, nv := range in.Versions {
			x := nv.Version
			v, dedup, restored, err := pickExisting(ctx, q, video.ID, in, nv)
			if err != nil {
				return err
			}
			if restored {
				fresh = append(fresh, created{v: v, nv: nv})
			}
			if !dedup && !restored {
				state := x.State
				if state == "" {
					state = "visible"
				}
				p := sqlcdb.CreateVersionParams{
					ID: newID(), VideoID: video.ID, Kind: x.Kind, Variant: x.Variant, Lang: x.Lang, Format: x.Format,
					Sha256: x.SHA256, Size: x.Size, ProvenanceJson: string(x.Provenance),
					TranscribeModel: x.TranscribeModel, CorrectModel: x.CorrectModel, TranslateModel: x.TranslateModel,
					ModelVerified: x.ModelVerified, Visibility: x.Visibility, License: x.License, State: state,
					Score: int32(x.Score), CreatedAt: now,
				}
				if p.ProvenanceJson == "" {
					p.ProvenanceJson = "{}"
				}
				if err := q.CreateVersion(ctx, p); err != nil {
					return err
				}
				if err := q.AddVersionBlob(ctx, sqlcdb.AddVersionBlobParams{VersionID: p.ID, Sha256: x.SHA256, Role: "content"}); err != nil {
					return err
				}
				for _, img := range nv.Images {
					if err := q.AddVersionBlob(ctx, sqlcdb.AddVersionBlobParams{VersionID: p.ID, Sha256: img, Role: "image"}); err != nil {
						return err
					}
				}
				if v, err = q.VersionByID(ctx, p.ID); err != nil {
					return err
				}
				fresh = append(fresh, created{v: v, nv: nv})
			}
			if err := q.AddShareVersion(ctx, sqlcdb.AddShareVersionParams{
				ShareID: res.ShareID, VersionID: v.ID, Position: int32(i), Deduplicated: dedup,
			}); err != nil {
				return err
			}
			res.Versions = append(res.Versions, hubstore.SharedVersion{Version: toVersion(v), Deduplicated: dedup})
		}
		// 衍生關係：新版本的 parents 與 regenerated_from，以及既有版本的 parents_sha256 指到新版本時補上的 parent（HREP-R6）。
		for _, c := range fresh {
			for _, sha := range c.nv.ParentsSHA256 {
				ps, err := q.VersionsBySHA(ctx, sqlcdb.VersionsBySHAParams{VideoID: video.ID, Sha256: sha})
				if err != nil {
					return err
				}
				for _, p := range ps {
					if err := addDerivation(ctx, q, c.v.ID, p.ID, hubstore.RelationParent); err != nil {
						return err
					}
				}
			}
			if c.nv.RegeneratedFrom != "" {
				if err := addDerivation(ctx, q, c.v.ID, c.nv.RegeneratedFrom, hubstore.RelationRegeneratedFrom); err != nil {
					return err
				}
			}
		}
		if len(fresh) > 0 {
			all, err := q.VideoVersions(ctx, video.ID)
			if err != nil {
				return err
			}
			for _, x := range all {
				parents := parentsOf(x.ProvenanceJson)
				for _, c := range fresh {
					if slices.Contains(parents, c.v.Sha256) {
						if err := addDerivation(ctx, q, x.ID, c.v.ID, hubstore.RelationParent); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return hubstore.ShareResult{}, err
	}
	return res, nil
}

// pickExisting 依 hubstore.ChooseExisting 處理鍵相同的既有版本：回傳併入的版本（dedup）或恢復的 withdrawn 版本（restored）；
// 兩者都不是時呼叫端建立新版本。
func pickExisting(ctx context.Context, q *sqlcdb.Queries, videoID string, in hubstore.NewShare, nv hubstore.NewVersion) (sqlcdb.Version, bool, bool, error) {
	x := nv.Version
	rows, err := q.VersionsByKey(ctx, sqlcdb.VersionsByKeyParams{VideoID: videoID, Kind: x.Kind, Variant: x.Variant, Lang: x.Lang, Sha256: x.SHA256})
	if err != nil {
		return sqlcdb.Version{}, false, false, err
	}
	cands := make([]hubstore.ExistingVersion, len(rows))
	for i, r := range rows {
		n, err := q.UserSharesVersion(ctx, sqlcdb.UserSharesVersionParams{VersionID: r.ID, UserID: in.UserID})
		if err != nil {
			return sqlcdb.Version{}, false, false, err
		}
		cands[i] = hubstore.ExistingVersion{Version: toVersion(r), IsUploader: n > 0}
	}
	join, restore := hubstore.ChooseExisting(cands, x.Visibility, in.CanJoin)
	switch {
	case join >= 0:
		return rows[join], true, false, nil
	case restore < 0:
		return sqlcdb.Version{}, false, false, nil
	}
	id := rows[restore].ID
	prov := string(x.Provenance)
	if prov == "" {
		prov = "{}"
	}
	n, err := q.RestoreVersion(ctx, sqlcdb.RestoreVersionParams{
		Format: x.Format, Size: x.Size, ProvenanceJson: prov, TranscribeModel: x.TranscribeModel, CorrectModel: x.CorrectModel,
		TranslateModel: x.TranslateModel, ModelVerified: x.ModelVerified, Visibility: x.Visibility, License: x.License, ID: id,
	})
	if err != nil {
		return sqlcdb.Version{}, false, false, err
	}
	if n != 1 {
		return sqlcdb.Version{}, false, false, fmt.Errorf("恢復撤回的版本 %s 失敗", id)
	}
	// 舊的來源紀錄宣告的 parent／regenerated_from 不再成立：刪掉，呼叫端依新的來源紀錄重建（別的版本以它為 parent 的關係依內容判斷，保留）。
	if err := q.DeleteChildDerivations(ctx, id); err != nil {
		return sqlcdb.Version{}, false, false, err
	}
	if err := q.DeleteVersionImageBlobs(ctx, id); err != nil {
		return sqlcdb.Version{}, false, false, err
	}
	for _, img := range nv.Images {
		if err := q.AddVersionBlob(ctx, sqlcdb.AddVersionBlobParams{VersionID: id, Sha256: img, Role: "image"}); err != nil {
			return sqlcdb.Version{}, false, false, err
		}
	}
	v, err := q.VersionByID(ctx, id)
	return v, false, true, err
}

func addDerivation(ctx context.Context, q *sqlcdb.Queries, child, parent, relation string) error {
	if child == parent {
		return nil
	}
	return q.AddDerivation(ctx, sqlcdb.AddDerivationParams{ChildVersionID: child, ParentVersionID: parent, Relation: relation})
}

func existingShare(ctx context.Context, q *sqlcdb.Queries, sh sqlcdb.Share, res *hubstore.ShareResult) error {
	v, err := q.VideoByID(ctx, sh.VideoID)
	if err != nil {
		return err
	}
	rows, err := q.ShareVersions(ctx, sh.ID)
	if err != nil {
		return err
	}
	res.ShareID, res.Video, res.Existing = sh.ID, toVideo(v), true
	for _, r := range rows {
		res.Versions = append(res.Versions, hubstore.SharedVersion{Version: toVersion(r.Version), Deduplicated: true})
	}
	return nil
}

func ensureVideo(ctx context.Context, q *sqlcdb.Queries, in hubstore.Video, now time.Time) (sqlcdb.Video, error) {
	v, err := q.VideoByKey(ctx, sqlcdb.VideoByKeyParams{Platform: in.Platform, ExternalID: in.ExternalID, Part: int32(in.Part)})
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	p := sqlcdb.CreateVideoParams{
		ID: newID(), Platform: in.Platform, ExternalID: in.ExternalID, Part: int32(in.Part), Title: in.Title,
		ChannelExternalID: in.ChannelExternalID, ChannelTitle: in.ChannelTitle, DurationS: in.DurationS,
		OriginalLang: in.OriginalLang, PublishedAt: in.PublishedAt,
		KeySha256: sqlite.VideoKeySHA256(in.Platform, in.ExternalID, in.Part), FirstSharedAt: now,
	}
	if err := q.CreateVideo(ctx, p); err != nil {
		return v, err
	}
	return q.VideoByID(ctx, p.ID)
}

// WithdrawShare 實作 hubstore.Store。逐一鎖住分享中的版本再數其他未撤回的分享：
// 兩個並行撤回同一個版本的不同分享時，後鎖到的一方一定看得到先提交的撤回。
func (s *Store) WithdrawShare(ctx context.Context, userID, shareID string) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		n, err := q.WithdrawShare(ctx, sqlcdb.WithdrawShareParams{WithdrawnAt: ptr(s.now()), ID: shareID, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return hubstore.ErrNotFound
		}
		rows, err := q.ShareVersions(ctx, shareID)
		if err != nil {
			return err
		}
		slices.SortFunc(rows, func(a, b sqlcdb.ShareVersionsRow) int { return compareStr(a.Version.ID, b.Version.ID) })
		for _, r := range rows {
			if err := lockVersion(ctx, q, r.Version.ID); err != nil {
				return err
			}
			v, err := q.VersionByID(ctx, r.Version.ID)
			if err != nil {
				return err
			}
			active, err := q.ActiveSharesOfVersion(ctx, v.ID)
			if err != nil {
				return err
			}
			if active == 0 && (v.State == "visible" || v.State == "hidden") {
				if _, err := q.SetVersionState(ctx, sqlcdb.SetVersionStateParams{State: "withdrawn", ID: v.ID}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func compareStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func ptr[T any](v T) *T { return &v }

// ShareUsage 實作 hubstore.Store。
func (s *Store) ShareUsage(ctx context.Context, userID string, since time.Time) (hubstore.ShareUsage, error) {
	r, err := s.q.ShareUsage(ctx, sqlcdb.ShareUsageParams{UserID: userID, CreatedAt: ts(since)})
	if err != nil {
		return hubstore.ShareUsage{}, err
	}
	u := hubstore.ShareUsage{Count: int(r.N), Bytes: r.Bytes}
	if r.N > 0 {
		u.Oldest = utc(r.Oldest)
	}
	return u, nil
}

// Version 實作 hubstore.Store。
func (s *Store) Version(ctx context.Context, id string) (hubstore.Version, error) {
	v, err := s.q.VersionByID(ctx, id)
	if err != nil {
		return hubstore.Version{}, notFound(err)
	}
	return toVersion(v), nil
}

// VideoByID 實作 hubstore.Store。
func (s *Store) VideoByID(ctx context.Context, id string) (hubstore.Video, error) {
	v, err := s.q.VideoByID(ctx, id)
	if err != nil {
		return hubstore.Video{}, notFound(err)
	}
	return toVideo(v), nil
}

// VideoByKey 實作 hubstore.Store。
func (s *Store) VideoByKey(ctx context.Context, platform, externalID string, part int) (hubstore.Video, error) {
	if int64(part) != int64(int32(part)) {
		return hubstore.Video{}, hubstore.ErrNotFound
	}
	v, err := s.q.VideoByKey(ctx, sqlcdb.VideoByKeyParams{Platform: platform, ExternalID: externalID, Part: int32(part)})
	if err != nil {
		return hubstore.Video{}, notFound(err)
	}
	return toVideo(v), nil
}

// VideoVersions 實作 hubstore.Store。
func (s *Store) VideoVersions(ctx context.Context, videoID string) ([]hubstore.Version, error) {
	rows, err := s.q.VideoVersions(ctx, videoID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.Version, 0, len(rows))
	for _, r := range rows {
		out = append(out, toVersion(r))
	}
	return out, nil
}

// VersionUploaders 實作 hubstore.Store。
func (s *Store) VersionUploaders(ctx context.Context, versionID string) ([]hubstore.Uploader, error) {
	rows, err := s.q.VersionUploaders(ctx, versionID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.Uploader, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.Uploader{UserID: r.ID, DisplayName: r.DisplayName})
	}
	return out, nil
}

// VersionDerivations 實作 hubstore.Store。
func (s *Store) VersionDerivations(ctx context.Context, versionID string) (parents, children []hubstore.Derived, err error) {
	ps, err := s.q.VersionParents(ctx, versionID)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range ps {
		parents = append(parents, hubstore.Derived{Version: toVersion(p.Version), Relation: p.Relation})
	}
	cs, err := s.q.VersionChildren(ctx, versionID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range cs {
		children = append(children, hubstore.Derived{Version: toVersion(c.Version), Relation: c.Relation})
	}
	return parents, children, nil
}

// BlobRefs 實作 hubstore.Store。
func (s *Store) BlobRefs(ctx context.Context, sha string) (int, error) {
	n, err := s.q.BlobRefs(ctx, sha)
	return int(n), err
}

// validStates 是版本的狀態（ADR-0031 第 3 點）。
var validStates = []string{"visible", "hidden", "removed", "withdrawn"}

// SetVersionState 實作 hubstore.Store。
func (s *Store) SetVersionState(ctx context.Context, versionID, state string) error {
	if !slices.Contains(validStates, state) {
		return fmt.Errorf("不認得的版本狀態：%q", state)
	}
	n, err := s.q.SetVersionState(ctx, sqlcdb.SetVersionStateParams{State: state, ID: versionID})
	if err != nil {
		return err
	}
	if n == 0 {
		return hubstore.ErrNotFound
	}
	return nil
}

// AddRemovedHash 實作 hubstore.Store。
func (s *Store) AddRemovedHash(ctx context.Context, videoID, sha, takedownID string) error {
	return s.q.AddRemovedHash(ctx, sqlcdb.AddRemovedHashParams{VideoID: videoID, Sha256: sha, TakedownID: takedownID, RemovedAt: s.now()})
}

// ReplaceVersionContent 實作 hubstore.Store。
func (s *Store) ReplaceVersionContent(ctx context.Context, versionID string, b hubstore.Blob) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if err := q.AddBlob(ctx, sqlcdb.AddBlobParams{Sha256: b.SHA256, Size: b.Size, MediaType: b.MediaType, CreatedAt: s.now()}); err != nil {
			return err
		}
		n, err := q.SetVersionContent(ctx, sqlcdb.SetVersionContentParams{Sha256: b.SHA256, Size: b.Size, ID: versionID})
		if err != nil {
			return err
		}
		if n == 0 {
			return hubstore.ErrNotFound
		}
		if err := q.DeleteVersionContentBlobs(ctx, versionID); err != nil {
			return err
		}
		return q.AddVersionBlob(ctx, sqlcdb.AddVersionBlobParams{VersionID: versionID, Sha256: b.SHA256, Role: "content"})
	})
}
