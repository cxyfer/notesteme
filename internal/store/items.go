package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// itemFromRow 把查詢結果轉成 Item；可為空的文字欄位 NULL 視為空字串。
func itemFromRow(row sqlcdb.Item) Item {
	it := Item{
		ID:             row.ID,
		Platform:       row.Platform,
		ExternalID:     row.ExternalID,
		Part:           int(row.Part),
		ChannelID:      deref(row.ChannelID),
		ProfileID:      deref(row.ProfileID),
		ProfileVersion: derefInt(row.ProfileVersion),
		Overrides:      json.RawMessage(row.OverridesJson),
		UntilStage:     row.UntilStage,
		Title:          row.Title,
		URL:            row.Url,
		PublishedAt:    msToTime(row.PublishedAt),
		OriginalLang:   deref(row.OriginalLang),
		LangSource:     deref(row.LangSource),
		LangMixed:      row.LangMixed != 0,
		Availability:   deref(row.Availability),
		MediaKind:      deref(row.MediaKind),
		Metadata:       json.RawMessage(row.MetadataJson),
		CreatedAt:      fromMs(row.CreatedAt),
		DeletedAt:      msToTime(row.DeletedAt),

		SourcePath:    row.SourcePath,
		ContentSHA256: row.ContentSha256,
		Origin:        row.Origin,
	}
	if row.DurationS != nil {
		d := int(*row.DurationS)
		it.DurationS = &d
	}
	return it
}

// InsertItemIfAbsent 新增影音；(platform, external_id, part) 已存在時不新增，回傳既有的影音，created 為 false（ING-R2）。
func (r *Repo) InsertItemIfAbsent(ctx context.Context, n NewItem) (it Item, created bool, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		var ierr error
		it, created, ierr = tx.insertItemIfAbsent(ctx, n)
		return ierr
	})
	if err != nil {
		return Item{}, false, err
	}
	return it, created, nil
}

func (r *Repo) insertItemIfAbsent(ctx context.Context, n NewItem) (it Item, created bool, err error) {
	overrides := n.Overrides
	if len(overrides) == 0 {
		overrides = json.RawMessage(`{}`)
	}
	id := n.ID
	if id == "" {
		id = NewID()
	}
	origin := n.Origin
	if origin == "" {
		origin = OriginManual
	}
	rows, err := r.q.InsertItemIfAbsent(ctx, sqlcdb.InsertItemIfAbsentParams{
		ID:             id,
		Platform:       n.Platform,
		ExternalID:     n.ExternalID,
		Part:           int64(n.Part),
		ProfileID:      n.ProfileID,
		ProfileVersion: int64(n.ProfileVersion),
		OverridesJson:  string(overrides),
		UntilStage:     n.UntilStage,
		Url:            n.URL,
		SourcePath:     n.SourcePath,
		ContentSha256:  n.ContentSHA256,
		Origin:         origin,
		CreatedAt:      r.nowMs(),
	})
	if err != nil {
		return Item{}, false, err
	}
	if rows > 0 {
		it, err = r.ItemByID(ctx, id)
		if err == nil {
			err = r.emitItemCreated(ctx, it)
		}
		return it, true, err
	}
	it, err = r.ItemByKey(ctx, n.Platform, n.ExternalID, n.Part)
	return it, false, err
}

// ItemByID 以 ID 查詢影音（含垃圾桶中的）；不存在時回傳 ErrNotFound。
func (r *Repo) ItemByID(ctx context.Context, id string) (Item, error) {
	row, err := r.q.ItemByID(ctx, id)
	if err != nil {
		return Item{}, notFound(err)
	}
	return itemFromRow(row), nil
}

// ItemByKey 以 (platform, external_id, part) 查詢影音（含垃圾桶中的）。
func (r *Repo) ItemByKey(ctx context.Context, platform, externalID string, part int) (Item, error) {
	row, err := r.q.ItemByKey(ctx, sqlcdb.ItemByKeyParams{Platform: platform, ExternalID: externalID, Part: int64(part)})
	if err != nil {
		return Item{}, notFound(err)
	}
	return itemFromRow(row), nil
}

// ListItems 依新增順序列出影音；不含垃圾桶中的。
func (r *Repo) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := r.q.ListItems(ctx)
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, row := range rows {
		out = append(out, itemFromRow(row))
	}
	return out, nil
}

// FoldTitle 是標題搜尋比對用的形式（LIB-R3）：Go 的 strings.ToLower。
// 不用 SQLite 的 lower() 或 LIKE，它們只對 ASCII 不分大小寫。
func FoldTitle(s string) string { return strings.ToLower(s) }

// UpdateItemMeta 寫入 metadata 步驟取得的欄位；沒有提供的欄位維持原值。
func (r *Repo) UpdateItemMeta(ctx context.Context, id string, m ItemMeta) error {
	metadata := m.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var dur *int64
	if m.DurationS != nil {
		d := int64(*m.DurationS)
		dur = &d
	}
	n, err := r.q.UpdateItemMeta(ctx, sqlcdb.UpdateItemMetaParams{
		Title:         m.Title,
		TitleFold:     FoldTitle(m.Title),
		DurationS:     dur,
		PublishedAt:   msPtr(m.PublishedAt),
		OriginalLang:  m.OriginalLang,
		LangSource:    m.LangSource,
		Availability:  m.Availability,
		ChannelID:     m.ChannelID,
		MetadataJson:  string(metadata),
		MediaKind:     m.MediaKind,
		ContentSha256: m.ContentSHA256,
		ID:            id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("更新影音 %s：%w", id, ErrNotFound)
	}
	return nil
}

// UpsertChannel 新增或更新頻道，回傳頻道 ID。
func (r *Repo) UpsertChannel(ctx context.Context, c Channel) (string, error) {
	return r.q.UpsertChannel(ctx, sqlcdb.UpsertChannelParams{
		ID:         NewID(),
		Platform:   c.Platform,
		ExternalID: c.ExternalID,
		Title:      c.Title,
		Url:        c.URL,
		UpdatedAt:  r.nowMs(),
	})
}

// SetItemProfile 記錄影音最近一次新增或重跑用的處理方案與版本（D7）。
func (r *Repo) SetItemProfile(ctx context.Context, itemID, name string, version int) error {
	v := int64(version)
	n, err := r.q.SetItemProfile(ctx, sqlcdb.SetItemProfileParams{ProfileID: &name, ProfileVersion: &v, ID: itemID})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("更新影音 %s：%w", itemID, ErrNotFound)
	}
	return nil
}

// ChannelByID 以 ID 查詢頻道；不存在時回傳 ErrNotFound。
func (r *Repo) ChannelByID(ctx context.Context, id string) (Channel, error) {
	row, err := r.q.GetChannel(ctx, id)
	if err != nil {
		return Channel{}, notFound(err)
	}
	return channelFromRow(row), nil
}

// SetChannelOverrides 寫入頻道層的覆寫（整份取代）；頻道不存在時回傳 ErrNotFound。
func (r *Repo) SetChannelOverrides(ctx context.Context, channelID, overridesJSON string) error {
	n, err := r.q.SetChannelOverrides(ctx, sqlcdb.SetChannelOverridesParams{OverridesJson: overridesJSON, UpdatedAt: r.nowMs(), ID: channelID})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("更新頻道 %s：%w", channelID, ErrNotFound)
	}
	return nil
}

// ChannelsWithOverrides 列出有頻道層覆寫的頻道。
func (r *Repo) ChannelsWithOverrides(ctx context.Context) ([]Channel, error) {
	rows, err := r.q.ChannelsWithOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelFromRow(row))
	}
	return out, nil
}

func derefInt(n *int64) int {
	if n == nil {
		return 0
	}
	return int(*n)
}

// SetItemUntil 更新影音「做到哪個步驟為止」；空字串代表做到最後（PIPE-R1）。
func (r *Repo) SetItemUntil(ctx context.Context, id, until string) error {
	n, err := r.q.SetItemUntil(ctx, sqlcdb.SetItemUntilParams{UntilStage: until, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("更新影音 %s：%w", id, ErrNotFound)
	}
	return nil
}

// SetItemLangMixed 標記影音的原文語言是否為 mixed（夾雜多種語言，COR-R5）；影音不存在時回傳 ErrNotFound。
func (r *Repo) SetItemLangMixed(ctx context.Context, id string, mixed bool) error {
	v := int64(0)
	if mixed {
		v = 1
	}
	n, err := r.q.SetItemLangMixed(ctx, sqlcdb.SetItemLangMixedParams{LangMixed: v, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
