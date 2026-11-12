package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const itemColumns = `id, platform, external_id, part, COALESCE(channel_id, ''), COALESCE(profile_id, ''),
	overrides_json, until_stage, title, url, published_at, duration_s,
	COALESCE(original_lang, ''), COALESCE(lang_source, ''), lang_mixed,
	COALESCE(availability, ''), COALESCE(media_kind, ''), metadata_json, created_at, deleted_at`

type scanner interface{ Scan(dest ...any) error }

func scanItem(sc scanner) (Item, error) {
	var (
		it                  Item
		overrides, metadata string
		pub, deleted        *int64
		dur                 *int
		created             int64
		mixed               int
	)
	err := sc.Scan(&it.ID, &it.Platform, &it.ExternalID, &it.Part, &it.ChannelID, &it.ProfileID,
		&overrides, &it.UntilStage, &it.Title, &it.URL, &pub, &dur,
		&it.OriginalLang, &it.LangSource, &mixed,
		&it.Availability, &it.MediaKind, &metadata, &created, &deleted)
	if err != nil {
		return Item{}, err
	}
	it.Overrides, it.Metadata = json.RawMessage(overrides), json.RawMessage(metadata)
	it.DurationS = dur
	it.LangMixed = mixed != 0
	it.CreatedAt = fromMs(created)
	it.PublishedAt = msToTime(pub)
	it.DeletedAt = msToTime(deleted)
	return it, nil
}

func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := fromMs(*ms)
	return &t
}

// InsertItemIfAbsent 新增影音；(platform, external_id, part) 已存在時不新增，回傳既有的影音，created 為 false（ING-R2）。
func (r *Repo) InsertItemIfAbsent(ctx context.Context, n NewItem) (it Item, created bool, err error) {
	overrides := n.Overrides
	if len(overrides) == 0 {
		overrides = json.RawMessage(`{}`)
	}
	id := NewID()
	res, err := r.q.ExecContext(ctx,
		`INSERT INTO items (id, platform, external_id, part, profile_id, overrides_json, until_stage, url, created_at)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)
		 ON CONFLICT (platform, external_id, part) DO NOTHING`,
		id, n.Platform, n.ExternalID, n.Part, n.ProfileID, string(overrides), n.UntilStage, n.URL, r.nowMs())
	if err != nil {
		return Item{}, false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return Item{}, false, err
	}
	if rows > 0 {
		it, err = r.ItemByID(ctx, id)
		return it, true, err
	}
	it, err = r.ItemByKey(ctx, n.Platform, n.ExternalID, n.Part)
	return it, false, err
}

// ItemByID 以 ID 查詢影音（含垃圾桶中的）；不存在時回傳 ErrNotFound。
func (r *Repo) ItemByID(ctx context.Context, id string) (Item, error) {
	it, err := scanItem(r.q.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Item{}, ErrNotFound
	}
	return it, err
}

// ItemByKey 以 (platform, external_id, part) 查詢影音（含垃圾桶中的）。
func (r *Repo) ItemByKey(ctx context.Context, platform, externalID string, part int) (Item, error) {
	it, err := scanItem(r.q.QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE platform = ? AND external_id = ? AND part = ?`,
		platform, externalID, part))
	if err == sql.ErrNoRows {
		return Item{}, ErrNotFound
	}
	return it, err
}

// ListItems 依新增順序列出影音；不含垃圾桶中的。
func (r *Repo) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE deleted_at IS NULL ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateItemMeta 寫入 metadata 步驟取得的欄位；沒有提供的欄位維持原值。
func (r *Repo) UpdateItemMeta(ctx context.Context, id string, m ItemMeta) error {
	metadata := m.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var dur any
	if m.DurationS != nil {
		dur = *m.DurationS
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE items SET
		   title         = COALESCE(NULLIF(?, ''), title),
		   duration_s    = COALESCE(?, duration_s),
		   published_at  = COALESCE(?, published_at),
		   original_lang = COALESCE(NULLIF(?, ''), original_lang),
		   lang_source   = CASE WHEN ? <> '' THEN ? ELSE lang_source END,
		   availability  = COALESCE(NULLIF(?, ''), availability),
		   channel_id    = COALESCE(NULLIF(?, ''), channel_id),
		   metadata_json = ?
		 WHERE id = ?`,
		m.Title, dur, msPtr(m.PublishedAt), m.OriginalLang, m.OriginalLang, m.LangSource,
		m.Availability, m.ChannelID, string(metadata), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("更新影音 %s：%w", id, ErrNotFound)
	}
	return nil
}

// UpsertChannel 新增或更新頻道，回傳頻道 ID。
func (r *Repo) UpsertChannel(ctx context.Context, c Channel) (string, error) {
	var id string
	err := r.q.QueryRowContext(ctx,
		`INSERT INTO channels (id, platform, external_id, title, url, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (platform, external_id) DO UPDATE SET
		   title = COALESCE(NULLIF(excluded.title, ''), title),
		   url = COALESCE(NULLIF(excluded.url, ''), url),
		   updated_at = excluded.updated_at
		 RETURNING id`,
		NewID(), c.Platform, c.ExternalID, c.Title, c.URL, r.nowMs()).Scan(&id)
	return id, err
}

// SetItemUntil 更新影音「做到哪個步驟為止」；空字串代表做到最後（PIPE-R1）。
func (r *Repo) SetItemUntil(ctx context.Context, id, until string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE items SET until_stage = ? WHERE id = ?`, until, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("更新影音 %s：%w", id, ErrNotFound)
	}
	return nil
}
