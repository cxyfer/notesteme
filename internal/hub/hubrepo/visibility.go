package hubrepo

import (
	"net/http"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 單一內容的可見度（HACC-R14）與上傳時指定的可見度（HREP-R8）。

// 可見度。
const (
	VisibilityPublic        = "public"
	VisibilityAuthenticated = "authenticated"
	VisibilityPrivate       = "private"
	groupPrefix             = "group:"
)

// 版本的狀態（ADR-0031 第 3 點）。
const (
	StateVisible   = "visible"
	StateHidden    = "hidden"
	StateRemoved   = "removed"
	StateWithdrawn = "withdrawn"
)

// GroupOf 回傳 group:<name> 的群組名稱；不是群組可見度時 ok 為 false。
func GroupOf(v string) (string, bool) {
	g, ok := strings.CutPrefix(v, groupPrefix)
	return g, ok && g != ""
}

// rank 是可見度的寬窄：private < group:<name> < authenticated < public。
func rank(v string) int {
	switch v {
	case VisibilityPublic:
		return 3
	case VisibilityAuthenticated:
		return 2
	case VisibilityPrivate:
		return 0
	}
	if _, ok := GroupOf(v); ok {
		return 1
	}
	return -1
}

// Effective 回傳有效可見度：不超過站台上限（site.visibility: authenticated 時 public 視為 authenticated）。
func Effective(v, site string) string {
	if v == VisibilityPublic && site != VisibilityPublic {
		return VisibilityAuthenticated
	}
	return v
}

func fieldError(path, msg string) validate.FieldError {
	return validate.FieldError{Path: path, Message: msg}
}

func invalidField(path, msg string) *apierr.Error {
	e := apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, path+"："+msg)
	e.Errors = []validate.FieldError{fieldError(path, msg)}
	return e
}

// CheckRequested 驗證上傳時指定的可見度（HREP-R8）：只能是 public、authenticated、private 或設定檔中的群組，
// 而且不能超過站台上限。groupExists 回報群組是否存在。
func CheckRequested(v, site string, groupExists func(string) bool) *apierr.Error {
	if g, ok := GroupOf(v); ok {
		if !groupExists(g) {
			return invalidField("visibility", "不認得的群組："+g+"。")
		}
		return nil
	}
	if rank(v) < 0 {
		return invalidField("visibility", "必須是 public、authenticated、private 或 group:<群組名稱>。")
	}
	if rank(v) > rank(site) {
		return invalidField("visibility", "可見度不能超過站台上限（"+site+"）。")
	}
	return nil
}

// Viewer 是查看內容的人；Anonymous 時其他欄位都是零值。
type Viewer struct {
	Anonymous bool
	UserID    string
	Groups    []string
	// Moderator 表示有效權限中有 moderate（admin 包含它）。
	Moderator bool
}

// Subject 是被查看的版本。
type Subject struct {
	Visibility, State string
	// Uploaders 是有未撤回分享的上傳者的帳號 ID。
	Uploaders []string
}

// CanSee 回報 viewer 看不看得到這個版本（HACC-R14、HREP-R10、R15）：
// withdrawn 一律看不到；hidden 只有上傳者與 moderate；其他狀態依有效可見度——public 任何人、authenticated 任何登入的人、
// group:<name> 群組成員、private 只有上傳者，上傳者與 moderate 一律看得到。removed 依可見度判斷（之後由呼叫端回 410）。
func CanSee(s Subject, viewer Viewer, site string) bool {
	if s.State == StateWithdrawn {
		return false
	}
	owner := !viewer.Anonymous && viewer.UserID != "" && slices.Contains(s.Uploaders, viewer.UserID)
	privileged := owner || (!viewer.Anonymous && viewer.Moderator)
	if s.State == StateHidden {
		return privileged
	}
	if privileged {
		return true
	}
	v := Effective(s.Visibility, site)
	switch {
	case v == VisibilityPublic:
		return true
	case viewer.Anonymous:
		return false
	case v == VisibilityAuthenticated:
		return true
	}
	if g, ok := GroupOf(v); ok {
		return slices.Contains(viewer.Groups, g)
	}
	return false
}
