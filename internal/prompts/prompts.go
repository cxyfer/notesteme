// Package prompts 是 prompt 庫（PRM）：使用者的 prompt 與它的版本存在資料庫，內建 prompt 隨程式發布。
//
// 這個檔案是 M2 S1/S2 的契約（Wave 0）：型別、錯誤與 Service 的簽名已經凍結，方法先回傳 ErrNotImplemented，
// 由 WP-3 實作。
package prompts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// Kinds 是 prompt 的類型（PRM-R1）。
var Kinds = []string{"summary", "chapters", "segment", "correct", "translate", "frames.select", "note"}

// Variables 是 prompt 內文可以使用的 {{變數}}（PRM-R3）。
var Variables = []string{"title", "channel", "description", "chapters", "glossary", "source_lang", "target_lang"}

// BuiltinIDs 是內建的 prompt（PRM-R4）。內容放在程式內，不寫入資料庫；使用者的 prompt id 不可以和它們相同（D12）。
var BuiltinIDs = []string{"segment-default", "correct-default", "translate-default", "localize-zh-hant-tw", "localize-zh-hans-cn"}

// 錯誤。API 層（WP-3）把它們對應成 404、409、422。
var (
	// ErrNotImplemented 表示功能還沒實作（API 層回 501）。
	ErrNotImplemented = errors.New("prompts: 尚未實作")
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

// Deps 是 Service 的依賴。
type Deps struct {
	Store *store.Store
	Clock clock.Clock
	// UsedBy 回傳引用這個 prompt 的對象（刪除前檢查，D13），app 接到 Pipeline.PromptUsers。
	// 順序：引用它的處理方案名稱（依字母排序）、"settings"、"channel:<platform>:<external_id>"。nil 代表沒有人引用。
	UsedBy func(ctx context.Context, id string) ([]string, error)
}

// Service 是 prompt 庫。
type Service struct {
	st     *store.Store
	clock  clock.Clock
	usedBy func(ctx context.Context, id string) ([]string, error)
}

// New 建立 Service。
func New(d Deps) *Service {
	return &Service{st: d.Store, clock: d.Clock, usedBy: d.UsedBy}
}

// List 列出所有 prompt：內建的在前，依 id 排序，接著是使用者的，依 id 排序。
func (s *Service) List(ctx context.Context) ([]Prompt, error) {
	return nil, ErrNotImplemented
}

// Create 建立使用者的 prompt（版本 1，也是預設版本）。檢查順序：id → kind → body → 未定義的變數（PRM-R1 的 errors[0].path 依賴它）。
// id 與內建的相同回傳 ErrBuiltin，已存在回傳 ErrExists。
func (s *Service) Create(ctx context.Context, id, kind, body string) (Prompt, Version, error) {
	return Prompt{}, Version{}, ErrNotImplemented
}

// AddVersion 新增一個版本。不改變 default_version（PRM-R5）；內建 prompt 回傳 ErrBuiltin。
func (s *Service) AddVersion(ctx context.Context, id, body string) (Version, error) {
	return Version{}, ErrNotImplemented
}

// Versions 列出 prompt 的所有版本，依版本遞增；內建的也可以。
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	return nil, ErrNotImplemented
}

// SetDefault 把 default_version 設成 version。版本不存在是 *InvalidError（路徑 version）；內建 prompt 回傳 ErrBuiltin。
func (s *Service) SetDefault(ctx context.Context, id string, version int) (Prompt, error) {
	return Prompt{}, ErrNotImplemented
}

// Delete 硬刪除 prompt 與它的所有版本（D13）。內建的回傳 ErrBuiltin；還被引用時回傳 *InUseError。
func (s *Service) Delete(ctx context.Context, id string) error {
	return ErrNotImplemented
}

// Resolve 解析引用（"<id>"、"<id>@default"、"<id>@<N>"）到實際的版本與內文。
// 錯誤：*InvalidError（格式）、ErrNotFound、ErrVersionNotFound。
func (s *Service) Resolve(ctx context.Context, ref string) (Resolved, error) {
	return Resolved{}, ErrNotImplemented
}
