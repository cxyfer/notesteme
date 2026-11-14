package store

import (
	"encoding/json"
	"time"
)

// 步驟狀態（plan 第 5.3 節）。
const (
	StatusPending       = "pending"
	StatusRunning       = "running"
	StatusWaiting       = "waiting"
	StatusAwaitingAgent = "awaiting_agent"
	StatusBlocked       = "blocked"
	StatusDone          = "done"
	StatusSkipped       = "skipped"
	StatusFailed        = "failed"
)

// 任務狀態（ADR-0004）。
const (
	TaskPending = "pending"
	TaskLeased  = "leased"
	TaskDone    = "done"
	TaskFailed  = "failed"
)

// Item 是一部影音。
type Item struct {
	ID          string
	Platform    string
	ExternalID  string
	Part        int
	ChannelID   string
	ProfileID   string
	Overrides   json.RawMessage
	UntilStage  string
	Title       string
	URL         string
	PublishedAt *time.Time
	DurationS   *int
	// OriginalLang 為空代表還不知道；LangSource 是 metadata | asr | detected。
	OriginalLang string
	LangSource   string
	LangMixed    bool
	Availability string
	MediaKind    string
	Metadata     json.RawMessage
	CreatedAt    time.Time
	DeletedAt    *time.Time
}

// NewItem 是新增影音所需的欄位。
type NewItem struct {
	Platform   string
	ExternalID string
	Part       int
	URL        string
	ProfileID  string
	Overrides  json.RawMessage // 空代表 {}
	UntilStage string
}

// ItemMeta 是 metadata 步驟取得的欄位。空字串與 nil 代表平台沒有提供，不覆蓋原有的值。
type ItemMeta struct {
	Title        string
	DurationS    *int
	PublishedAt  *time.Time
	OriginalLang string
	LangSource   string
	Availability string
	ChannelID    string
	Metadata     json.RawMessage
}

// Channel 是頻道。
type Channel struct {
	ID         string
	Platform   string
	ExternalID string
	Title      string
	URL        string
}

// StageRun 是影音某個步驟的一次執行。
type StageRun struct {
	ID               string
	ItemID           string
	Stage            string
	Lang             string
	Variant          string
	Status           string
	BlockReason      string
	ResumeOn         string // blocked 時機器可讀的恢復條件，例如 cookie:bilibili；其他狀態為空
	ErrorClass       string
	Message          string
	Actions          []string
	Executor         string
	CacheKey         string
	ImplVersion      int
	ConfigSnapshotID string
	ArtifactID       string
	NotBefore        *time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	CreatedAt        time.Time
}

// ExecutedRun 是一次實際執行過的步驟，附上當時生效的設定（PIPE-R13）。
type ExecutedRun struct {
	ID          string
	Stage       string
	Status      string
	StartedAt   time.Time
	FinishedAt  *time.Time
	CacheKey    string
	ImplVersion int
	// ConfigJSON 是該次 config snapshot 的生效設定。
	ConfigJSON string
}

// NewStageRun 是新增步驟所需的欄位。
type NewStageRun struct {
	ItemID           string
	Stage            string
	Lang             string
	Variant          string
	Status           string
	ConfigSnapshotID string
}

// StageState 是步驟狀態與相關說明；每個欄位都會寫入（沒有設定的視為清除）。
type StageState struct {
	Status      string
	BlockReason string
	ResumeOn    string // 只在 blocked 時寫入（格式見 errclass.Error.ResumeOn），其他狀態一律清空
	ErrorClass  string
	Message     string
	Actions     []string
	NotBefore   *time.Time
}

// StageResult 是步驟完成後記錄的產出與快取鍵。
type StageResult struct {
	Executor    string
	CacheKey    string
	ImplVersion int
	ArtifactID  string
}

// Task 是佇列中的執行單位（ADR-0004）。
type Task struct {
	ID            string
	StageRunID    string
	Kind          string
	Executor      string
	ResourceClass string
	State         string
	Priority      int
	Payload       json.RawMessage
	ChunkIndex    *int
	LeaseToken    string
	LeaseOwner    string
	LeaseUntil    *time.Time
	Attempts      int
	MaxAttempts   int
	NotBefore     *time.Time
	DeadlineAt    *time.Time
	CreatedAt     time.Time
}

// NewTask 是新增任務所需的欄位。
type NewTask struct {
	StageRunID    string
	Kind          string
	Executor      string
	ResourceClass string
	Priority      int
	Payload       json.RawMessage // 空代表 {}
	MaxAttempts   int             // 0 代表 3
	NotBefore     *time.Time
	DeadlineAt    *time.Time
}

// ClaimOptions 是領取任務的條件。
type ClaimOptions struct {
	Owner string
	// Max 是一次最多領取的任務數。
	Max int
	// Lease 是租約長度。
	Lease time.Duration
	// Executor 限定執行者（builtin、api、agent）。
	Executor string
	// ResourceClasses 限定資源類別；空代表全部。
	ResourceClasses []string
	// AgingInterval 是等待多久優先度提高一級（PIPE-R9）。
	AgingInterval time.Duration
}

// Artifact 是步驟的產出。
type Artifact struct {
	ID           string
	ItemID       string
	Kind         string
	Variant      string
	Lang         string
	Format       string
	Path         string
	SHA256       string
	Size         int64
	Parents      []string
	Provenance   json.RawMessage
	SupersededBy string
	Pinned       bool
	CreatedAt    time.Time
}

// Probe 是一次 Probe 中某個來源的結果（SRC-R17）。
type Probe struct {
	ID        string
	ItemID    string
	RunID     string
	SourceID  string
	Lang      string
	Kind      string
	Available bool
	EstCost   *float64
	// State 是 used | rejected | unavailable；Reason 是沒有採用的原因。
	State    string
	Reason   string
	ProbedAt time.Time
}
