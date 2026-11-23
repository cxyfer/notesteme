// Package hubstore 是 Hub 的儲存介面（ADR-0030 第 2 點）：SQLite 是參考 backend（驗收測試、開發、小型自架），
// Postgres 是正式環境。兩種實作共用這個介面與 storetest 的單元測試；domain 型別放在這裡。
//
// 實作在子套件：sqlite（goose migration 與 sqlc，從 00001 起獨立編號，不和 Client 的 internal/store 共用）、
// postgres（M5-S0 只有佔位，CI 加 service container 之後由後續切片實作）。
// 資料表的方向見 ADR-0031 第 5 點；M5-S0 的 00001 只建 users 與 identities，其餘由各切片加 migration。
package hubstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound 表示查不到資料。
var ErrNotFound = errors.New("not found")

// 登入與帳號連結的錯誤（HACC-R6、R7）。
var (
	// ErrIdentityInUse 表示這個身分已經屬於另一個帳號。
	ErrIdentityInUse = errors.New("identity in use")
	// ErrProviderLinked 表示帳號已經連結了同一個供應商的另一個身分。
	ErrProviderLinked = errors.New("provider already linked")
	// ErrLastIdentity 表示要移除的是帳號唯一的登入方式。
	ErrLastIdentity = errors.New("last identity")
)

// ErrNotImplemented 表示這個 backend 還沒有實作（M5-S0 的 postgres）。
var ErrNotImplemented = errors.New("尚未實作")

// Store 是 Hub 的儲存層；方法由各切片依需要加入，兩種 backend 都要實作。
type Store interface {
	// CheckWritable 確認可以取得寫入鎖（/readyz），不寫入任何資料。
	CheckWritable(ctx context.Context) error
	// CountUsers 回傳帳號總數（doctor 與驗收的 `the number of Hub accounts is`）。
	CountUsers(ctx context.Context) (int64, error)
	// SchemaVersion 回傳已套用的 migration 版本（doctor 與 migrate 顯示）。
	SchemaVersion(ctx context.Context) (int64, error)
	Close() error

	// SignIn 以供應商的身分登入（HACC-R4、R5）：身分已經存在時更新它的 login、email 與屬性，回傳所屬帳號；
	// 不存在時在同一個 transaction 建立帳號（display_name）與身分，created 為 true。不依 email 合併帳號。
	SignIn(ctx context.Context, in ExternalIdentity, displayName string) (u User, created bool, err error)
	// LinkIdentity 把身分連結到既有帳號並寫入稽核紀錄（identity.link，HACC-R6）。
	// 身分屬於另一個帳號時回 ErrIdentityInUse，帳號已有同一個供應商的身分時回 ErrProviderLinked；兩者都不改變任何資料。
	// 身分已經屬於這個帳號時視為成功（重新整理屬性）。
	LinkIdentity(ctx context.Context, userID string, in ExternalIdentity) (Identity, error)
	// UnlinkIdentity 移除帳號在這個供應商的身分並寫入稽核紀錄（identity.unlink，HACC-R7）。
	// 沒有這個身分時回 ErrNotFound，是唯一的身分時回 ErrLastIdentity。
	UnlinkIdentity(ctx context.Context, userID, provider string) error
	// User 回傳帳號；不存在時回 ErrNotFound。
	User(ctx context.Context, id string) (User, error)
	// Identities 依連結的先後回傳帳號的身分。
	Identities(ctx context.Context, userID string) ([]Identity, error)

	// CreateOAuthState 記錄等待中的登入（HACC-R2），同時清掉已過期的紀錄。
	CreateOAuthState(ctx context.Context, st OAuthState) error
	// ConsumeOAuthState 以原子操作取用一筆未使用、未過期、同一個供應商的 state（HACC-R3）；
	// 不符合時回 ErrNotFound。同一個 state 只有第一次會成功。
	ConsumeOAuthState(ctx context.Context, stateHash, provider string) (OAuthState, error)

	// CreateSession 建立 session（只存 token 的 hash，HACC-R8）。
	CreateSession(ctx context.Context, s Session) error
	// SessionByHash 回傳未過期的 session；不存在或過期時回 ErrNotFound。
	SessionByHash(ctx context.Context, tokenHash string) (Session, error)
	// TouchSession 延長 session 的閒置期限。
	TouchSession(ctx context.Context, id string, expiresAt time.Time) error
	// DeleteSession 讓 session 立即失效；不存在時不算錯誤。
	DeleteSession(ctx context.Context, tokenHash string) error

	// AuditEntries 依時間倒序回傳最多 limit 筆稽核紀錄（HACC-R17）。
	AuditEntries(ctx context.Context, limit int) ([]AuditEntry, error)
}

// User 是 Hub 帳號。
type User struct {
	ID          string
	DisplayName string
	CreatedAt   time.Time
	// SuspendedAt 非 nil 代表帳號已停用（HACC-R17）。
	SuspendedAt *time.Time
}

// Suspended 回報帳號是否已停用。
func (u User) Suspended() bool { return u.SuspendedAt != nil }

// IdentityAttrs 是套用自動規則用的供應商屬性（HACC-R11）。
type IdentityAttrs struct {
	TrustLevel *int     `json:"trust_level,omitempty"` // linuxdo
	Orgs       []string `json:"orgs,omitempty"`        // github
}

// ExternalIdentity 是供應商回報的身分：以 (Provider, Subject) 識別，其他欄位只用來顯示與套用規則。
type ExternalIdentity struct {
	Provider      string
	Subject       string
	Login         string
	Email         string
	EmailVerified bool
	Attrs         IdentityAttrs
}

// Identity 是帳號的一個登入方式。
type Identity struct {
	ExternalIdentity
	ID          string
	UserID      string
	LinkedAt    time.Time
	LastLoginAt time.Time
}

// 登入的意圖（HACC-R2、R6）。
const (
	IntentLogin = "login"
	IntentLink  = "link"
)

// OAuthState 是一筆等待中的登入。
type OAuthState struct {
	StateHash string
	Provider  string
	Intent    string
	// UserID 在 Intent 為 IntentLink 時是發起連結的帳號。
	UserID string
	// VerifierNonce、VerifierSealed 是以 session 金鑰加密的 PKCE verifier。
	VerifierNonce, VerifierSealed []byte
	// Nonce 是 Google 的 OIDC nonce；其他供應商為空字串。
	Nonce     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Session 是瀏覽器的 session。
type Session struct {
	ID        string
	UserID    string
	TokenHash string
	CreatedAt time.Time
	// AuthenticatedAt 是這個 session 登入的時間（HACC-R6 的 10 分鐘）。
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
}

// AuditEntry 是一筆稽核紀錄。
type AuditEntry struct {
	ID          string
	ActorUserID string
	// Actor 是執行者當下的 display_name。
	Actor      string
	Action     string
	TargetKind string
	TargetID   string
	At         time.Time
}

// 稽核紀錄的動作（HACC-R17）。
const (
	AuditIdentityLink   = "identity.link"
	AuditIdentityUnlink = "identity.unlink"
)
