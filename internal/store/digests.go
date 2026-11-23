package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Digest 是 digests 的一列（DIG-R8）。下一次執行的時間在 scheduled_jobs（kind digest）。
type Digest struct {
	ID        string
	Period    string
	At        string
	Weekday   string
	Timezone  string
	Notifiers []string
	Scope     string
	Enabled   bool
	CreatedAt time.Time
	// LastRunAt 是上一次執行涵蓋的範圍終點（排定的時間，DIG-R9）；nil 代表還沒有執行過。
	LastRunAt *time.Time
}

// DigestCandidate 是彙整範圍內的一部影音與它的 summary 步驟結束的時間。
type DigestCandidate struct {
	ItemID     string
	FinishedAt time.Time
}

func digestFromRow(row sqlcdb.Digest) (Digest, error) {
	d := Digest{
		ID: row.ID, Period: row.Period, At: row.At, Weekday: row.Weekday, Timezone: row.Timezone, Scope: row.Scope,
		Enabled: row.Enabled != 0, CreatedAt: fromMs(row.CreatedAt), LastRunAt: msToTime(row.LastRunAt),
	}
	if err := json.Unmarshal([]byte(row.NotifierIdsJson), &d.Notifiers); err != nil {
		return Digest{}, err
	}
	if d.Notifiers == nil {
		d.Notifiers = []string{}
	}
	return d, nil
}

func notifierIDsJSON(ids []string) (string, error) {
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

// InsertDigest 新增一個彙整設定；created_at 是 store 的現在時間。id 重複時回傳資料庫的唯一鍵錯誤。
func (r *Repo) InsertDigest(ctx context.Context, d Digest) error {
	ids, err := notifierIDsJSON(d.Notifiers)
	if err != nil {
		return err
	}
	return r.q.InsertDigest(ctx, sqlcdb.InsertDigestParams{
		ID: d.ID, Period: d.Period, At: d.At, Weekday: d.Weekday, Timezone: d.Timezone,
		NotifierIdsJson: ids, Scope: d.Scope, Enabled: boolInt(d.Enabled), CreatedAt: r.nowMs(),
	})
}

// UpdateDigest 寫回彙整設定（含 last_run_at）；不存在時回傳 ErrNotFound。
func (r *Repo) UpdateDigest(ctx context.Context, d Digest) error {
	ids, err := notifierIDsJSON(d.Notifiers)
	if err != nil {
		return err
	}
	n, err := r.q.UpdateDigest(ctx, sqlcdb.UpdateDigestParams{
		Period: d.Period, At: d.At, Weekday: d.Weekday, Timezone: d.Timezone, NotifierIdsJson: ids,
		Scope: d.Scope, Enabled: boolInt(d.Enabled), LastRunAt: msPtr(d.LastRunAt), ID: d.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Digest 取出一個彙整設定；不存在時回傳 ErrNotFound。
func (r *Repo) Digest(ctx context.Context, id string) (Digest, error) {
	row, err := r.q.GetDigest(ctx, id)
	if err != nil {
		return Digest{}, notFound(err)
	}
	return digestFromRow(row)
}

// ListDigests 依建立順序列出所有彙整設定。
func (r *Repo) ListDigests(ctx context.Context) ([]Digest, error) {
	rows, err := r.q.ListDigests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Digest, 0, len(rows))
	for _, row := range rows {
		d, err := digestFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// DeleteDigest 刪除彙整設定與它的 digest_entries；回傳是否存在。排程工作由呼叫端一併刪除。
func (r *Repo) DeleteDigest(ctx context.Context, id string) (bool, error) {
	n, err := r.q.DeleteDigest(ctx, id)
	return n > 0, err
}

// SetDigestLastRun 記錄這次執行涵蓋的範圍終點（DIG-R9）。
func (r *Repo) SetDigestLastRun(ctx context.Context, id string, end time.Time) error {
	ms := end.UnixMilli()
	return r.q.SetDigestLastRun(ctx, sqlcdb.SetDigestLastRunParams{LastRunAt: &ms, ID: id})
}

// InsertDigestEntry 記錄影音已列在這個彙整中（DIG-R9）；已經存在時不做事。
func (r *Repo) InsertDigestEntry(ctx context.Context, digestID, itemID string, runAt time.Time) error {
	return r.q.InsertDigestEntry(ctx, sqlcdb.InsertDigestEntryParams{DigestID: digestID, ItemID: itemID, RunAt: runAt.UnixMilli()})
}

// DigestCandidates 列出 summary 步驟在 (start, end] 結束、不在垃圾桶、還沒有列在這個彙整中的影音（DIG-R9），
// 依結束時間排序；allItems 為 false 時只列訂閱建立的影音（DIG-R8 的 scope）。
func (r *Repo) DigestCandidates(ctx context.Context, digestID string, start, end time.Time, allItems bool) ([]DigestCandidate, error) {
	s, e := start.UnixMilli(), end.UnixMilli()
	rows, err := r.q.DigestCandidates(ctx, sqlcdb.DigestCandidatesParams{RangeStart: &s, RangeEnd: &e, AllItems: boolInt(allItems), DigestID: digestID})
	if err != nil {
		return nil, err
	}
	out := make([]DigestCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, DigestCandidate{ItemID: row.ItemID, FinishedAt: fromMs(row.FinishedAt)})
	}
	return out, nil
}
