package hubauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/validate"
	"github.com/cxyfer/notesteme/internal/websession"
)

// 群組、自動規則、設定檔的 admins 與 Hub token（M5-S2，HACC-R9～R12）。
//
// 有效權限每個請求重新計算：帳號的群組（members 加資料庫中仍存在於設定檔的群組）的權限聯集，
// PAT 再與它的權限取交集（ADR-0031 第 2 點）。帳號失去的權限，PAT 立即跟著失去。

// Hub token 的格式（HACC-R9）：nsh_ 加 40 個 base62 字元。
const (
	PATPrefix  = "nsh_"
	patBodyLen = 40
	base62     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Hub token 的期限（HACC-R9）。
const (
	PATDefaultDays = 90
	PATMaxDays     = 365
	// PATMaxName 是 token 名稱的字元上限。
	PATMaxName = 100
)

// 錯誤代碼。
const (
	CodeForbidden         = "forbidden"
	CodePermissionNotHeld = "permission_not_held"
	CodeAdminByConfig     = "admin_by_config"
	CodeSessionRequired   = "session_required"
)

// ErrForbidden 是缺少路由需要的權限（HACC-R13），回應帶 required_permission。
func ErrForbidden(perm string) *apierr.Error {
	e := apierr.New(http.StatusForbidden, CodeForbidden, fmt.Sprintf("你沒有這個操作需要的權限（%s）。", perm))
	e.RequiredPermission = perm
	return e
}

// ErrSessionRequired 是用 Hub token 呼叫只接受瀏覽器 session 的路由（建立 token、連結登入方式等，HACC 待決問題 A12）。
func ErrSessionRequired() *apierr.Error {
	return apierr.New(http.StatusForbidden, CodeSessionRequired, "這個操作只能在 Hub 網頁登入後進行，不能使用 Hub token。")
}

// ---- 群組 ----

// groupOrder 是 /me 列出群組的順序：內建的四個在前，自訂群組依名稱。
func (s *Service) groupOrder(names map[string]bool) []string {
	out := []string{}
	for _, g := range []string{hubconfig.GroupMembers, hubconfig.GroupContributors, hubconfig.GroupModerators, hubconfig.GroupAdmins} {
		if names[g] {
			out = append(out, g)
			delete(names, g)
		}
	}
	rest := make([]string, 0, len(names))
	for g := range names {
		rest = append(rest, g)
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// Groups 回傳帳號所屬的群組：每個帳號都在 members，加上資料庫中的成員資格（任何來源）；
// 設定檔已經沒有的群組不列出、不計入權限。
func (s *Service) Groups(ctx context.Context, userID string) ([]string, error) {
	ms, err := s.store.GroupMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{hubconfig.GroupMembers: true}
	for _, m := range ms {
		if _, ok := s.cfg.Groups[m.Group]; ok {
			names[m.Group] = true
		}
	}
	return s.groupOrder(names), nil
}

// expand 依 hubconfig.Permissions 的順序回傳權限；有 admin 時是全部權限。
func expand(held map[string]bool) []string {
	if held[hubconfig.PermAdmin] {
		return slices.Clone(hubconfig.Permissions)
	}
	out := []string{}
	for _, p := range hubconfig.Permissions {
		if held[p] {
			out = append(out, p)
		}
	}
	return out
}

// Permissions 回傳群組權限的聯集（HACC-R10）；admin 包含全部權限。
func (s *Service) Permissions(groups []string) []string {
	held := map[string]bool{}
	for _, g := range groups {
		for _, p := range s.cfg.Groups[g].Permissions {
			held[p] = true
		}
	}
	return expand(held)
}

// AccountPermissions 回傳帳號當下的有效權限。
func (s *Service) AccountPermissions(ctx context.Context, userID string) ([]string, error) {
	groups, err := s.Groups(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.Permissions(groups), nil
}

// EffectivePermissions 回傳這個請求的有效權限：session 是帳號的權限；PAT 是 PAT 的權限 ∩ 帳號當下的權限（HACC-R9）。
func (s *Service) EffectivePermissions(ctx context.Context, p *Principal) ([]string, error) {
	acct, err := s.AccountPermissions(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	if p.PAT == nil {
		return acct, nil
	}
	held := map[string]bool{}
	for _, x := range p.PAT.Permissions {
		held[x] = true
	}
	tok := map[string]bool{}
	for _, x := range expand(held) {
		tok[x] = true
	}
	out := []string{}
	for _, x := range acct {
		if tok[x] {
			out = append(out, x)
		}
	}
	return out, nil
}

// HasPermission 回報這個請求是否有 perm。
func (s *Service) HasPermission(ctx context.Context, p *Principal, perm string) (bool, error) {
	eff, err := s.EffectivePermissions(ctx, p)
	if err != nil {
		return false, err
	}
	return slices.Contains(eff, perm), nil
}

// ---- 自動規則與設定檔的 admins（每次登入時重新評估，HACC-R11、R12）----

// RuleHolds 回報一條規則對這個身分是否成立。規則的供應商與身分不同時不成立。
func RuleHolds(r hubconfig.Rule, id hubstore.ExternalIdentity) bool {
	if string(r.Provider) != id.Provider {
		return false
	}
	switch {
	case r.TrustLevelGTE != nil:
		return id.Attrs.TrustLevel != nil && *id.Attrs.TrustLevel >= *r.TrustLevelGTE
	case len(r.OrgMember) > 0:
		// 全是數字的項目是組織的 id（建議：組織改名後舊名稱可能被別人註冊，安全修補 A）；其他是名稱，不分大小寫。
		for _, want := range r.OrgMember {
			if isDigits(want) {
				if slices.Contains(id.Attrs.OrgIDs, want) {
					return true
				}
				continue
			}
			if slices.ContainsFunc(id.Attrs.Orgs, func(o string) bool { return strings.EqualFold(o, want) }) {
				return true
			}
		}
		return false
	case len(r.EmailDomain) > 0:
		if !id.EmailVerified {
			return false
		}
		at := strings.LastIndexByte(id.Email, '@')
		if at <= 0 || at == len(id.Email)-1 {
			return false
		}
		domain := id.Email[at+1:]
		// 還要是那個網域的 Google Workspace 帳號（hd）：任何人都能用公司信箱註冊個人 Google 帳號，
		// 離職後也還能用它驗證 email（安全修補 A）。
		if !strings.EqualFold(id.Attrs.HostedDomain, domain) {
			return false
		}
		return slices.ContainsFunc(r.EmailDomain, func(d string) bool { return strings.EqualFold(d, domain) })
	}
	return false
}

// ruleGroups 回傳這個身分以自動規則應該在的群組（依名稱排序）。
func (s *Service) ruleGroups(id hubstore.ExternalIdentity) []string {
	var out []string
	for name, g := range s.cfg.Groups {
		if slices.ContainsFunc(g.Rules, func(r hubconfig.Rule) bool { return RuleHolds(r, id) }) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// isConfigAdmin 回報 (provider, subject) 是否列在設定檔的 admins。
func (s *Service) isConfigAdmin(provider, subject string) bool {
	return slices.ContainsFunc(s.cfg.Admins, func(a hubconfig.Identity) bool {
		return string(a.Provider) == provider && a.Subject == subject
	})
}

// applySignIn 在登入（或連結）成功後、建立 session 之前，重新評估這個供應商的自動規則（只動 source: rule 的成員資格，
// HACC-R11），並在身分列在設定檔的 admins 時以 source: config 加入 admins（HACC-R12）。
func (s *Service) applySignIn(ctx context.Context, userID string, id hubstore.ExternalIdentity) error {
	if err := s.store.SyncRuleGroups(ctx, userID, id.Provider, s.ruleGroups(id)); err != nil {
		return err
	}
	if s.isConfigAdmin(id.Provider, id.Subject) {
		return s.store.SetConfigGroup(ctx, userID, hubconfig.GroupAdmins, true)
	}
	return nil
}

// SyncConfigAdmins 讓 source: config 的 admins 成員與設定檔一致（Hub 啟動時呼叫）：設定檔移除的管理員在重新啟動後
// 立即失去權限；列出但還沒有登入過的身分在第一次登入時加入。
func (s *Service) SyncConfigAdmins(ctx context.Context) error {
	var ids []string
	for _, a := range s.cfg.Admins {
		ident, err := s.store.IdentityBySubject(ctx, string(a.Provider), a.Subject)
		switch {
		case errors.Is(err, hubstore.ErrNotFound):
			continue
		case err != nil:
			return err
		}
		if !slices.Contains(ids, ident.UserID) {
			ids = append(ids, ident.UserID)
		}
	}
	return s.store.ReplaceConfigGroup(ctx, hubconfig.GroupAdmins, ids)
}

// ---- 管理員指派群組（HACC-R12）----

// SetGroup 以 source: manual 把帳號加入或移出群組；admins 不能用 API 指派。
func (s *Service) SetGroup(ctx context.Context, actor *Principal, userID, group string, member bool) error {
	g, ok := s.cfg.Groups[group]
	// 依群組的權限判斷（不只看名稱）：帶 admin 的群組一律不能由 API 指派（ADR-0031 第 2 點）。
	if group == hubconfig.GroupAdmins || (ok && slices.Contains(g.Permissions, hubconfig.PermAdmin)) {
		return apierr.New(http.StatusForbidden, CodeAdminByConfig, "管理員只能在設定檔或用 notesteme-hub admin grant 指定。")
	}
	if group == hubconfig.GroupMembers {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, "每個帳號都在 members 群組，不能指派或移除。")
	}
	if !ok {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "找不到這個群組。")
	}
	_, err := s.store.SetManualGroup(ctx, actor.User.ID, userID, group, member)
	if errors.Is(err, hubstore.ErrNotFound) {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "找不到這個使用者。")
	}
	return err
}

// ---- Hub token（HACC-R9）----

// NewToken 是建立 token 的輸入。
type NewToken struct {
	Name          string
	Permissions   []string
	ExpiresInDays *int
}

// CreatedToken 是建立成功的 token；Token 是明文，只回傳這一次。
type CreatedToken struct {
	PAT   hubstore.PAT
	Token string
}

func invalid(fields []validate.FieldError) *apierr.Error {
	e := apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, fields[0].Path+"："+fields[0].Message)
	e.Errors = fields
	return e
}

// CreateToken 建立 Hub token。權限要在帳號當下的有效權限中（PAT 不能提升權限）。
func (s *Service) CreateToken(ctx context.Context, p *Principal, in NewToken) (CreatedToken, error) {
	var fields []validate.FieldError
	name := cleanText(in.Name, PATMaxName+1)
	switch {
	case name == "":
		fields = append(fields, validate.FieldError{Path: "name", Message: "請填寫 token 的名稱。"})
	case len([]rune(name)) > PATMaxName:
		fields = append(fields, validate.FieldError{Path: "name", Message: fmt.Sprintf("名稱最多 %d 個字元。", PATMaxName)})
	}
	if len(in.Permissions) == 0 {
		fields = append(fields, validate.FieldError{Path: "permissions", Message: "至少要選一個權限。"})
	}
	for i, x := range in.Permissions {
		if !slices.Contains(hubconfig.Permissions, x) {
			fields = append(fields, validate.FieldError{Path: fmt.Sprintf("permissions.%d", i),
				Message: fmt.Sprintf("不認得的權限 %q（%s）。", x, strings.Join(hubconfig.Permissions, "、"))})
		}
	}
	days := PATDefaultDays
	if in.ExpiresInDays != nil {
		days = *in.ExpiresInDays
		if days < 1 || days > PATMaxDays {
			fields = append(fields, validate.FieldError{Path: "expires_in_days", Message: fmt.Sprintf("必須是 1～%d 的整數。", PATMaxDays)})
		}
	}
	if len(fields) > 0 {
		return CreatedToken{}, invalid(fields)
	}
	// 權限上限是帳號當下的有效權限（session 才能建立 token，所以就是帳號的權限）。
	acct, err := s.AccountPermissions(ctx, p.User.ID)
	if err != nil {
		return CreatedToken{}, err
	}
	var notHeld []validate.FieldError
	var perms []string
	for i, x := range in.Permissions {
		if !slices.Contains(acct, x) {
			notHeld = append(notHeld, validate.FieldError{Path: fmt.Sprintf("permissions.%d", i), Message: fmt.Sprintf("你的帳號沒有 %s 權限。", x)})
			continue
		}
		if !slices.Contains(perms, x) {
			perms = append(perms, x)
		}
	}
	if len(notHeld) > 0 {
		e := apierr.New(http.StatusUnprocessableEntity, CodePermissionNotHeld, "token 不能有帳號沒有的權限："+notHeld[0].Path+"。")
		e.Errors = notHeld
		return CreatedToken{}, e
	}
	// 依 hubconfig.Permissions 的順序存放。
	slices.SortFunc(perms, func(a, b string) int {
		return slices.Index(hubconfig.Permissions, a) - slices.Index(hubconfig.Permissions, b)
	})
	token, err := newPATToken()
	if err != nil {
		return CreatedToken{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return CreatedToken{}, err
	}
	now := s.clock.Now()
	pat := hubstore.PAT{
		ID: id.String(), UserID: p.User.ID, Name: name, TokenHash: websession.HashToken(token), Permissions: perms,
		CreatedAt: now, ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour),
	}
	if err := s.store.CreatePAT(ctx, pat); err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{PAT: pat, Token: token}, nil
}

// newPATToken 產生 nsh_ 加 40 個 base62 字元（每個字元獨立均勻取樣，約 238 bits）。
func newPATToken() (string, error) {
	var b strings.Builder
	b.WriteString(PATPrefix)
	n := big.NewInt(int64(len(base62)))
	for range patBodyLen {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(base62[i.Int64()])
	}
	return b.String(), nil
}

// ValidPATFormat 回報字串是否符合 Hub token 的格式（不查資料庫）。
func ValidPATFormat(t string) bool {
	body, ok := strings.CutPrefix(t, PATPrefix)
	if !ok || len(body) != patBodyLen {
		return false
	}
	for i := range len(body) {
		if !strings.ContainsRune(base62, rune(body[i])) {
			return false
		}
	}
	return true
}

// AuthenticateToken 驗證 Hub token：格式不符、不存在、撤銷或過期時回 errNoSession（401）；帳號停用時回 ErrSuspended。
func (s *Service) AuthenticateToken(ctx context.Context, token string) (*Principal, error) {
	if !ValidPATFormat(token) {
		return nil, errNoSession
	}
	pat, err := s.store.PATByHash(ctx, websession.HashToken(token))
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	if !pat.ValidAt(s.clock.Now()) {
		return nil, errNoSession
	}
	user, err := s.store.User(ctx, pat.UserID)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, errNoSession
	}
	if err != nil {
		return nil, err
	}
	if user.Suspended() {
		return nil, ErrSuspended()
	}
	return &Principal{User: user, PAT: &pat}, nil
}

// Tokens 回傳帳號未撤銷的 token。
func (s *Service) Tokens(ctx context.Context, p *Principal) ([]hubstore.PAT, error) {
	return s.store.PATs(ctx, p.User.ID)
}

// RevokeToken 撤銷帳號自己的 token；別人的或不存在的回 404。
func (s *Service) RevokeToken(ctx context.Context, p *Principal, id string) error {
	err := s.store.RevokePAT(ctx, p.User.ID, id)
	if errors.Is(err, hubstore.ErrNotFound) {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "找不到這個 token。")
	}
	return err
}

// isDigits 回報 s 是否非空且全是 ASCII 數字（GitHub 組織的 id）。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
