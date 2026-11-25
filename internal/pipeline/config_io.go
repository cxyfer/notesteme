package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/cxyfer/notesteme/internal/configio"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 `notesteme config export|import`（SEC-R10）的 service。
//
// 範圍是保守的：DB 管理的設定中，只涵蓋處理方案（不含內建的 default）與預設處理方案。
// 不含 secrets：處理方案的內容只有設定值與 prompt、provider 的引用名稱，
// API key、cookie、token、密碼雜湊都不在處理方案裡，也不在這個流程碰到的任何資料表中。

// ExportConfig 回傳目前 DB 管理的設定（不含 secrets）。
// 預設處理方案只有在明確設定過時才會有值（沒設定過就是內建的 default，匯入時不需要重現）。
func (p *Pipeline) ExportConfig(ctx context.Context) (*configio.Document, error) {
	r := p.st.Read()
	rows, err := r.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	configs, err := r.CurrentProfileConfigs(ctx)
	if err != nil {
		return nil, err
	}
	builtin := map[string]bool{BuiltinProfileName: true}
	for _, pr := range rows {
		if pr.Builtin {
			builtin[pr.Name] = true
		}
	}
	doc := &configio.Document{Profiles: map[string]map[string]any{}}
	for name, raw := range configs {
		if builtin[name] {
			continue
		}
		cfg, err := decodeOverrides([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("處理方案 %q 的內容損毀：%w", name, err)
		}
		doc.Profiles[name] = orEmpty(cfg)
	}
	if def, ok, err := r.Setting(ctx, settingDefaultProfile); err != nil {
		return nil, err
	} else if ok {
		doc.DefaultProfile = def
	}
	return doc, nil
}

// ConfigImportResult 是匯入成功的結果。三個清單都依名稱排序，沒有項目時是空切片（不是 nil）。
type ConfigImportResult struct {
	Created   []string
	Updated   []string
	Unchanged []string
	// DefaultProfile 是檔案中的 default_profile；空字串代表檔案沒有這個鍵（沒有改動預設處理方案）。
	DefaultProfile string
	// DefaultChanged 表示預設處理方案因為這次匯入而不同。
	DefaultChanged bool
}

// ImportConfig 匯入 config export 的 YAML：先驗證整份，沒有任何錯誤才在同一個交易中寫入。
// 任何一個錯誤（YAML、頂層鍵、名稱、內建處理方案、內容、default_profile、檔案大小）都不會寫入任何東西，
// 回傳 *InvalidError，Fields 列出每一個錯誤的欄位路徑（處理方案內的路徑加上 profiles.<名稱> 前綴）。
//
// 語意是「新增或取代同名處理方案」，不刪除檔案裡沒有的處理方案。取代沿用 PRF-R3：建立新版本，
// 已完成的與進行中的步驟綁定的是當時的版本，不受影響；內容和目前版本完全相同時不新增版本（回報為未變更），
// 重複匯入同一份檔案才是冪等的。
func (p *Pipeline) ImportConfig(ctx context.Context, data []byte) (ConfigImportResult, error) {
	doc, errs := configio.Parse(data)
	if doc == nil {
		return ConfigImportResult{}, importInvalid(errs)
	}
	var res ConfigImportResult
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		errs = append(errs, p.validateImport(ctx, r, doc)...)
		if len(errs) > 0 {
			return importInvalid(errs)
		}
		var err error
		res, err = p.applyImport(ctx, r, doc)
		return err
	})
	if err != nil {
		return ConfigImportResult{}, err
	}
	return res, nil
}

// importInvalid 組出匯入失敗的 422：訊息說明整份都沒有套用，每個錯誤的欄位路徑在 Fields。
func importInvalid(errs []validate.FieldError) *InvalidError {
	return &InvalidError{
		Msg:    fmt.Sprintf("匯入內容有 %d 個錯誤，沒有套用任何設定。", len(errs)),
		Fields: errs,
	}
}

func sortedProfileNames(m map[string]map[string]any) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// validateImport 檢查匯入內容，回傳全部的錯誤（依處理方案名稱排序，最後是 default_profile）。
// 處理方案的內容用和 `POST /api/profiles` 完全相同的檢查（schema、完整性、合併後的語意）。
func (p *Pipeline) validateImport(ctx context.Context, r *store.Repo, doc *configio.Document) []validate.FieldError {
	var errs []validate.FieldError
	for _, name := range sortedProfileNames(doc.Profiles) {
		if !profileNameRE.MatchString(name) {
			// 名稱是不可信的輸入：用 %q 印出，控制字元不會原樣進到終端機。路徑只能放 profiles。
			errs = append(errs, validate.FieldError{Path: configio.KeyProfiles, Message: fmt.Sprintf("處理方案名稱 %q 不正確：名稱只能用小寫英文、數字、底線與連字號，開頭必須是英文或數字，最長 64 個字元。", name)})
			continue
		}
		prefix := configio.KeyProfiles + "." + name
		if name == BuiltinProfileName {
			errs = append(errs, validate.FieldError{Path: prefix, Message: "內建的處理方案不能被匯入覆蓋。"})
			continue
		}
		if pr, err := r.Profile(ctx, name); err == nil && pr.Builtin {
			errs = append(errs, validate.FieldError{Path: prefix, Message: "內建的處理方案不能被匯入覆蓋。"})
			continue
		}
		cfg := doc.Profiles[name]
		if fs := ValidateLayer(ctx, cfg, KindProfile, p.validateDeps()); len(fs) > 0 {
			errs = append(errs, validate.Prefix(prefix, fs)...)
			continue
		}
		// 通過 schema 之後再做合併設定的語意檢查，和 CreateProfile、PatchProfile 一樣。
		if _, err := BuildEffective(cfg); err != nil {
			var inv *InvalidError
			if errors.As(err, &inv) {
				if len(inv.Fields) > 0 {
					errs = append(errs, validate.Prefix(prefix, inv.Fields)...)
				} else {
					errs = append(errs, validate.FieldError{Path: prefix, Message: inv.Msg})
				}
				continue
			}
			errs = append(errs, validate.FieldError{Path: prefix, Message: fmt.Sprintf("設定無法使用：%s", err.Error())})
		}
	}

	if def := doc.DefaultProfile; def != "" {
		// 匯入後要存在：在檔案中（不論內容是否通過，錯誤已另外列出），或本來就存在（含內建的 default）。
		if _, inFile := doc.Profiles[def]; !inFile {
			if _, err := r.Profile(ctx, def); err != nil {
				errs = append(errs, validate.FieldError{Path: configio.KeyDefaultProfile, Message: fmt.Sprintf("找不到處理方案 %q；default_profile 必須是這個檔案中的處理方案，或已經存在的處理方案。", def)})
			}
		}
	}
	return errs
}

// applyImport 在呼叫端的交易內寫入已經驗證過的內容。寫入前再檢查一次內建與存在與否（驗證到寫入之間
// 同一個交易，不會有別人插進來；這裡的檢查只是防止寫入內建處理方案的最後一道關卡）。
func (p *Pipeline) applyImport(ctx context.Context, r *store.Repo, doc *configio.Document) (ConfigImportResult, error) {
	res := ConfigImportResult{Created: []string{}, Updated: []string{}, Unchanged: []string{}, DefaultProfile: doc.DefaultProfile}
	for _, name := range sortedProfileNames(doc.Profiles) {
		cfgJSON, sha, err := profileConfigJSON(doc.Profiles[name])
		if err != nil {
			return ConfigImportResult{}, err
		}
		pr, err := r.Profile(ctx, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			if _, err := r.CreateProfile(ctx, name, cfgJSON, sha); err != nil {
				return ConfigImportResult{}, err
			}
			res.Created = append(res.Created, name)
		case err != nil:
			return ConfigImportResult{}, err
		case pr.Builtin:
			return ConfigImportResult{}, importInvalid([]validate.FieldError{{Path: configio.KeyProfiles + "." + name, Message: "內建的處理方案不能被匯入覆蓋。"}})
		default:
			cur, err := r.ProfileVersion(ctx, name, pr.CurrentVersion)
			if err != nil {
				return ConfigImportResult{}, err
			}
			if cur.SHA256 == sha {
				res.Unchanged = append(res.Unchanged, name)
				continue
			}
			if _, err := r.AddProfileVersion(ctx, name, cfgJSON, sha); err != nil {
				return ConfigImportResult{}, err
			}
			res.Updated = append(res.Updated, name)
		}
	}

	if def := doc.DefaultProfile; def != "" {
		if _, err := r.Profile(ctx, def); err != nil {
			return ConfigImportResult{}, profileLookupErr(err)
		}
		stored, ok, err := r.Setting(ctx, settingDefaultProfile)
		if err != nil {
			return ConfigImportResult{}, err
		}
		before := BuiltinProfileName
		if ok && stored != "" {
			before = stored
		}
		res.DefaultChanged = before != def
		if !ok || stored != def {
			if err := r.SetSetting(ctx, settingDefaultProfile, def); err != nil {
				return ConfigImportResult{}, err
			}
		}
	}
	return res, nil
}
