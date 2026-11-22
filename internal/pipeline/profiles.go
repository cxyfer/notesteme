package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
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

// BuiltinProfileName 是內建的預設處理方案；它的 config 是空的一層，所以套用它等於 M1 的生效設定。
const BuiltinProfileName = "default"

// settingDefaultProfile 是預設處理方案在 settings 表中的 key（D4）。
const settingDefaultProfile = "profiles.default"

// profileNameRE 是處理方案名稱的規則（D1）。
var profileNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// profileConfigJSON 把一層設定序列化（encoding/json 對 map 依 key 排序）並算出 sha256（對序列化的內容）。
func profileConfigJSON(cfg map[string]any) (configJSON, sha string, err error) {
	b, err := json.Marshal(orEmpty(cfg))
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// profileFrom 把 store 的處理方案與它目前的版本組成 Profile。
func profileFrom(pr store.Profile, pv store.ProfileVersion, defaultName string) (Profile, error) {
	cfg, err := decodeOverrides(json.RawMessage(pv.ConfigJSON))
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		Name: pr.Name, Version: pv.Version, Config: orEmpty(cfg), SHA256: pv.SHA256, Builtin: pr.Builtin,
		IsDefault: pr.Name == defaultName, CreatedAt: pr.CreatedAt, UpdatedAt: pr.UpdatedAt,
	}, nil
}

func profileLookupErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrProfileNotFound
	}
	return err
}

// Profiles 列出處理方案，依名稱排序。
func (p *Pipeline) Profiles(ctx context.Context) ([]Profile, error) {
	def, err := p.DefaultProfileName(ctx)
	if err != nil {
		return nil, err
	}
	r := p.st.Read()
	rows, err := r.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(rows))
	for _, pr := range rows {
		pv, err := r.ProfileVersion(ctx, pr.Name, pr.CurrentVersion)
		if err != nil {
			return nil, err
		}
		pf, err := profileFrom(pr, pv, def)
		if err != nil {
			return nil, err
		}
		out = append(out, pf)
	}
	return out, nil
}

// Profile 回傳一個處理方案；不存在時回傳 ErrProfileNotFound。
func (p *Pipeline) Profile(ctx context.Context, name string) (Profile, error) {
	def, err := p.DefaultProfileName(ctx)
	if err != nil {
		return Profile{}, err
	}
	return p.profileOf(ctx, p.st.Read(), name, def)
}

func (p *Pipeline) profileOf(ctx context.Context, r *store.Repo, name, defaultName string) (Profile, error) {
	pr, err := r.Profile(ctx, name)
	if err != nil {
		return Profile{}, profileLookupErr(err)
	}
	pv, err := r.ProfileVersion(ctx, name, pr.CurrentVersion)
	if err != nil {
		return Profile{}, err
	}
	return profileFrom(pr, pv, defaultName)
}

// validateProfileConfig 以處理方案文件的規則檢查 config（含完整性，D8）。
func (p *Pipeline) validateProfileConfig(ctx context.Context, cfg map[string]any) error {
	if fs := ValidateLayer(ctx, cfg, KindProfile, p.validateDeps()); len(fs) > 0 {
		return invalidFields(fs)
	}
	// 通過 schema 之後再做合併設定的語意檢查，確保這一層疊上內建預設是可用的設定。
	if _, err := BuildEffective(cfg); err != nil {
		return err
	}
	return nil
}

// CreateProfile 建立處理方案（版本 1）。錯誤：*InvalidError（name 的錯誤排在 config 的錯誤之前）、ErrProfileExists。
func (p *Pipeline) CreateProfile(ctx context.Context, name string, cfg map[string]any) (Profile, error) {
	var fs []validate.FieldError
	if !profileNameRE.MatchString(name) {
		fs = append(fs, validate.FieldError{Path: "name", Message: "名稱只能用小寫英文、數字、底線與連字號，開頭必須是英文或數字，最長 64 個字元。"})
	}
	cfg = orEmpty(cfg)
	fs = append(fs, ValidateLayer(ctx, cfg, KindProfile, p.validateDeps())...)
	if len(fs) > 0 {
		return Profile{}, invalidFields(fs)
	}
	if _, err := BuildEffective(cfg); err != nil {
		return Profile{}, err
	}
	cfgJSON, sha, err := profileConfigJSON(cfg)
	if err != nil {
		return Profile{}, err
	}
	if _, err := p.st.Write().CreateProfile(ctx, name, cfgJSON, sha); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return Profile{}, ErrProfileExists
		}
		return Profile{}, err
	}
	return p.Profile(ctx, name)
}

// PatchProfile 以 RFC 7396 merge patch 修改 config，建立新版本（內容相同也一樣，D2）。內建的 default 也可以修改。
func (p *Pipeline) PatchProfile(ctx context.Context, name string, patch map[string]any) (Profile, error) {
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		pr, err := r.Profile(ctx, name)
		if err != nil {
			return profileLookupErr(err)
		}
		cur, err := r.ProfileVersion(ctx, name, pr.CurrentVersion)
		if err != nil {
			return err
		}
		base, err := decodeOverrides(json.RawMessage(cur.ConfigJSON))
		if err != nil {
			return err
		}
		merged := MergePatch(base, patch)
		if err := p.validateProfileConfig(ctx, merged); err != nil {
			return err
		}
		cfgJSON, sha, err := profileConfigJSON(merged)
		if err != nil {
			return err
		}
		_, err = r.AddProfileVersion(ctx, name, cfgJSON, sha)
		return err
	})
	if err != nil {
		return Profile{}, err
	}
	return p.Profile(ctx, name)
}

// DeleteProfile 硬刪除處理方案與它的所有版本（D3）。錯誤：ErrProfileNotFound、ErrBuiltinProfile、
// 目前是預設處理方案時的 *InUseError（Code 為 CodeProfileInUse，UsedBy 為 ["default_profile"]）。
func (p *Pipeline) DeleteProfile(ctx context.Context, name string) error {
	return p.st.Tx(ctx, func(r *store.Repo) error {
		pr, err := r.Profile(ctx, name)
		if err != nil {
			return profileLookupErr(err)
		}
		if pr.Builtin {
			return ErrBuiltinProfile
		}
		// 在同一個交易中讀預設值：和 SetDefaultProfile 互斥，不會刪掉剛被設為預設的處理方案。
		def, ok, err := r.Setting(ctx, settingDefaultProfile)
		if err != nil {
			return err
		}
		if !ok || def == "" {
			def = BuiltinProfileName
		}
		if def == name {
			return &InUseError{Code: CodeProfileInUse, UsedBy: []string{"default_profile"}}
		}
		deleted, err := r.DeleteProfile(ctx, name)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrProfileNotFound
		}
		return nil
	})
}

// ProfileVersions 列出處理方案的所有版本，依版本遞增。
func (p *Pipeline) ProfileVersions(ctx context.Context, name string) ([]ProfileVersion, error) {
	r := p.st.Read()
	if _, err := r.Profile(ctx, name); err != nil {
		return nil, profileLookupErr(err)
	}
	rows, err := r.ProfileVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileVersion, 0, len(rows))
	for _, pv := range rows {
		cfg, err := decodeOverrides(json.RawMessage(pv.ConfigJSON))
		if err != nil {
			return nil, err
		}
		out = append(out, ProfileVersion{Version: pv.Version, Config: orEmpty(cfg), SHA256: pv.SHA256, CreatedAt: pv.CreatedAt})
	}
	return out, nil
}

// SetDefaultProfile 設定預設處理方案（D4）。
func (p *Pipeline) SetDefaultProfile(ctx context.Context, name string) (Profile, error) {
	// 檢查與寫入在同一個交易中：不會指向同時被刪除的處理方案。
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		if _, err := r.Profile(ctx, name); err != nil {
			return profileLookupErr(err)
		}
		return r.SetSetting(ctx, settingDefaultProfile, name)
	})
	if err != nil {
		return Profile{}, err
	}
	return p.Profile(ctx, name)
}

// DefaultProfileName 回傳預設處理方案的名稱；沒有設定時是 "default"。
func (p *Pipeline) DefaultProfileName(ctx context.Context) (string, error) {
	v, ok, err := p.st.GetSetting(ctx, settingDefaultProfile)
	if err != nil {
		return "", err
	}
	if !ok || v == "" {
		return BuiltinProfileName, nil
	}
	return v, nil
}

// maxPromptUserItems 是 PromptUsers 最多列出的影音數；更多時只附上總數，不把整個媒體庫塞進錯誤訊息。
const maxPromptUserItems = 20

// PromptUsers 列出引用這個 prompt 的對象（D13、P7）：處理方案名稱（依字母排序，只看目前版本）、
// "settings"（全域 stages 有引用時）、"channel:<platform>:<external_id>"、"item:<影音代號>"（影音層的覆寫，
// 垃圾桶中的也算，最多 maxPromptUserItems 個，多出的以一筆「另有 N 個影音」帶總數）。
// 用唯讀連線，給 API 顯示「使用中」；刪除前的檢查要用 PromptUsersIn，才和刪除在同一個交易。
func (p *Pipeline) PromptUsers(ctx context.Context, promptID string) ([]string, error) {
	return promptUsersIn(ctx, p.st.Read(), promptID)
}

// PromptUsersIn 與 PromptUsers 相同，但在給定的 Repo（唯讀或 Store.Tx 傳入的交易）上掃描。
// 給 prompts.Deps.UsedBy：prompt 的刪除在同一個寫入交易內檢查，之後才新增的引用要等交易結束才寫得進去。
func (p *Pipeline) PromptUsersIn(ctx context.Context, r *store.Repo, promptID string) ([]string, error) {
	return promptUsersIn(ctx, r, promptID)
}

func promptUsersIn(ctx context.Context, r *store.Repo, promptID string) ([]string, error) {
	users := []string{}

	configs, err := r.CurrentProfileConfigs(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg, err := decodeOverrides(json.RawMessage(configs[name]))
		if err != nil {
			return nil, err
		}
		if layerUsesPrompt(cfg, promptID) {
			users = append(users, name)
		}
	}

	global, err := globalConfigIn(ctx, r)
	if err != nil {
		return nil, err
	}
	if layerUsesPrompt(global, promptID) {
		users = append(users, "settings")
	}

	channels, err := r.ChannelsWithOverrides(ctx)
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		ov, err := decodeOverrides(ch.Overrides)
		if err != nil {
			return nil, err
		}
		if layerUsesPrompt(ov, promptID) {
			users = append(users, "channel:"+ch.Platform+":"+ch.ExternalID)
		}
	}

	// 影音層的覆寫（items.overrides_json）：SQL 先篩出可能的列，這裡解析 JSON 確認真的引用。
	items, err := r.ItemsOverridingPrompt(ctx, promptID)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, it := range items {
		ov, err := decodeOverrides(it.Overrides)
		if err != nil {
			return nil, err
		}
		if !layerUsesPrompt(ov, promptID) {
			continue
		}
		total++
		if total <= maxPromptUserItems {
			users = append(users, "item:"+source.Ref(it.Platform, it.ExternalID, it.Part))
		}
	}
	if total > maxPromptUserItems {
		users = append(users, fmt.Sprintf("…（另有 %d 個影音）", total-maxPromptUserItems))
	}
	return users, nil
}

// layerUsesPrompt 回報設定層的 stages.<s>.prompt 有沒有引用這個 prompt（引用格式 "<id>"、"<id>@default"、"<id>@<N>"）。
func layerUsesPrompt(layer map[string]any, promptID string) bool {
	stages, _ := layer["stages"].(map[string]any)
	for _, s := range []string{StageSegment, StageCorrect, StageTranslate} {
		entry, _ := stages[s].(map[string]any)
		ref, _ := entry["prompt"].(string)
		if ref == "" {
			continue
		}
		id, _, _ := strings.Cut(ref, "@")
		if id == promptID {
			return true
		}
	}
	return false
}
