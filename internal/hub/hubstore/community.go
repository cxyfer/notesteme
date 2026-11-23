package hubstore

import (
	"context"
	"errors"
	"time"
)

// 社群與管理（M5-S5）：投票（HREP-R13）、檢舉與自動隱藏（HREP-R14、R15）、下架申請（HACC-R15、R16）、
// 停用帳號（HACC-R17）與命令列的管理員指派（notesteme-hub admin）。

// 錯誤。
var (
	// ErrAlreadyReported 表示同一人對同一版本已有未處理的檢舉（HREP-R14）。
	ErrAlreadyReported = errors.New("already reported")
	// ErrDecided 表示檢舉或下架申請已經處理過（HACC-R16、HREP-R15）。
	ErrDecided = errors.New("already decided")
	// ErrVersionState 表示版本目前的狀態不允許這個動作（例如隱藏已移除的版本）。
	ErrVersionState = errors.New("version state does not allow this")
)

// CLIActor 是命令列（notesteme-hub admin）動作在稽核紀錄中的執行者名稱。
const CLIActor = "notesteme-hub admin"

// CLIActorID 是傳給寫入方法的執行者 ID，代表命令列（稽核紀錄的 actor_user_id 存 NULL）；帳號 ID 是 UUID，不會相同。
const CLIActorID = "cli"

// SourceCLI 是 notesteme-hub admin grant 的成員資格來源（啟動時依設定檔的同步不會清掉它）。
const SourceCLI = "cli"

// 稽核紀錄的動作（HACC-R17，M5-S5 加入的部分）。
const (
	AuditUserSuspend   = "user.suspend"
	AuditUserUnsuspend = "user.unsuspend"
	AuditTakedown      = "takedown.decide"
	AuditReport        = "report.decide"
	AuditVersionHide   = "version.hide"
)

// 檢舉的狀態。
const (
	ReportOpen      = "open"
	ReportDismissed = "dismissed"
	ReportActioned  = "actioned"
)

// 下架申請的狀態。
const (
	TakedownOpen     = "open"
	TakedownRemoved  = "removed"
	TakedownRejected = "rejected"
)

// 版本被移除的來源（GET /api/v1/me/shares）。
const (
	RemovalTakedown = "takedown"
	RemovalReport   = "report"
)

// Usage 是一段時間內的次數；Oldest 是這段時間內最早的一筆（Count 為 0 時是零值）。
type Usage struct {
	Count  int
	Oldest time.Time
}

// Report 是一筆檢舉。
type Report struct {
	ID         string
	VersionID  string
	ReporterID string
	Reason     string
	Note       string
	State      string
	CreatedAt  time.Time
	DecidedAt  *time.Time
}

// NewReport 是建立檢舉的輸入；AutoHide 與 MinAccountAge 是自動隱藏的門檻（HREP-R15，AutoHide <= 0 代表不自動隱藏）。
type NewReport struct {
	VersionID, ReporterID, Reason, Note string
	AutoHide                            int
	MinAccountAge                       time.Duration
}

// Takedown 是一筆下架申請；contact email 以信封加密存放（由 hubapi 加解密）。
type Takedown struct {
	ID        string
	VersionID string
	Reason    string
	Statement string
	// DEKNonce、DEKSealed 是以 KEK 加密的資料金鑰；ContactNonce、ContactSealed 是以資料金鑰加密的 email。
	DEKNonce, DEKSealed         []byte
	ContactNonce, ContactSealed []byte
	State                       string
	Note                        string
	CreatedAt                   time.Time
	DecidedAt                   *time.Time
}

// Removal 是移除版本的結果：BlobsToDelete 是不再被任何未移除的版本引用的 blob（交易提交後由呼叫端刪檔）。
type Removal struct {
	BlobsToDelete []string
}

// MyShare 是帳號自己的一次分享與它的版本（GET /api/v1/me/shares）。
type MyShare struct {
	ID          string
	Video       Video
	CreatedAt   time.Time
	WithdrawnAt *time.Time
	Versions    []MyShareVersion
}

// MyShareVersion 是分享中的版本與它被移除的原因（只在 removed 時有值）。
type MyShareVersion struct {
	Version
	RemovalSource string
	RemovalReason string
}

// CommunityStore 是社群與管理的方法（嵌入 Store）。
type CommunityStore interface {
	// SetVote 投票或改票（value 為 1 或 -1；0 代表收回），記一筆投票次數，並在同一個交易中重算版本的分數，回傳新的分數。
	SetVote(ctx context.Context, versionID, userID string, value int) (score int, err error)
	// VoteUsage 回傳帳號在 since 之後的投票次數（含收回）。
	VoteUsage(ctx context.Context, userID string, since time.Time) (Usage, error)

	// CreateReport 建立檢舉；同一人對同一版本已有未處理的檢舉時回 ErrAlreadyReported。
	// 同一個交易中，未處理的檢舉來自至少 AutoHide 個在檢舉當時帳號齡滿 MinAccountAge、沒有被停用的不同帳號時，
	// visible 的版本變成 hidden（hidden 為 true）。
	CreateReport(ctx context.Context, in NewReport) (r Report, hidden bool, err error)
	// ReportUsage 回傳帳號在 since 之後建立的檢舉數。
	ReportUsage(ctx context.Context, userID string, since time.Time) (Usage, error)
	// Report 回傳檢舉；不存在時回 ErrNotFound。
	Report(ctx context.Context, id string) (Report, error)
	// Reports 依建立時間倒序列出檢舉；state 空字串代表不限。
	Reports(ctx context.Context, state string, limit int) ([]Report, error)
	// DismissReport 駁回檢舉：版本所有未處理的檢舉結案（dismissed），hidden 的版本恢復 visible，寫入稽核紀錄（report.decide）。
	// 不存在回 ErrNotFound，已處理回 ErrDecided。
	DismissReport(ctx context.Context, actorUserID, reportID string) (Report, error)
	// RemoveByReport 依檢舉移除版本（同 HACC-R16 的移除，removal_source: report）。不存在回 ErrNotFound，已處理回 ErrDecided。
	RemoveByReport(ctx context.Context, actorUserID, reportID string) (Report, Removal, error)
	// HideVersion 由版主隱藏 visible 的版本並寫入稽核紀錄（version.hide）；已經 hidden 時 changed 為 false、不寫稽核紀錄。
	// 不存在回 ErrNotFound，removed 或 withdrawn 回 ErrVersionState。
	HideVersion(ctx context.Context, actorUserID, versionID string) (changed bool, err error)

	// CreateTakedown 建立下架申請（ID、State、CreatedAt 由 store 填入；ID 也可以由呼叫端預先指定，加密的 AAD 要用它）。
	CreateTakedown(ctx context.Context, t Takedown) (Takedown, error)
	// Takedown 回傳下架申請；不存在時回 ErrNotFound。
	Takedown(ctx context.Context, id string) (Takedown, error)
	// Takedowns 依建立時間倒序列出下架申請；state 空字串代表不限。
	Takedowns(ctx context.Context, state string, limit int) ([]Takedown, error)
	// RejectTakedown 駁回下架申請並寫入稽核紀錄（takedown.decide）。不存在回 ErrNotFound，已決定回 ErrDecided。
	RejectTakedown(ctx context.Context, actorUserID, id, note string) (Takedown, error)
	// RemoveByTakedown 依下架申請移除版本（HACC-R16）：版本變成 removed、(video, sha256) 記入 removed_hashes、
	// 版本未處理的檢舉結案（actioned）、申請變成 removed，寫入稽核紀錄。申請不存在回 ErrNotFound，已決定回 ErrDecided，
	// 版本不存在回 ErrVersionState（什麼都不改）。
	RemoveByTakedown(ctx context.Context, actorUserID, id, note string) (Takedown, Removal, error)

	// SuspendUser 停用帳號並重算這個帳號投過票的版本的分數（HACC-R17、HREP-R13）；actorUserID 非空字串時寫入稽核紀錄
	// （CLIActorID 代表命令列）。
	// 帳號不存在回 ErrNotFound；已經停用時 changed 為 false。session 與 Hub token 保留（middleware 對它們回 403）。
	SuspendUser(ctx context.Context, actorUserID, userID, reason string) (changed bool, err error)
	// UnsuspendUser 恢復帳號：重算分數，並讓停用前的 session 與 Hub token 失效（要重新登入、重新建立 token）。
	// 帳號不存在回 ErrNotFound；沒有停用時 changed 為 false。
	UnsuspendUser(ctx context.Context, actorUserID, userID string) (changed bool, err error)
	// Users 依建立順序列出帳號，最多 limit 個。
	Users(ctx context.Context, limit int) ([]User, error)
	// SetCLIGroup 以 source: cli 加入或移出群組，有改變時寫入稽核紀錄（執行者是命令列）；帳號不存在回 ErrNotFound。
	SetCLIGroup(ctx context.Context, userID, group string, member bool) (changed bool, err error)

	// MyShares 依建立時間倒序回傳帳號的分享（含已撤回的），最多 limit 個。
	MyShares(ctx context.Context, userID string, limit int) ([]MyShare, error)
}
