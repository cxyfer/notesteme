package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite/sqlcdb"
)

// 社群與管理（M5-S5，HREP-R13～R15、HACC-R15～R17）。
// 分數一律在同一個交易中以「沒有被停用的帳號的票」整筆重算（不用增減量），投票、停用與恢復的結果才一致。

// voteEventRetention 是投票次數紀錄保留的時間（只用來算每小時的次數）。
const voteEventRetention = 2 * time.Hour

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optTime(v *int64) *time.Time {
	if v == nil {
		return nil
	}
	t := fromMS(*v)
	return &t
}

// SetVote 實作 hubstore.Store。
func (s *Store) SetVote(ctx context.Context, versionID, userID string, value int) (int, error) {
	score := 0
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		var err error
		if value == 0 {
			err = q.DeleteVote(ctx, sqlcdb.DeleteVoteParams{VersionID: versionID, UserID: userID})
		} else {
			err = q.UpsertVote(ctx, sqlcdb.UpsertVoteParams{VersionID: versionID, UserID: userID, Value: int64(value), CreatedAt: now, UpdatedAt: now})
		}
		if err != nil {
			return err
		}
		if err := q.PruneVoteEvents(ctx, sqlcdb.PruneVoteEventsParams{UserID: userID, At: now - voteEventRetention.Milliseconds()}); err != nil {
			return err
		}
		if err := q.AddVoteEvent(ctx, sqlcdb.AddVoteEventParams{UserID: userID, At: now}); err != nil {
			return err
		}
		if err := q.RecomputeScore(ctx, versionID); err != nil {
			return err
		}
		v, err := q.VersionByID(ctx, versionID)
		if err != nil {
			return notFound(err)
		}
		score = int(v.Score)
		return nil
	})
	return score, err
}

// VoteUsage 實作 hubstore.Store。
func (s *Store) VoteUsage(ctx context.Context, userID string, since time.Time) (hubstore.Usage, error) {
	r, err := s.rq.VoteUsage(ctx, sqlcdb.VoteUsageParams{UserID: userID, At: ms(since)})
	if err != nil {
		return hubstore.Usage{}, err
	}
	return usage(r.N, r.Oldest), nil
}

func usage(n, oldest int64) hubstore.Usage {
	u := hubstore.Usage{Count: int(n)}
	if n > 0 {
		u.Oldest = fromMS(oldest)
	}
	return u
}

func toReport(r sqlcdb.Report) hubstore.Report {
	return hubstore.Report{
		ID: r.ID, VersionID: r.VersionID, ReporterID: r.ReporterID, Reason: r.Reason, Note: r.Note, State: r.State,
		CreatedAt: fromMS(r.CreatedAt), DecidedAt: optTime(r.DecidedAt),
	}
}

// CreateReport 實作 hubstore.Store。
func (s *Store) CreateReport(ctx context.Context, in hubstore.NewReport) (hubstore.Report, bool, error) {
	var (
		out    hubstore.Report
		hidden bool
	)
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		n, err := q.OpenReportExists(ctx, sqlcdb.OpenReportExistsParams{VersionID: in.VersionID, ReporterID: in.ReporterID})
		if err != nil {
			return err
		}
		if n > 0 {
			return hubstore.ErrAlreadyReported
		}
		id := newID()
		if err := q.CreateReport(ctx, sqlcdb.CreateReportParams{
			ID: id, VersionID: in.VersionID, ReporterID: in.ReporterID, Reason: in.Reason, Note: in.Note, CreatedAt: s.now(),
		}); err != nil {
			return err
		}
		r, err := q.GetReport(ctx, id)
		if err != nil {
			return err
		}
		out = toReport(r)
		if in.AutoHide <= 0 {
			return nil
		}
		established, err := q.CountEstablishedReporters(ctx, sqlcdb.CountEstablishedReportersParams{
			VersionID: in.VersionID, MinAgeMs: in.MinAccountAge.Milliseconds(),
		})
		if err != nil {
			return err
		}
		if established < int64(in.AutoHide) {
			return nil
		}
		changed, err := q.SetVersionStateFrom(ctx, sqlcdb.SetVersionStateFromParams{State: "hidden", ID: in.VersionID, State_2: "visible"})
		hidden = changed > 0
		return err
	})
	return out, hidden, err
}

// ReportUsage 實作 hubstore.Store。
func (s *Store) ReportUsage(ctx context.Context, userID string, since time.Time) (hubstore.Usage, error) {
	r, err := s.rq.ReportUsage(ctx, sqlcdb.ReportUsageParams{ReporterID: userID, CreatedAt: ms(since)})
	if err != nil {
		return hubstore.Usage{}, err
	}
	return usage(r.N, r.Oldest), nil
}

// Report 實作 hubstore.Store。
func (s *Store) Report(ctx context.Context, id string) (hubstore.Report, error) {
	r, err := s.rq.GetReport(ctx, id)
	if err != nil {
		return hubstore.Report{}, notFound(err)
	}
	return toReport(r), nil
}

// Reports 實作 hubstore.Store。
func (s *Store) Reports(ctx context.Context, state string, limit int) ([]hubstore.Report, error) {
	rows, err := s.rq.ListReports(ctx, sqlcdb.ListReportsParams{State: state, Lim: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.Report, 0, len(rows))
	for _, r := range rows {
		out = append(out, toReport(r))
	}
	return out, nil
}

// decideReport 以條件式的 UPDATE 決定一筆未處理的檢舉（影響列數決定並發的決定誰先），回傳它。
func (s *Store) decideReport(ctx context.Context, q *sqlcdb.Queries, actor, id, state string, now int64) (sqlcdb.Report, error) {
	n, err := q.DecideReport(ctx, sqlcdb.DecideReportParams{State: state, DecidedAt: &now, DecidedBy: optStr(actor), ID: id})
	if err != nil {
		return sqlcdb.Report{}, err
	}
	r, err := q.GetReport(ctx, id)
	if err != nil {
		return r, notFound(err)
	}
	if n == 0 {
		return r, hubstore.ErrDecided
	}
	return r, q.CloseOpenReports(ctx, sqlcdb.CloseOpenReportsParams{State: state, DecidedAt: &now, DecidedBy: optStr(actor), VersionID: r.VersionID})
}

// DismissReport 實作 hubstore.Store。
func (s *Store) DismissReport(ctx context.Context, actorUserID, reportID string) (hubstore.Report, error) {
	var out hubstore.Report
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		r, err := s.decideReport(ctx, q, actorUserID, reportID, hubstore.ReportDismissed, now)
		if err != nil {
			return err
		}
		if _, err := q.SetVersionStateFrom(ctx, sqlcdb.SetVersionStateFromParams{State: "visible", ID: r.VersionID, State_2: "hidden"}); err != nil {
			return err
		}
		if r, err = q.GetReport(ctx, reportID); err != nil {
			return err
		}
		out = toReport(r)
		return audit(ctx, q, actorUserID, hubstore.AuditReport, "report", reportID, map[string]string{"decision": "dismiss", "version_id": r.VersionID}, now)
	})
	return out, err
}

// removeVersion 把版本標成 removed、記入 removed_hashes 並結案它未處理的檢舉；回傳之後沒有任何未移除的版本引用的 blob。
func (s *Store) removeVersion(ctx context.Context, q *sqlcdb.Queries, actor string, v sqlcdb.Version, source, reason, takedownID string, now int64) (hubstore.Removal, error) {
	var rm hubstore.Removal
	if v.State != "removed" {
		if err := q.MarkVersionRemoved(ctx, sqlcdb.MarkVersionRemovedParams{RemovalSource: source, RemovalReason: reason, ID: v.ID}); err != nil {
			return rm, err
		}
	}
	if err := q.AddRemovedHash(ctx, sqlcdb.AddRemovedHashParams{VideoID: v.VideoID, Sha256: v.Sha256, TakedownID: takedownID, RemovedAt: now}); err != nil {
		return rm, err
	}
	if err := q.CloseOpenReports(ctx, sqlcdb.CloseOpenReportsParams{State: hubstore.ReportActioned, DecidedAt: &now, DecidedBy: optStr(actor), VersionID: v.ID}); err != nil {
		return rm, err
	}
	shas, err := q.VersionBlobSHAs(ctx, v.ID)
	if err != nil {
		return rm, err
	}
	for _, sha := range shas {
		n, err := q.LiveBlobRefs(ctx, sha)
		if err != nil {
			return rm, err
		}
		if n == 0 {
			rm.BlobsToDelete = append(rm.BlobsToDelete, sha)
		}
	}
	return rm, nil
}

// RemoveByReport 實作 hubstore.Store。
func (s *Store) RemoveByReport(ctx context.Context, actorUserID, reportID string) (hubstore.Report, hubstore.Removal, error) {
	var (
		out hubstore.Report
		rm  hubstore.Removal
	)
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		r, err := s.decideReport(ctx, q, actorUserID, reportID, hubstore.ReportActioned, now)
		if err != nil {
			return err
		}
		v, err := q.VersionByID(ctx, r.VersionID)
		if err != nil {
			return err
		}
		if rm, err = s.removeVersion(ctx, q, actorUserID, v, hubstore.RemovalReport, r.Reason, "", now); err != nil {
			return err
		}
		if r, err = q.GetReport(ctx, reportID); err != nil {
			return err
		}
		out = toReport(r)
		return audit(ctx, q, actorUserID, hubstore.AuditReport, "report", reportID, map[string]string{"decision": "remove", "version_id": r.VersionID}, now)
	})
	return out, rm, err
}

// HideVersion 實作 hubstore.Store。
func (s *Store) HideVersion(ctx context.Context, actorUserID, versionID string) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		v, err := q.VersionByID(ctx, versionID)
		if err != nil {
			return notFound(err)
		}
		switch v.State {
		case "hidden":
			return nil
		case "visible":
		default:
			return hubstore.ErrVersionState
		}
		if _, err := q.SetVersionStateFrom(ctx, sqlcdb.SetVersionStateFromParams{State: "hidden", ID: versionID, State_2: "visible"}); err != nil {
			return err
		}
		changed = true
		return audit(ctx, q, actorUserID, hubstore.AuditVersionHide, "version", versionID, map[string]string{}, s.now())
	})
	return changed, err
}

func toTakedown(t sqlcdb.Takedown) hubstore.Takedown {
	return hubstore.Takedown{
		ID: t.ID, VersionID: t.VersionID, Reason: t.Reason, Statement: t.Statement,
		DEKNonce: t.DekNonce, DEKSealed: t.DekSealed, ContactNonce: t.ContactNonce, ContactSealed: t.ContactSealed,
		State: t.State, Note: t.Note, CreatedAt: fromMS(t.CreatedAt), DecidedAt: optTime(t.DecidedAt),
	}
}

// CreateTakedown 實作 hubstore.Store。
func (s *Store) CreateTakedown(ctx context.Context, t hubstore.Takedown) (hubstore.Takedown, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	if err := s.wq.CreateTakedown(ctx, sqlcdb.CreateTakedownParams{
		ID: t.ID, VersionID: t.VersionID, Reason: t.Reason, Statement: t.Statement,
		DekNonce: t.DEKNonce, DekSealed: t.DEKSealed, ContactNonce: t.ContactNonce, ContactSealed: t.ContactSealed, CreatedAt: s.now(),
	}); err != nil {
		return hubstore.Takedown{}, err
	}
	r, err := s.wq.GetTakedown(ctx, t.ID)
	if err != nil {
		return hubstore.Takedown{}, err
	}
	return toTakedown(r), nil
}

// Takedown 實作 hubstore.Store。
func (s *Store) Takedown(ctx context.Context, id string) (hubstore.Takedown, error) {
	r, err := s.rq.GetTakedown(ctx, id)
	if err != nil {
		return hubstore.Takedown{}, notFound(err)
	}
	return toTakedown(r), nil
}

// Takedowns 實作 hubstore.Store。
func (s *Store) Takedowns(ctx context.Context, state string, limit int) ([]hubstore.Takedown, error) {
	rows, err := s.rq.ListTakedowns(ctx, sqlcdb.ListTakedownsParams{State: state, Lim: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.Takedown, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTakedown(r))
	}
	return out, nil
}

// decideTakedown 以條件式的 UPDATE 決定一筆未決定的申請。
func decideTakedown(ctx context.Context, q *sqlcdb.Queries, actor, id, state, note string, now int64) (sqlcdb.Takedown, error) {
	n, err := q.DecideTakedown(ctx, sqlcdb.DecideTakedownParams{State: state, Note: note, DecidedAt: &now, DecidedBy: optStr(actor), ID: id})
	if err != nil {
		return sqlcdb.Takedown{}, err
	}
	t, err := q.GetTakedown(ctx, id)
	if err != nil {
		return t, notFound(err)
	}
	if n == 0 {
		return t, hubstore.ErrDecided
	}
	return t, nil
}

// RejectTakedown 實作 hubstore.Store。
func (s *Store) RejectTakedown(ctx context.Context, actorUserID, id, note string) (hubstore.Takedown, error) {
	var out hubstore.Takedown
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		t, err := decideTakedown(ctx, q, actorUserID, id, hubstore.TakedownRejected, note, now)
		if err != nil {
			return err
		}
		out = toTakedown(t)
		return audit(ctx, q, actorUserID, hubstore.AuditTakedown, "takedown", id, map[string]string{"decision": "reject", "version_id": t.VersionID}, now)
	})
	return out, err
}

// RemoveByTakedown 實作 hubstore.Store。
func (s *Store) RemoveByTakedown(ctx context.Context, actorUserID, id, note string) (hubstore.Takedown, hubstore.Removal, error) {
	var (
		out hubstore.Takedown
		rm  hubstore.Removal
	)
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		t, err := decideTakedown(ctx, q, actorUserID, id, hubstore.TakedownRemoved, note, now)
		if err != nil {
			return err
		}
		v, err := q.VersionByID(ctx, t.VersionID)
		if err != nil {
			if errors.Is(notFound(err), hubstore.ErrNotFound) {
				return hubstore.ErrVersionState
			}
			return err
		}
		if rm, err = s.removeVersion(ctx, q, actorUserID, v, hubstore.RemovalTakedown, t.Reason, t.ID, now); err != nil {
			return err
		}
		out = toTakedown(t)
		return audit(ctx, q, actorUserID, hubstore.AuditTakedown, "takedown", id, map[string]string{"decision": "remove", "version_id": t.VersionID}, now)
	})
	return out, rm, err
}

// SuspendUser 實作 hubstore.Store。
func (s *Store) SuspendUser(ctx context.Context, actorUserID, userID, reason string) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if _, err := q.GetUser(ctx, userID); err != nil {
			return notFound(err)
		}
		now := s.now()
		n, err := q.SetSuspended(ctx, sqlcdb.SetSuspendedParams{SuspendedAt: &now, SuspendedReason: reason, ID: userID})
		if err != nil || n == 0 {
			return err
		}
		changed = true
		if err := q.RecomputeScoresOfVoter(ctx, userID); err != nil {
			return err
		}
		if actorUserID == "" {
			return nil
		}
		return audit(ctx, q, actorUserID, hubstore.AuditUserSuspend, "user", userID, map[string]string{"reason": reason}, now)
	})
	return changed, err
}

// UnsuspendUser 實作 hubstore.Store。
func (s *Store) UnsuspendUser(ctx context.Context, actorUserID, userID string) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if _, err := q.GetUser(ctx, userID); err != nil {
			return notFound(err)
		}
		n, err := q.ClearSuspended(ctx, userID)
		if err != nil || n == 0 {
			return err
		}
		changed = true
		now := s.now()
		// 停用前的 session 與 Hub token 不會因為恢復而重新生效。
		if err := q.DeleteUserSessions(ctx, userID); err != nil {
			return err
		}
		if err := q.RevokeUserPATs(ctx, sqlcdb.RevokeUserPATsParams{RevokedAt: &now, UserID: userID}); err != nil {
			return err
		}
		if err := q.RecomputeScoresOfVoter(ctx, userID); err != nil {
			return err
		}
		if actorUserID == "" {
			return nil
		}
		return audit(ctx, q, actorUserID, hubstore.AuditUserUnsuspend, "user", userID, map[string]string{}, now)
	})
	return changed, err
}

// Users 實作 hubstore.Store。
func (s *Store) Users(ctx context.Context, limit int) ([]hubstore.User, error) {
	rows, err := s.rq.ListUsers(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.User, 0, len(rows))
	for _, u := range rows {
		out = append(out, toUser(u))
	}
	return out, nil
}

// SetCLIGroup 實作 hubstore.Store。
func (s *Store) SetCLIGroup(ctx context.Context, userID, group string, member bool) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if _, err := q.GetUser(ctx, userID); err != nil {
			return notFound(err)
		}
		now := s.now()
		var (
			n      int64
			err    error
			action = hubstore.AuditGroupAdd
		)
		if member {
			n, err = q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceCLI, AddedAt: now})
		} else {
			action = hubstore.AuditGroupRemove
			n, err = q.RemoveGroupMember(ctx, sqlcdb.RemoveGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceCLI})
		}
		if err != nil || n == 0 {
			return err
		}
		changed = true
		return audit(ctx, q, "", action, "user", userID, map[string]string{"group": group}, now)
	})
	return changed, err
}

// MyShares 實作 hubstore.Store。
func (s *Store) MyShares(ctx context.Context, userID string, limit int) ([]hubstore.MyShare, error) {
	rows, err := s.rq.UserShares(ctx, sqlcdb.UserSharesParams{UserID: userID, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.MyShare, 0, len(rows))
	videos := map[string]hubstore.Video{}
	for _, r := range rows {
		video, ok := videos[r.VideoID]
		if !ok {
			v, err := s.rq.VideoByID(ctx, r.VideoID)
			if err != nil {
				return nil, err
			}
			video = toVideo(v)
			videos[r.VideoID] = video
		}
		vs, err := s.rq.ShareVersions(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		ms := hubstore.MyShare{ID: r.ID, Video: video, CreatedAt: fromMS(r.CreatedAt), WithdrawnAt: optTime(r.WithdrawnAt)}
		for _, v := range vs {
			ms.Versions = append(ms.Versions, hubstore.MyShareVersion{
				Version: toVersion(v.Version), RemovalSource: v.Version.RemovalSource, RemovalReason: v.Version.RemovalReason,
			})
		}
		out = append(out, ms)
	}
	return out, nil
}
