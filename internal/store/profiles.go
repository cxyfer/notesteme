package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Profile 是處理方案（PRF）：CurrentVersion 指向 profile_versions 中最新的一版。
type Profile struct {
	Name           string
	CurrentVersion int
	Builtin        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProfileVersion 是處理方案的一個版本（不可修改，PRF-R3）。ConfigJSON 是存下來的這一層，沒有合併內建預設。
type ProfileVersion struct {
	Name       string
	Version    int
	ConfigJSON string
	SHA256     string
	CreatedAt  time.Time
}

func profileFromRow(row sqlcdb.Profile) Profile {
	return Profile{
		Name: row.Name, CurrentVersion: int(row.CurrentVersion), Builtin: row.Builtin != 0,
		CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt),
	}
}

func profileVersionFromRow(row sqlcdb.ProfileVersion) ProfileVersion {
	return ProfileVersion{
		Name: row.Name, Version: int(row.Version), ConfigJSON: row.ConfigJson, SHA256: row.Sha256, CreatedAt: fromMs(row.CreatedAt),
	}
}

// Profiles 列出處理方案，依名稱排序。
func (r *Repo) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := r.q.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(rows))
	for _, row := range rows {
		out = append(out, profileFromRow(row))
	}
	return out, nil
}

// Profile 以名稱查詢處理方案；不存在時回傳 ErrNotFound。
func (r *Repo) Profile(ctx context.Context, name string) (Profile, error) {
	row, err := r.q.GetProfile(ctx, name)
	if err != nil {
		return Profile{}, notFound(err)
	}
	return profileFromRow(row), nil
}

// ProfileVersion 查詢處理方案的某一版；不存在時回傳 ErrNotFound。
func (r *Repo) ProfileVersion(ctx context.Context, name string, version int) (ProfileVersion, error) {
	row, err := r.q.GetProfileVersion(ctx, sqlcdb.GetProfileVersionParams{Name: name, Version: int64(version)})
	if err != nil {
		return ProfileVersion{}, notFound(err)
	}
	return profileVersionFromRow(row), nil
}

// ProfileVersions 列出處理方案的所有版本，依版本遞增。
func (r *Repo) ProfileVersions(ctx context.Context, name string) ([]ProfileVersion, error) {
	rows, err := r.q.ListProfileVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileVersion, 0, len(rows))
	for _, row := range rows {
		out = append(out, profileVersionFromRow(row))
	}
	return out, nil
}

// CreateProfile 建立處理方案與它的版本 1；已有同名的處理方案時回傳 ErrConflict。
func (r *Repo) CreateProfile(ctx context.Context, name, configJSON, sha string) (pv ProfileVersion, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		if _, gerr := tx.q.GetProfile(ctx, name); gerr == nil {
			return fmt.Errorf("%w: 已經有同名的處理方案 %q", ErrConflict, name)
		} else if !errors.Is(gerr, sql.ErrNoRows) {
			return gerr
		}
		now := tx.nowMs()
		if err := tx.q.InsertProfile(ctx, sqlcdb.InsertProfileParams{Name: name, CreatedAt: now}); err != nil {
			return err
		}
		if err := tx.q.InsertProfileVersion(ctx, sqlcdb.InsertProfileVersionParams{
			Name: name, Version: 1, ConfigJson: configJSON, Sha256: sha, CreatedAt: now,
		}); err != nil {
			return err
		}
		pv = ProfileVersion{Name: name, Version: 1, ConfigJSON: configJSON, SHA256: sha, CreatedAt: fromMs(now)}
		return nil
	})
	if err != nil {
		return ProfileVersion{}, err
	}
	return pv, nil
}

// AddProfileVersion 為處理方案新增一版（內容相同也一樣，D2）並讓它成為目前的版本；處理方案不存在時回傳 ErrNotFound。
func (r *Repo) AddProfileVersion(ctx context.Context, name, configJSON, sha string) (pv ProfileVersion, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		now := tx.nowMs()
		v, berr := tx.q.BumpProfileVersion(ctx, sqlcdb.BumpProfileVersionParams{UpdatedAt: now, Name: name})
		if berr != nil {
			return notFound(berr)
		}
		if err := tx.q.InsertProfileVersion(ctx, sqlcdb.InsertProfileVersionParams{
			Name: name, Version: v, ConfigJson: configJSON, Sha256: sha, CreatedAt: now,
		}); err != nil {
			return err
		}
		pv = ProfileVersion{Name: name, Version: int(v), ConfigJSON: configJSON, SHA256: sha, CreatedAt: fromMs(now)}
		return nil
	})
	if err != nil {
		return ProfileVersion{}, err
	}
	return pv, nil
}

// DeleteProfile 硬刪除處理方案與它的所有版本（D3）。內建的不會被刪除；回傳是否刪除了資料列。
// 版本靠 ON DELETE CASCADE 一起刪除（foreign_keys 在每個連線都開著，見 openDB）。
func (r *Repo) DeleteProfile(ctx context.Context, name string) (bool, error) {
	n, err := r.q.DeleteProfile(ctx, name)
	return n > 0, err
}

// CurrentProfileConfigs 回傳每個處理方案目前版本的 config JSON（name → JSON）。
func (r *Repo) CurrentProfileConfigs(ctx context.Context) (map[string]string, error) {
	rows, err := r.q.CurrentProfileConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Name] = row.ConfigJson
	}
	return out, nil
}

// Setting 在這個 Repo（可能是交易）中讀取 settings 的值；不存在時 ok 為 false。
func (r *Repo) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	value, err = r.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting 在這個 Repo（可能是交易）中寫入或覆寫 settings 的值。
func (r *Repo) SetSetting(ctx context.Context, key, value string) error {
	return r.q.SetSetting(ctx, sqlcdb.SetSettingParams{Key: key, Value: value, UpdatedAt: r.nowMs()})
}
