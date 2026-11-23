// Package prompts 是 prompt 庫（PRM）：使用者的 prompt 與它的版本存在資料庫，內建 prompt 隨程式發布。
//
// 內建 prompt（BuiltinIDs）的內文放在 builtin/<id>/v<N>.md（embed），不寫入資料庫，不能修改（ErrBuiltin）；
// 使用者的 prompt id 不可以和內建的相同（D12）。
package prompts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// Kinds 是 prompt 的類型（PRM-R1）。
var Kinds = []string{"summary", "chapters", "segment", "correct", "translate", "frames", "note", "note_map"}

// Variables 是 prompt 內文可以使用的 {{變數}}（PRM-R3）。
var Variables = []string{"title", "channel", "description", "chapters", "glossary", "source_lang", "target_lang"}

// BuiltinIDs 是內建的 prompt（PRM-R4）。內容放在程式內，不寫入資料庫；使用者的 prompt id 不可以和它們相同（D12）。
var BuiltinIDs = []string{"segment-default", "correct-default", "translate-default", "localize-zh-hant-tw", "localize-zh-hans-cn", "chapters-default",
	// M3：FRM 的選圖（PRM 的 M3 修訂）。
	"frames-default"}

// maxBodyBytes 是 prompt 內文的上限（64 KiB）。
const maxBodyBytes = 64 << 10

// 錯誤。API 層把它們對應成 404、409、422。
var (
	// ErrNotFound：沒有這個 prompt。
	ErrNotFound = errors.New("找不到這個 prompt。")
	// ErrVersionNotFound：prompt 沒有這個版本。
	ErrVersionNotFound = errors.New("找不到這個 prompt 的指定版本。")
	// ErrExists：已經有同 id 的 prompt。
	ErrExists = errors.New("已經有同 id 的 prompt。")
	// ErrBuiltin：內建 prompt 不能修改（409 builtin_prompt）。
	ErrBuiltin = errors.New("內建 prompt 不能修改，請另存為新的 prompt。")
)

// InvalidError 是輸入不正確（422）。Fields 是依固定順序排列的欄位錯誤（id → kind → body → 未定義的變數）；
// Undefined 是 body 用到但不在 Variables 內的變數，依第一次出現的順序、去掉重複。
type InvalidError struct {
	Msg       string
	Fields    []validate.FieldError
	Undefined []string
}

func (e *InvalidError) Error() string { return e.Msg }

// InUseError 表示 prompt 還被引用，不能刪除（409 prompt_in_use）。UsedBy 的順序見 Deps.UsedBy。
type InUseError struct{ UsedBy []string }

func (e *InUseError) Error() string { return "這個 prompt 還在使用中，不能刪除。" }

// Prompt 是一個 prompt 的摘要。DefaultVersion 是 "<id>" 與 "<id>@default" 解析到的版本，
// 只有 SetDefault 會改變它（PRM-R5）；LatestVersion 是最新儲存的版本。
type Prompt struct {
	ID             string
	Kind           string
	Builtin        bool
	DefaultVersion int
	LatestVersion  int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Version 是 prompt 的一個版本（不可修改，PRM-R2）。Variables 是內文用到的變數，依第一次出現的順序。
type Version struct {
	PromptID  string
	Version   int
	Body      string
	SHA256    string
	Variables []string
	CreatedAt time.Time
}

// Resolved 是解析後的引用：實際指到的版本與內文。
type Resolved struct {
	ID      string
	Kind    string
	Version int
	SHA256  string
	Body    string
}

// BodySHA256 回傳內文的 sha256（十六進位小寫）。內文要先把 CRLF 換成 LF 再計算，見 Create 與 AddVersion。
func BodySHA256(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// normalizeBody 把 CRLF（與單獨的 CR）換成 LF；儲存與計算 sha256 都用正規化後的內文。
func normalizeBody(body string) string {
	return strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
}

// Deps 是 Service 的依賴。
type Deps struct {
	Store *store.Store
	Clock clock.Clock
	// UsedBy 回傳引用這個 prompt 的對象（刪除前檢查，D13、P7），app 接到 Pipeline.PromptUsersIn。
	// r 是刪除所在的寫入交易：檢查必須用它讀，不能另開連線，否則檢查與刪除之間仍可能有新的引用寫進來。
	// 順序：引用它的處理方案名稱（依字母排序）、"settings"、"channel:<platform>:<external_id>"、"item:<影音代號>"。
	// nil 代表沒有人引用。
	UsedBy func(ctx context.Context, r *store.Repo, id string) ([]string, error)
}

// Service 是 prompt 庫。
type Service struct {
	st     *store.Store
	clock  clock.Clock
	usedBy func(ctx context.Context, r *store.Repo, id string) ([]string, error)
}

// New 建立 Service。
func New(d Deps) *Service {
	return &Service{st: d.Store, clock: d.Clock, usedBy: d.UsedBy}
}

func promptFromStore(p store.Prompt) Prompt {
	return Prompt{
		ID: p.ID, Kind: p.Kind, DefaultVersion: p.DefaultVersion, LatestVersion: p.LatestVersion,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func versionFromStore(v store.PromptVersion) Version {
	vars := []string{}
	if err := json.Unmarshal([]byte(v.VariablesJSON), &vars); err != nil || vars == nil {
		vars = []string{}
	}
	return Version{
		PromptID: v.PromptID, Version: v.Version, Body: v.Body, SHA256: v.SHA256, Variables: vars, CreatedAt: v.CreatedAt,
	}
}

// List 列出所有 prompt：內建的在前，依 id 排序，接著是使用者的，依 id 排序。
func (s *Service) List(ctx context.Context) ([]Prompt, error) {
	ids := slices.Clone(BuiltinIDs)
	slices.Sort(ids)
	out := make([]Prompt, 0, len(ids))
	for _, id := range ids {
		out = append(out, builtins[id].prompt)
	}
	rows, err := s.st.Read().Prompts(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, promptFromStore(row))
	}
	return out, nil
}

// checkBody 正規化內文並檢查：不可以是空的、不超過上限、變數都已定義。
// 回傳正規化後的內文、它用到的變數，以及依序排列的錯誤（路徑都是 body）與未定義的變數。
func checkBody(body string) (norm string, used []string, fields []validate.FieldError, undefined []string) {
	norm = normalizeBody(body)
	switch {
	case strings.TrimSpace(norm) == "":
		return norm, nil, []validate.FieldError{{Path: "body", Message: "內文不可以是空的。"}}, nil
	case len(norm) > maxBodyBytes:
		return norm, nil, []validate.FieldError{{Path: "body", Message: fmt.Sprintf("內文不可以超過 %d KiB。", maxBodyBytes>>10)}}, nil
	}
	used, undefined, err := ParseVariables(norm)
	switch {
	case err != nil:
		return norm, nil, []validate.FieldError{{Path: "body", Message: err.Error() + "。"}}, nil
	case len(undefined) > 0:
		return norm, used, []validate.FieldError{{Path: "body", Message: fmt.Sprintf(
			"用到未定義的變數：%s；可用的變數：%s。", strings.Join(undefined, "、"), strings.Join(Variables, "、"))}}, undefined
	}
	return norm, used, nil, nil
}

func invalid(fields []validate.FieldError, undefined []string) *InvalidError {
	return &InvalidError{Msg: fields[0].Path + "：" + fields[0].Message, Fields: fields, Undefined: undefined}
}

// Create 建立使用者的 prompt（版本 1，也是預設版本）。檢查順序：id → kind → body → 未定義的變數（PRM-R1 的 errors[0].path 依賴它）。
// id 與內建的相同回傳 ErrBuiltin，已存在回傳 ErrExists。
func (s *Service) Create(ctx context.Context, id, kind, body string) (Prompt, Version, error) {
	var fields []validate.FieldError
	if !idRE.MatchString(id) {
		fields = append(fields, validate.FieldError{Path: "id", Message: "id 要以小寫英文字母或數字開頭，只能用小寫英文字母、數字、「.」與「-」，最長 64 個字元。"})
	}
	if !slices.Contains(Kinds, kind) {
		fields = append(fields, validate.FieldError{Path: "kind", Message: fmt.Sprintf("不認識的類型「%s」；可用的類型：%s。", kind, strings.Join(Kinds, "、"))})
	}
	norm, used, bodyFields, undefined := checkBody(body)
	fields = append(fields, bodyFields...)
	if len(fields) > 0 {
		return Prompt{}, Version{}, invalid(fields, undefined)
	}
	if isBuiltin(id) {
		return Prompt{}, Version{}, ErrBuiltin
	}
	varsJSON, _ := json.Marshal(used)
	p, v, err := s.st.Write().CreatePrompt(ctx, id, kind, norm, BodySHA256(norm), string(varsJSON))
	if errors.Is(err, store.ErrPromptExists) {
		return Prompt{}, Version{}, ErrExists
	}
	if err != nil {
		return Prompt{}, Version{}, err
	}
	return promptFromStore(p), versionFromStore(v), nil
}

// AddVersion 新增一個版本。不改變 default_version（PRM-R5）；內建 prompt 回傳 ErrBuiltin。
func (s *Service) AddVersion(ctx context.Context, id, body string) (Version, error) {
	if isBuiltin(id) {
		return Version{}, ErrBuiltin
	}
	if _, err := s.st.Read().Prompt(ctx, id); err != nil {
		return Version{}, mapNotFound(err)
	}
	norm, used, fields, undefined := checkBody(body)
	if len(fields) > 0 {
		return Version{}, invalid(fields, undefined)
	}
	varsJSON, _ := json.Marshal(used)
	v, err := s.st.Write().AddPromptVersion(ctx, id, norm, BodySHA256(norm), string(varsJSON))
	if err != nil {
		return Version{}, mapNotFound(err)
	}
	return versionFromStore(v), nil
}

// mapNotFound 把 store 的 ErrNotFound 換成 prompt 庫的 ErrNotFound，其他錯誤原樣回傳。
func mapNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// Versions 列出 prompt 的所有版本，依版本遞增；內建的也可以。
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	if b, ok := builtins[id]; ok {
		return slices.Clone(b.versions), nil
	}
	if _, err := s.st.Read().Prompt(ctx, id); err != nil {
		return nil, mapNotFound(err)
	}
	rows, err := s.st.Read().PromptVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, row := range rows {
		out = append(out, versionFromStore(row))
	}
	return out, nil
}

// SetDefault 把 default_version 設成 version。版本不存在是 *InvalidError（路徑 version）；內建 prompt 回傳 ErrBuiltin。
func (s *Service) SetDefault(ctx context.Context, id string, version int) (Prompt, error) {
	if isBuiltin(id) {
		return Prompt{}, ErrBuiltin
	}
	p, err := s.st.Read().Prompt(ctx, id)
	if err != nil {
		return Prompt{}, mapNotFound(err)
	}
	if version < 1 || version > p.LatestVersion {
		return Prompt{}, invalid([]validate.FieldError{{
			Path: "version", Message: fmt.Sprintf("prompt「%s」沒有第 %d 版（目前有 1～%d 版）。", id, version, p.LatestVersion),
		}}, nil)
	}
	if err := s.st.Write().SetPromptDefault(ctx, id, version); err != nil {
		return Prompt{}, mapNotFound(err)
	}
	p, err = s.st.Read().Prompt(ctx, id)
	if err != nil {
		return Prompt{}, mapNotFound(err)
	}
	return promptFromStore(p), nil
}

// Delete 硬刪除 prompt 與它的所有版本（D13）。內建的回傳 ErrBuiltin；還被引用時回傳 *InUseError。
// 讀取使用者清單與刪除在同一個寫入交易內（P7）：唯一的寫入連線被它佔住，其他寫入（新增引用）要等它結束，
// 所以不會在檢查之後才冒出新的引用而留下懸空的 prompt 引用。
func (s *Service) Delete(ctx context.Context, id string) error {
	if isBuiltin(id) {
		return ErrBuiltin
	}
	return s.st.Tx(ctx, func(r *store.Repo) error {
		if _, err := r.Prompt(ctx, id); err != nil {
			return mapNotFound(err)
		}
		if s.usedBy != nil {
			users, err := s.usedBy(ctx, r, id)
			if err != nil {
				return err
			}
			if len(users) > 0 {
				return &InUseError{UsedBy: users}
			}
		}
		if ok, err := r.DeletePrompt(ctx, id); err != nil {
			return err
		} else if !ok {
			return ErrNotFound
		}
		return nil
	})
}

// Resolve 解析引用（"<id>"、"<id>@default"、"<id>@<N>"）到實際的版本與內文。
// 錯誤：*InvalidError（格式）、ErrNotFound、ErrVersionNotFound。
func (s *Service) Resolve(ctx context.Context, ref string) (Resolved, error) {
	r, err := ParseRef(ref)
	if err != nil {
		return Resolved{}, err
	}
	if b, ok := builtins[r.ID]; ok {
		n := r.Version
		if n == 0 {
			n = b.prompt.DefaultVersion
		}
		if n > len(b.versions) {
			return Resolved{}, ErrVersionNotFound
		}
		return resolved(b.prompt.Kind, b.versions[n-1]), nil
	}
	p, err := s.st.Read().Prompt(ctx, r.ID)
	if err != nil {
		return Resolved{}, mapNotFound(err)
	}
	n := r.Version
	if n == 0 {
		n = p.DefaultVersion
	}
	row, err := s.st.Read().PromptVersion(ctx, r.ID, n)
	if errors.Is(err, store.ErrNotFound) {
		return Resolved{}, ErrVersionNotFound
	}
	if err != nil {
		return Resolved{}, err
	}
	return resolved(p.Kind, versionFromStore(row)), nil
}

func resolved(kind string, v Version) Resolved {
	return Resolved{ID: v.PromptID, Kind: kind, Version: v.Version, SHA256: v.SHA256, Body: v.Body}
}
