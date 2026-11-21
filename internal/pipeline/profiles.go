package pipeline

import (
	"context"
	"time"
)

// Profile 是處理方案目前的版本（PRF）。Config 是存下來的這一層，沒有合併內建預設。
type Profile struct {
	Name      string
	Version   int
	Config    map[string]any
	SHA256    string
	Builtin   bool
	IsDefault bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProfileVersion 是處理方案的一個歷史版本（不可修改，PRF-R3）。
type ProfileVersion struct {
	Version   int
	Config    map[string]any
	SHA256    string
	CreatedAt time.Time
}

// 以下是處理方案的 service（M2 S1/S2 設計第 6.2 節）。
// TODO(WP-2)：實作；契約階段全部回傳 *NotImplementedError，API 層（WP-4）據此回 501。

func errProfilesNotImplemented() error { return &NotImplementedError{What: "處理方案"} }

// Profiles 列出處理方案，依名稱排序。
func (p *Pipeline) Profiles(ctx context.Context) ([]Profile, error) {
	return nil, errProfilesNotImplemented()
}

// Profile 回傳一個處理方案；不存在時回傳 ErrProfileNotFound。
func (p *Pipeline) Profile(ctx context.Context, name string) (Profile, error) {
	return Profile{}, errProfilesNotImplemented()
}

// CreateProfile 建立處理方案（版本 1）。錯誤：*InvalidError（name 的錯誤排在 config 的錯誤之前）、ErrProfileExists。
func (p *Pipeline) CreateProfile(ctx context.Context, name string, cfg map[string]any) (Profile, error) {
	return Profile{}, errProfilesNotImplemented()
}

// PatchProfile 以 RFC 7396 merge patch 修改 config，建立新版本（內容相同也一樣，D2）。內建的 default 也可以修改。
func (p *Pipeline) PatchProfile(ctx context.Context, name string, patch map[string]any) (Profile, error) {
	return Profile{}, errProfilesNotImplemented()
}

// DeleteProfile 硬刪除處理方案與它的所有版本（D3）。錯誤：ErrProfileNotFound、ErrBuiltinProfile、
// 目前是預設處理方案時的 *InUseError（Code 為 CodeProfileInUse，UsedBy 為 ["default_profile"]）。
func (p *Pipeline) DeleteProfile(ctx context.Context, name string) error {
	return errProfilesNotImplemented()
}

// ProfileVersions 列出處理方案的所有版本，依版本遞增。
func (p *Pipeline) ProfileVersions(ctx context.Context, name string) ([]ProfileVersion, error) {
	return nil, errProfilesNotImplemented()
}

// SetDefaultProfile 設定預設處理方案（D4）。
func (p *Pipeline) SetDefaultProfile(ctx context.Context, name string) (Profile, error) {
	return Profile{}, errProfilesNotImplemented()
}

// DefaultProfileName 回傳預設處理方案的名稱；沒有設定時是 "default"。
func (p *Pipeline) DefaultProfileName(ctx context.Context) (string, error) {
	return "", errProfilesNotImplemented()
}

// PromptUsers 列出引用這個 prompt 的對象（D13）：處理方案名稱（依字母排序，只看目前版本）、
// "settings"（全域 stages 有引用時）、"channel:<platform>:<external_id>"。給 prompts.Deps.UsedBy。
func (p *Pipeline) PromptUsers(ctx context.Context, promptID string) ([]string, error) {
	return nil, errProfilesNotImplemented()
}
