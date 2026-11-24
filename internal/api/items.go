package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 錯誤代碼（items 與 settings）。
const (
	CodeUnrecognizedURL = "unrecognized_url"
	CodeChannel         = "channel_not_ingestable"
)

// Item 是影音（ING、LIB 規格的欄位）。
type Item struct {
	ID           string         `json:"id"`
	Ref          string         `json:"ref" doc:"影音的短代號，例如 yt:abc123xxxxx"`
	Platform     string         `json:"platform"`
	ExternalID   string         `json:"external_id"`
	Part         int            `json:"part,omitempty" doc:"Bilibili 的分P，其他平台沒有"`
	URL          string         `json:"url"`
	Title        string         `json:"title"`
	ChannelID    string         `json:"channel_id,omitempty"`
	DurationS    *int           `json:"duration_s"`
	PublishedAt  *time.Time     `json:"published_at"`
	OriginalLang string         `json:"original_lang,omitempty"`
	LangSource   string         `json:"lang_source,omitempty" doc:"原文語言的來源：metadata、asr 或 detected"`
	LangMixed    bool           `json:"lang_mixed"`
	Availability string         `json:"availability,omitempty"`
	Overrides    map[string]any `json:"overrides" doc:"新增時的單次覆寫"`
	Until        string         `json:"until,omitempty" doc:"新增時指定做到哪個步驟為止"`
	CreatedAt    time.Time      `json:"created_at"`
	// SubscriptionID 是建立這部影音的訂閱（SUB-R13）；手動新增或訂閱已刪除時是 null。
	SubscriptionID *string `json:"subscription_id" nullable:"true" doc:"建立這部影音的訂閱；手動新增或訂閱已刪除時是 null"`
	// ReadState 是已讀狀態（LIB-R19）；打開影音詳情不會自動標為已讀。
	ReadState string `json:"read_state" enum:"unread,read" doc:"已讀狀態：unread（新增時的預設）或 read；用批次的 mark_read、mark_unread 改變"`
}

// Stage 是影音某個步驟的狀態（plan 第 5.3 節）。
type Stage struct {
	Stage            string     `json:"stage"`
	Status           string     `json:"status"`
	BlockReason      string     `json:"block_reason,omitempty"`
	Executor         string     `json:"executor,omitempty" doc:"步驟實際使用的執行方式（local、api、agent、off、opencc）；還沒決定時省略"`
	ErrorClass       string     `json:"error_class,omitempty"`
	NotBefore        *time.Time `json:"not_before"`
	Message          string     `json:"message,omitempty"`
	Actions          []string   `json:"actions"`
	ActionCodes      []string   `json:"action_codes,omitempty" doc:"actions 中有穩定代碼的動作（例如 use_asr_now：POST /api/items/{id}/use-asr-now）；沒有時省略"`
	ConfigSnapshotID string     `json:"config_snapshot_id"`
	ArtifactID       string     `json:"artifact_id,omitempty"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	Attempts         int        `json:"attempts" doc:"最近一個任務已經嘗試的次數；重啟時放回的租約不計入（PIPE-R11）"`
	// Batch 只在步驟等待 Batch API 的結果時出現（BATCH 的介面）。
	Batch *StageBatch `json:"batch,omitempty" doc:"步驟等待 Batch API 的結果時才有"`
}

// StageBatch 是步驟正在等待的 Batch API（BATCH-R3）。
type StageBatch struct {
	Provider    string    `json:"provider"`
	SubmittedAt time.Time `json:"submitted_at"`
	DeadlineAt  time.Time `json:"deadline_at" doc:"等待的期限；超過時改走一般 API（BATCH-R5）"`
}

// Source 是一個字幕來源在最近一次 Probe 中的結果（SRC-R17）。
type Source struct {
	Source string `json:"source"`
	Lang   string `json:"lang,omitempty"`
	Kind   string `json:"kind,omitempty"`
	State  string `json:"state" enum:"used,rejected,unavailable"`
	Reason string `json:"reason,omitempty" doc:"沒有採用的原因"`
}

// IngestedItem 是新增的結果。
type IngestedItem struct {
	Item
	AlreadyInLibrary bool `json:"already_in_library"`
	// InTrash 為 true 代表影音在垃圾桶中，沒有重新建立；先還原才能使用（DEL-R10）。
	InTrash bool `json:"in_trash"`
}

// ItemDetail 是影音詳情。
type ItemDetail struct {
	Item
	Stages  []Stage  `json:"stages"`
	Sources []Source `json:"sources"`
	// SponsorSegments 是現行的 SponsorBlock 片段（SPB-R4）：沒有片段時是空陣列，sponsorblock 步驟沒有執行過時沒有這個欄位。
	SponsorSegments *[]SponsorSegment `json:"sponsor_segments,omitempty" doc:"SponsorBlock 的片段，依開始時間排序；沒有片段時是空陣列，步驟沒有執行過時沒有這個欄位"`
	// Chapters 是現行的章節（CHAP-R7）：沒有章節時是空陣列，chapters 步驟沒有產生過產出時沒有這個欄位。
	Chapters *[]Chapter `json:"chapters,omitempty" doc:"章節，依開始時間排序；沒有章節時是空陣列，chapters 步驟沒有產生過產出時沒有這個欄位"`
	// HubShares 是分享到 Hub 的紀錄（HSHR-R2）；沒有分享過時沒有這個欄位。
	HubShares []HubShareRef `json:"hub_shares,omitempty" doc:"分享到 Hub 的紀錄，依時間排序；沒有分享過時沒有這個欄位"`
	// Summary 是現行的摘要（DIG-R5）；沒有摘要產出時沒有這個欄位。
	Summary *ItemSummary `json:"summary,omitempty" doc:"摘要（DIG-R5）；沒有摘要產出時沒有這個欄位"`
	// Reprobe 是排定的重新 Probe（SRC-R18），BetterSource 是事後發現的更好字幕來源（SRC-R23）；沒有時都是 null。
	Reprobe      *ItemReprobe      `json:"reprobe" doc:"排定的重新 Probe（SRC-R18）；沒有排程時是 null"`
	BetterSource *ItemBetterSource `json:"better_source" doc:"事後發現的更好字幕來源（SRC-R23）；沒有時是 null"`
	// ImportedFrom 是最近一次從 bundle 匯入的來源（BND-R8、N7）；沒有時是 null。
	ImportedFrom *ItemImportedFrom `json:"imported_from" doc:"最近一次從 bundle 匯入的來源（BND-R8）；沒有從 bundle 匯入過時是 null"`
}

// ItemImportedFrom 是影音的產出從哪個實例的 bundle 匯入（BND-R8）。值取自 bundle 的 manifest，是不可信的文字。
type ItemImportedFrom struct {
	InstanceID   string    `json:"instance_id" doc:"產生 bundle 的實例 ID（UUID）；bundle 沒有寫時是空字串"`
	AppVersion   string    `json:"app_version" doc:"產生 bundle 的 Notesteme 版本"`
	BundleSHA256 string    `json:"bundle_sha256" doc:"bundle 檔案的 sha256"`
	ImportedAt   time.Time `json:"imported_at" doc:"匯入的時間"`
}

// importedFromView 轉成 API 的形狀；manifest 的字串在輸出前去掉控制與格式字元並限制長度（不可信的輸入）。
func importedFromView(b *store.BundleImport) *ItemImportedFrom {
	if b == nil {
		return nil
	}
	clean := func(s string, n int) string {
		s = strings.Map(func(r rune) rune {
			// 控制字元、格式字元（例如 U+202E 雙向覆寫）與不合法的 UTF-8 一律去掉。
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
				return -1
			}
			return r
		}, s)
		if rs := []rune(s); len(rs) > n {
			s = string(rs[:n])
		}
		return s
	}
	return &ItemImportedFrom{InstanceID: clean(b.InstanceID, 64), AppVersion: clean(b.AppVersion, 64),
		BundleSHA256: clean(b.BundleSHA256, 64), ImportedAt: b.ImportedAt}
}

// ItemReprobe 是影音排定的重新 Probe（SRC-R18）。
type ItemReprobe struct {
	NextAt       time.Time `json:"next_at" doc:"下一次重新 Probe 的時間"`
	Until        time.Time `json:"until" doc:"最後一次不晚於這個時間（發布時間＋7 天）"`
	Runs         int       `json:"runs" doc:"已經執行的次數"`
	Sources      bool      `json:"sources" doc:"是否重新列出平台字幕軌"`
	SponsorBlock bool      `json:"sponsorblock" doc:"是否重新查詢 SponsorBlock（SRC-R20）"`
}

// ItemBetterSource 是事後發現的更好字幕來源（SRC-R23）；改用它要從 transcript 重跑。
type ItemBetterSource struct {
	Source      string    `json:"source"`
	Lang        string    `json:"lang"`
	Kind        string    `json:"kind"`
	FoundAt     time.Time `json:"found_at"`
	EstimateUSD *float64  `json:"estimate_usd" nullable:"true" doc:"從 transcript 重跑的預估費用；無法估算時是 null"`
}

// ItemSummary 是影音的摘要（DIG-R5）。內容來自模型，是不可信的文字，顯示時要跳脫。
type ItemSummary struct {
	Text             string   `json:"text" doc:"摘要（可以有換行）"`
	KeyPoints        []string `json:"key_points" doc:"0～7 個重點"`
	TitleTranslation *string  `json:"title_translation" doc:"標題譯文；原文與摘要同語言時是 null"`
	Language         string   `json:"language" doc:"摘要的語言"`
}

// Chapter 是一個章節；時間是 HH:MM:SS.mmm。
type Chapter struct {
	Title  string `json:"title" doc:"章節標題（不可信的文字，顯示時要跳脫）"`
	Start  string `json:"start" doc:"開始時間（HH:MM:SS.mmm）"`
	End    string `json:"end" doc:"結束時間（HH:MM:SS.mmm）"`
	Origin string `json:"origin" enum:"platform,llm" doc:"章節的來源：platform 是平台提供的章節，llm 是模型依字幕提出的章節"`
}

// SponsorSegment 是一個 SponsorBlock 片段；時間是 HH:MM:SS.mmm。
type SponsorSegment struct {
	Category string `json:"category" doc:"SponsorBlock 的類別，例如 sponsor"`
	Start    string `json:"start" doc:"開始時間（HH:MM:SS.mmm）"`
	End      string `json:"end" doc:"結束時間（HH:MM:SS.mmm）"`
}

func toItem(it store.Item) Item {
	out := Item{
		ID: it.ID, Ref: source.Ref(it.Platform, it.ExternalID, it.Part), Platform: it.Platform, ExternalID: it.ExternalID,
		Part: it.Part, URL: it.URL, Title: it.Title, ChannelID: it.ChannelID, DurationS: it.DurationS,
		PublishedAt: it.PublishedAt, OriginalLang: it.OriginalLang, LangSource: it.LangSource, LangMixed: it.LangMixed,
		Availability: it.Availability, Until: it.UntilStage, CreatedAt: it.CreatedAt,
		Overrides: map[string]any{}, ReadState: it.ReadState,
	}
	if out.ReadState == "" {
		out.ReadState = store.ReadUnread
	}
	_ = json.Unmarshal(it.Overrides, &out.Overrides)
	if it.SubscriptionID != "" {
		id := it.SubscriptionID
		out.SubscriptionID = &id
	}
	return out
}

func toStage(s store.StageRun, attempts int) Stage {
	actions := s.Actions
	if actions == nil {
		actions = []string{}
	}
	return Stage{
		Stage: s.Stage, Status: s.Status, BlockReason: s.BlockReason, Executor: s.Executor, ErrorClass: s.ErrorClass, NotBefore: s.NotBefore,
		Message: s.Message, Actions: actions, ActionCodes: pipeline.ActionCodes(actions), ConfigSnapshotID: s.ConfigSnapshotID, ArtifactID: s.ArtifactID,
		StartedAt: s.StartedAt, FinishedAt: s.FinishedAt, Attempts: attempts,
	}
}

// ---- 新增 ----

// allInTrash 回報新增的結果是否非空，而且每一部影音都在垃圾桶中。
func allInTrash(res []pipeline.IngestResult) bool {
	for _, r := range res {
		if !r.InTrash {
			return false
		}
	}
	return len(res) > 0
}

type createItemInput struct {
	Body struct {
		URL       string         `json:"url,omitempty" doc:"影音的網址"`
		Path      string         `json:"path,omitempty" doc:"本機檔案的路徑（要在 inbox 或 media_roots 之內；檔案不存在時仍建立，處理時才會失敗）"`
		Parts     []int          `json:"parts,omitempty" doc:"Bilibili 要新增的分P；沒有指定時新增全部"`
		Episodes  []string       `json:"episodes,omitempty" doc:"Podcast 要新增的集（guid）"`
		Profile   string         `json:"profile,omitempty" doc:"處理方案的名稱；沒有指定時使用預設處理方案；不存在回 422"`
		Overrides map[string]any `json:"overrides,omitempty" doc:"單次覆寫，只影響這次新增的影音"`
		Until     string         `json:"until,omitempty" doc:"做到哪個步驟為止（含）"`
	}
}

type createItemOutput struct {
	Status int
	Body   struct {
		Items []IngestedItem `json:"items"`
	}
}

// createItems 新增影音：有新建立的回 201，全部已經存在回 200（ING-R2）。
func (rt *routes) createItems(ctx context.Context, in *createItemInput) (*createItemOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if in.Body.Path != "" && in.Body.URL != "" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "url 與 path 只能擇一提供。")
	}
	// 路徑防護（GRD-R7）：不在根目錄內的路徑一律 403；之後都用解析過的路徑，避免檢查之後被換成 symlink 的競態。
	var resolved string
	if in.Body.Path != "" {
		var err error
		if resolved, err = rt.d.Paths.Resolve(in.Body.Path); err != nil {
			return nil, newError(http.StatusForbidden, CodePathOutsideRoots, "只能新增媒體資料夾中的檔案。")
		}
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	switch {
	case in.Body.Path != "":
		return rt.createLocalItem(ctx, in, resolved)
	case in.Body.URL == "":
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供要新增的網址（url）或本機檔案路徑（path）。")
	}
	res, err := rt.d.Pipeline.Ingest(ctx, pipeline.IngestRequest{
		URL: in.Body.URL, Parts: in.Body.Parts, Episodes: in.Body.Episodes, Profile: in.Body.Profile,
		Overrides: in.Body.Overrides, Until: in.Body.Until,
	})
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	// 要新增的影音全部都在垃圾桶中時回 409，動作「還原」（DEL-R10）。這要在 mapErr 之前處理：
	// 讀取垃圾桶中的影音是 404，新增它是 409。只有部分在垃圾桶中時照常回應，在 items 中標示 in_trash。
	if allInTrash(res) {
		return nil, inTrashError(http.StatusConflict, res[0].Item.ID)
	}
	out := &createItemOutput{Status: http.StatusOK}
	out.Body.Items = make([]IngestedItem, 0, len(res))
	for _, r := range res {
		out.Body.Items = append(out.Body.Items, IngestedItem{Item: toItem(r.Item), AlreadyInLibrary: r.AlreadyInLibrary, InTrash: r.InTrash})
		if !r.AlreadyInLibrary && !r.InTrash {
			out.Status = http.StatusCreated
		}
	}
	return out, nil
}

// CodeNotAFile：path 指到的東西存在，但不是一般檔案（目錄、FIFO、裝置等，422）。
const CodeNotAFile = "not_a_file"

// createLocalItem 以路徑新增本機檔案（ING-05）。延遲驗證：檔案不存在仍回 201，
// 之後 metadata 步驟以「找不到這個檔案。」失敗；存在但不是一般檔案才回 422。
func (rt *routes) createLocalItem(ctx context.Context, in *createItemInput, resolved string) (*createItemOutput, error) {
	if fi, err := os.Lstat(resolved); err == nil && !fi.Mode().IsRegular() {
		return nil, newError(http.StatusUnprocessableEntity, CodeNotAFile, "這個路徑不是一般檔案。")
	}
	opts := pipeline.LocalOptions{Profile: in.Body.Profile, Overrides: in.Body.Overrides, Until: in.Body.Until}
	if rt.d.Inbox != "" {
		// 識別鍵是相對「解析後的 inbox」的路徑；inbox 解析失敗就當成沒有 inbox（鍵為絕對路徑）。
		if inbox, err := rt.d.Paths.Resolve(rt.d.Inbox); err == nil {
			opts.Inbox = inbox
		}
	}
	res, err := rt.d.Pipeline.IngestPath(ctx, resolved, opts)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	if res.InTrash {
		return nil, inTrashError(http.StatusConflict, res.Item.ID)
	}
	out := &createItemOutput{Status: http.StatusOK}
	out.Body.Items = []IngestedItem{{Item: toItem(res.Item), AlreadyInLibrary: res.AlreadyInLibrary}}
	if !res.AlreadyInLibrary {
		out.Status = http.StatusCreated
	}
	return out, nil
}

// ---- 查詢 ----

// listItemsInput 是 GET /api/items 的查詢參數（LIB-R1、R3、R4、R7）。
type listItemsInput struct {
	Platform      string   `query:"platform" doc:"youtube、bilibili、podcast 或 local"`
	Channel       string   `query:"channel" doc:"頻道 ID"`
	Status        []string `query:"status,explode" doc:"<stage>:<狀態>，可以重複；同一個步驟的多個值是 OR，不同步驟之間是 AND"`
	ErrorClass    string   `query:"error_class" doc:"步驟最近一次執行的錯誤分類"`
	Lang          string   `query:"lang" doc:"原文語言"`
	PublishedFrom string   `query:"published_from" doc:"發布時間下限（含），RFC 3339"`
	PublishedTo   string   `query:"published_to" doc:"發布時間上限（含），RFC 3339"`
	DurationMin   string   `query:"duration_min" doc:"長度下限（秒，含），不得小於 0"`
	DurationMax   string   `query:"duration_max" doc:"長度上限（秒，含），不得小於 0"`
	Timing        string   `query:"timing" doc:"目前的字幕中至少有一個 cue 的 timing 是這個值：source、merged、silence、interpolated、model"`
	Q             string   `query:"q" doc:"標題搜尋：不分大小寫的部分字串，去掉前後空白，最多 200 字元"`
	Blocked       string   `query:"blocked" doc:"停住的原因，例如 awaiting_user（摘要後等你決定，DIG-R6）、budget_blocked、needs_action：至少有一個步驟的最新執行是 blocked 且原因是這個值"`
	Limit         int      `query:"limit" default:"50" minimum:"1" maximum:"200" doc:"單頁筆數"`
	Cursor        string   `query:"cursor" doc:"上一頁回傳的 next_cursor"`
	ReadState     string   `query:"read_state" doc:"read 或 unread（LIB-R19）；其他值回 422"`
}

type listItemsOutput struct {
	Body struct {
		Items []Item `json:"items"`
		Total int    `json:"total" doc:"符合條件的總數，和目前在第幾頁無關"`
		// NextCursor 在最後一頁省略，不回 null。
		NextCursor string `json:"next_cursor,omitempty" doc:"下一頁的游標；最後一頁沒有這個欄位"`
	}
}

// errBadCursor 是游標無法使用時的錯誤：格式不正確、被竄改、或篩選條件已經改變。
func errBadCursor() *Error {
	return newError(http.StatusUnprocessableEntity, CodeInvalidInput, "游標不正確或已失效，請從第一頁重新讀取。")
}

// parseStatus 把 "<stage>:<狀態>" 轉成 pipeline 的 Status；步驟與狀態是否合法由 ItemQuery.Validate 檢查。
func parseStatus(values []string) (map[string][]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string][]string{}
	for _, v := range values {
		if strings.Contains(v, ",") {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "status 的值不能用逗號分隔；多個條件請重複 status 參數，例如 status=metadata:done&status=normalize:done。")
		}
		stage, status, ok := strings.Cut(v, ":")
		if !ok || stage == "" || status == "" {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "status 的格式是 <步驟>:<狀態>，例如 normalize:done。")
		}
		if !slices.Contains(out[stage], status) {
			out[stage] = append(out[stage], status)
		}
	}
	return out, nil
}

func parseRFC3339(name, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, name+" 必須是 RFC 3339 時間，例如 2026-01-02T15:04:05Z。")
	}
	return &t, nil
}

// parseSeconds 解析不得小於 0 的整數秒；空字串代表沒有指定。
func parseSeconds(name, v string) (*int, error) {
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, name+" 必須是不小於 0 的整數（秒）。")
	}
	return &n, nil
}

func (rt *routes) listItems(ctx context.Context, in *listItemsInput) (*listItemsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	status, err := parseStatus(in.Status)
	if err != nil {
		return nil, err
	}
	from, err := parseRFC3339("published_from", in.PublishedFrom)
	if err != nil {
		return nil, err
	}
	to, err := parseRFC3339("published_to", in.PublishedTo)
	if err != nil {
		return nil, err
	}
	durMin, err := parseSeconds("duration_min", in.DurationMin)
	if err != nil {
		return nil, err
	}
	durMax, err := parseSeconds("duration_max", in.DurationMax)
	if err != nil {
		return nil, err
	}
	q := pipeline.ItemQuery{
		Platform: in.Platform, Channel: in.Channel, ErrorClass: in.ErrorClass, Lang: in.Lang, Timing: in.Timing,
		Q: strings.TrimSpace(in.Q), Blocked: in.Blocked, Status: status, PublishedFrom: from, PublishedTo: to,
		DurationMin: durMin, DurationMax: durMax, ReadState: in.ReadState, Limit: in.Limit,
	}
	// 不論有沒有 Pipeline，都先驗證參數與游標，不合法回 422。
	if err := q.Validate(); err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	fp := filterFingerprint(q)
	if in.Cursor != "" {
		if q.After, err = decodeItemCursor(in.Cursor, rt.d.CursorKey, fp); err != nil {
			return nil, errBadCursor()
		}
	}
	out := &listItemsOutput{}
	out.Body.Items = []Item{}
	if rt.d.Pipeline == nil {
		return out, nil
	}
	page, err := rt.d.Pipeline.ListItems(ctx, q)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	for _, it := range page.Items {
		out.Body.Items = append(out.Body.Items, toItem(it))
	}
	out.Body.Total = page.Total
	if page.Next != nil {
		out.Body.NextCursor = encodeItemCursor(*page.Next, rt.d.CursorKey, fp)
	}
	return out, nil
}

// ---- 游標 ----

// maxCursorLen 是游標字串的長度上限；正常的游標不到 150 字元。
const maxCursorLen = 512

// itemCursor 是游標的內容：版本、位置（created_at 毫秒與 id），以及 MAC。
// F 是 cursorMAC 的結果，涵蓋版本、位置與篩選條件的指紋；改動任何一項都會被拒絕。
type itemCursor struct {
	V  int    `json:"v"`
	T  int64  `json:"t"`
	ID string `json:"id"`
	F  string `json:"f"`
}

// filterFingerprint 是篩選條件的指紋（sha256 前 8 bytes 的 hex）；limit 與 cursor 不算在內。
// 游標只在相同的篩選條件下有效，條件改變時要從第一頁重新讀取。
func filterFingerprint(q pipeline.ItemQuery) string {
	type stageStatus struct {
		Stage    string   `json:"stage"`
		Statuses []string `json:"statuses"`
	}
	var status []stageStatus
	for stage, vals := range q.Status {
		vals = slices.Sorted(slices.Values(vals))
		status = append(status, stageStatus{stage, vals})
	}
	slices.SortFunc(status, func(a, b stageStatus) int { return strings.Compare(a.Stage, b.Stage) })
	ms := func(t *time.Time) *int64 {
		if t == nil {
			return nil
		}
		v := t.UnixMilli()
		return &v
	}
	b, _ := json.Marshal(struct {
		Platform, Channel, ErrorClass, Lang, Timing, Q string
		Status                                         []stageStatus
		From, To                                       *int64
		DurMin, DurMax                                 *int
		ReadState                                      string `json:",omitempty"`
		// Blocked 省略空值：沒有用這個篩選時指紋與之前相同。
		Blocked string `json:",omitempty"`
	}{q.Platform, q.Channel, q.ErrorClass, q.Lang, q.Timing, q.Q, status, ms(q.PublishedFrom), ms(q.PublishedTo), q.DurationMin, q.DurationMax, q.ReadState, q.Blocked})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// cursorMAC 是游標內容的 HMAC-SHA256（前 16 bytes 的 hex）：涵蓋版本、位置與篩選條件的指紋。
// 只比對篩選條件的指紋擋不住竄改：指紋不含位置，改掉 t 或 id 再重新編碼仍會通過。
func cursorMAC(key []byte, v int, t int64, id, fp string) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%d\x00%d\x00%s\x00%s", v, t, id, fp)
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func encodeItemCursor(c pipeline.ItemCursor, key []byte, fp string) string {
	b, _ := json.Marshal(itemCursor{V: 1, T: c.CreatedAtMs, ID: c.ID, F: cursorMAC(key, 1, c.CreatedAtMs, c.ID, fp)})
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeItemCursor 嚴格解析游標：長度上限、標準的 base64url（不補 =、不含換行、尾端位元為零）、
// 不接受未知欄位或多餘內容、版本為 1、id 必須是標準形式的 UUID、MAC 必須與內容及目前的篩選條件相符。任何不符都回傳錯誤，不 panic。
func decodeItemCursor(s string, key []byte, wantFP string) (*pipeline.ItemCursor, error) {
	if len(s) > maxCursorLen {
		return nil, errors.New("游標太長")
	}
	enc := base64.RawURLEncoding.Strict()
	raw, err := enc.DecodeString(s)
	if err != nil {
		return nil, err
	}
	// Strict 只擋尾端位元；Go 的解碼器仍會略過 \r 與 \n，所以再比對一次標準形式。
	if enc.EncodeToString(raw) != s {
		return nil, errors.New("游標不是標準的 base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c itemCursor
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("游標有多餘的內容")
	}
	if c.V != 1 || c.T < 0 {
		return nil, errors.New("游標的版本或位置不正確")
	}
	// 只接受小寫、帶連字號的標準形式；uuid.Parse 還會接受大括號、urn:uuid: 與沒有連字號的寫法。
	parsed, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, err
	}
	if c.ID != parsed.String() {
		return nil, errors.New("游標的 id 不是標準的 uuid")
	}
	if !hmac.Equal([]byte(c.F), []byte(cursorMAC(key, c.V, c.T, c.ID, wantFP))) {
		return nil, errors.New("游標被竄改或篩選條件已經改變")
	}
	return &pipeline.ItemCursor{CreatedAtMs: c.T, ID: c.ID}, nil
}

type itemInput struct {
	ID string `path:"id" doc:"影音 ID"`
}

type itemDetailOutput struct{ Body ItemDetail }

func (rt *routes) getItem(ctx context.Context, in *itemInput) (*itemDetailOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	d, err := rt.d.Pipeline.Item(ctx, id)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := toDetail(d)
	if out.HubShares, err = rt.hubShares(ctx, id); err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &itemDetailOutput{Body: out}, nil
}

func toDetail(d pipeline.Detail) ItemDetail {
	out := ItemDetail{Item: toItem(d.Item), Stages: []Stage{}, Sources: []Source{}}
	for _, s := range d.Stages {
		st := toStage(s, d.Attempts[s.ID])
		if b := d.Batches[s.ID]; b != nil {
			st.Batch = &StageBatch{Provider: b.Provider, SubmittedAt: b.SubmittedAt, DeadlineAt: b.DeadlineAt}
		}
		out.Stages = append(out.Stages, st)
	}
	for _, p := range d.Sources {
		out.Sources = append(out.Sources, Source{Source: p.SourceID, Lang: p.Lang, Kind: p.Kind, State: p.State, Reason: p.Reason})
	}
	if d.SponsorSegments != nil {
		segs := make([]SponsorSegment, 0, len(d.SponsorSegments))
		for _, s := range d.SponsorSegments {
			segs = append(segs, SponsorSegment{Category: s.Category, Start: subtitle.FormatDisplayTime(s.StartMS), End: subtitle.FormatDisplayTime(s.EndMS)})
		}
		out.SponsorSegments = &segs
	}
	if d.Chapters != nil {
		chs := make([]Chapter, 0, len(d.Chapters.Chapters))
		for _, c := range d.Chapters.Chapters {
			chs = append(chs, Chapter{Title: c.Title, Start: subtitle.FormatDisplayTime(c.StartMS), End: subtitle.FormatDisplayTime(c.EndMS), Origin: d.Chapters.Origin})
		}
		out.Chapters = &chs
	}
	if sm := d.Summary; sm != nil {
		out.Summary = &ItemSummary{Text: sm.Text, KeyPoints: sm.KeyPoints, TitleTranslation: sm.TitleTranslation, Language: sm.Language}
	}
	if rp := d.Reprobe; rp != nil {
		out.Reprobe = &ItemReprobe{NextAt: rp.NextAt.UTC(), Until: rp.Until.UTC(), Runs: rp.Runs, Sources: rp.Sources, SponsorBlock: rp.SponsorBlock}
	}
	if b := d.BetterSource; b != nil {
		out.BetterSource = &ItemBetterSource{Source: b.Source, Lang: b.Lang, Kind: b.Kind, FoundAt: b.FoundAt.UTC(), EstimateUSD: b.EstimateUSD}
	}
	out.ImportedFrom = importedFromView(d.ImportedFrom)
	return out
}

// ---- 執行紀錄 ----

// Run 是步驟的一次實際執行與當時生效的設定（PIPE-R13）。
type Run struct {
	ID          string          `json:"id"`
	Stage       string          `json:"stage"`
	Status      string          `json:"status"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at"`
	CacheKey    string          `json:"cache_key"`
	ImplVersion int             `json:"impl_version"`
	Config      json.RawMessage `json:"config" doc:"該次執行的 config snapshot：四層覆寫合併後的生效設定"`
}

type runsOutput struct {
	Body struct {
		Runs []Run `json:"runs"`
	}
}

// listRuns 回傳影音實際執行過的步驟，由新到舊。
func (rt *routes) listRuns(ctx context.Context, in *itemInput) (*runsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	runs, err := rt.d.Pipeline.Runs(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &runsOutput{}
	out.Body.Runs = make([]Run, 0, len(runs))
	for _, r := range runs {
		cfg := json.RawMessage(r.ConfigJSON)
		if !json.Valid(cfg) {
			cfg = json.RawMessage("{}")
		}
		out.Body.Runs = append(out.Body.Runs, Run{
			ID: r.ID, Stage: r.Stage, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
			CacheKey: r.CacheKey, ImplVersion: r.ImplVersion, Config: cfg,
		})
	}
	return out, nil
}

// ---- 重跑 ----

type rerunInput struct {
	ID   string `path:"id" doc:"影音 ID"`
	Body struct {
		From      string         `json:"from" minLength:"1" doc:"從哪個步驟開始重新處理"`
		Overrides map[string]any `json:"overrides,omitempty" doc:"這次重跑的單次覆寫"`
		Until     string         `json:"until,omitempty" doc:"做到哪個步驟為止（含）；沒有指定時做到最後"`
		Profile   string         `json:"profile,omitempty" doc:"這次重跑改用的處理方案；沒有指定時用影音目前的處理方案的最新版本（PRF-R3）；不存在回 422"`
		Chunks    []int          `json:"chunks,omitempty" doc:"只重新送出這些 chunk，從 1 起算，其他 chunk 重用快取（LLM-R8）；只能用在 LLM 步驟，否則回 422"`
		// FRM-R13：from 為 frames 時沿用現行的候選畫面，只重新選圖（frames 尚未實作，目前回 501）。
		ReuseCandidates bool `json:"reuse_candidates,omitempty" doc:"from 為 frames 時沿用現行的候選畫面，只重新選圖（FRM-R13）；其他步驟忽略"`
	}
}

type rerunOutput struct{ Body ItemDetail }

// rerun 從指定步驟重新建立 stage run 與任務（PIPE-R7）：產出新版本，舊版本標記 superseded_by。
func (rt *routes) rerun(ctx context.Context, in *rerunInput) (*rerunOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	d, err := rt.d.Pipeline.Rerun(ctx, in.ID, pipeline.RerunRequest{From: in.Body.From, Overrides: in.Body.Overrides, Until: in.Body.Until,
		Profile: in.Body.Profile, Chunks: in.Body.Chunks, ReuseCandidates: in.Body.ReuseCandidates})
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &rerunOutput{Body: toDetail(d)}, nil
}

// Cue 是 API 回傳的一句字幕；時間用 subtitle.FormatDisplayTime 格式化。
type Cue struct {
	Index  int    `json:"index"`
	Start  string `json:"start" example:"00:00:01.000"`
	End    string `json:"end" example:"00:00:03.500"`
	Text   string `json:"text"`
	Timing string `json:"timing,omitempty" doc:"時間的來源：source、merged、silence、interpolated、model"`
	Words  []Word `json:"words,omitempty" doc:"字詞級時間；來源沒有時省略"`
}

// Word 是一個字詞的時間。
type Word struct {
	Text  string `json:"text"`
	Start string `json:"start" example:"00:00:01.000"`
	End   string `json:"end" example:"00:00:01.800"`
}

func toCue(c subtitle.Cue) Cue {
	out := Cue{
		Index: c.Index, Start: subtitle.FormatDisplayTime(c.StartMS), End: subtitle.FormatDisplayTime(c.EndMS),
		Text: c.Text, Timing: string(c.Timing),
	}
	for _, w := range c.Words {
		out.Words = append(out.Words, Word{Text: w.Text, Start: subtitle.FormatDisplayTime(w.StartMS), End: subtitle.FormatDisplayTime(w.EndMS)})
	}
	return out
}

type cuesInput struct {
	ID      string `path:"id" doc:"影音 ID"`
	Variant string `query:"variant" doc:"字幕變體；沒指定時選處理得最完整的（OUT-R2）"`
	Lang    string `query:"lang" doc:"語言；沒指定時使用影音的原文語言"`
	From    string `query:"from" doc:"起點，HH:MM:SS(.mmm) 或秒數；只回傳和 [from, to) 有重疊的 cue（OUT-R3）"`
	To      string `query:"to" doc:"終點，格式同 from"`
}

type cuesOutput struct {
	Body struct {
		Variant string `json:"variant"`
		Lang    string `json:"lang"`
		Cues    []Cue  `json:"cues"`
	}
}

func (rt *routes) getCues(ctx context.Context, in *cuesInput) (*cuesOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	q, err := cuesQuery(in.Variant, in.Lang, in.From, in.To)
	if err != nil {
		return nil, err
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	tr, err := rt.d.Pipeline.Cues(ctx, id, q)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &cuesOutput{}
	out.Body.Variant, out.Body.Lang = string(tr.Variant), tr.Lang
	out.Body.Cues = make([]Cue, 0, len(tr.Cues))
	for _, c := range tr.Cues {
		out.Body.Cues = append(out.Body.Cues, toCue(c))
	}
	return out, nil
}

// parseTime 解析時間碼（HH:MM:SS.mmm）或秒數（75、75.5），回傳毫秒。
func parseTime(s string) (int64, error) {
	if strings.Contains(s, ":") {
		return subtitle.ParseTimecode(s)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	// NaN 與 Inf 不能只靠大小比較擋下（NaN 的比較一律為 false），要明確檢查。
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1e7 {
		return 0, errors.New("invalid seconds")
	}
	return int64(f * 1000), nil
}

// ---- 設定 ----

type settingsInput struct {
	Section string `path:"section" doc:"設定區段：language、timing、llm、agent、subtitle_rules、stages、supadata 或 services"`
}

type putSettingsInput struct {
	Section string `path:"section" doc:"設定區段：language、timing、llm、agent、subtitle_rules、stages、supadata 或 services"`
	// Body 用指標：沒有內容時不被 Huma 擋下，由 handler 回 422（services 區段自己處理）。
	Body *map[string]any
}

type settingsOutput struct{ Body map[string]any }

func (rt *routes) getSettings(ctx context.Context, in *settingsInput) (*settingsOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if in.Section == "services" {
		return rt.getServicesSettings(ctx)
	}
	if in.Section == "budget" {
		return rt.getBudget(ctx)
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	v, err := rt.d.Pipeline.GetSettings(ctx, in.Section)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &settingsOutput{Body: v}, nil
}

func (rt *routes) putSettings(ctx context.Context, in *putSettingsInput) (*settingsOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if in.Section == "services" {
		return rt.putServicesSettings(ctx, in.Body)
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	var body map[string]any
	if in.Body != nil {
		body = *in.Body
	}
	// 除了 services（上面已處理），所有區段的 body 都是物件；沒有內容或是 null 都回 422。
	if body == nil {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供 JSON 物件格式的設定內容。")
	}
	if in.Section == "budget" {
		return rt.putBudget(ctx, body)
	}
	v, err := rt.d.Pipeline.SetSettings(ctx, in.Section, body)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &settingsOutput{Body: v}, nil
}

// ---- 錯誤對應 ----

// mapErr 把 pipeline 的錯誤對應到 HTTP 錯誤；沒有對應的視為內部錯誤，內容只寫進 log。
func (rt *routes) mapErr(ctx context.Context, err error) error {
	var (
		inv *pipeline.InvalidError
		ni  *pipeline.NotImplementedError
		up  *pipeline.UpstreamError
		iu  *pipeline.InUseError
		ts  *pipeline.TaskStateError
		ce  *pipeline.CorruptArtifactError
	)
	if pe := policyError(err); pe != nil {
		return pe
	}
	switch {
	case errors.As(err, &up):
		// 原始錯誤（yt-dlp 的輸出）只寫進 log，不回傳給 client。
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "外部工具失敗", slog.Any("error", up.Err))
		return newError(http.StatusBadGateway, CodeUpstream, up.Msg)
	case errors.Is(err, pipeline.ErrUnrecognizedURL):
		return newError(http.StatusUnprocessableEntity, CodeUnrecognizedURL, err.Error())
	case errors.Is(err, pipeline.ErrChannel):
		return newError(http.StatusUnprocessableEntity, CodeChannel, err.Error())
	case errors.As(err, &inv):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
		e.Errors = inv.Fields
		return e
	case errors.As(err, &iu):
		e := newError(http.StatusConflict, iu.Code, iu.Error())
		e.UsedBy = iu.UsedBy
		return e
	case errors.Is(err, pipeline.ErrProfileNotFound), errors.Is(err, pipeline.ErrPromptNotFound), errors.Is(err, pipeline.ErrPromptVersionNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, pipeline.ErrProfileExists):
		return newError(http.StatusConflict, CodeProfileExists, err.Error())
	case errors.Is(err, pipeline.ErrBuiltinProfile):
		return newError(http.StatusConflict, CodeBuiltinProfile, err.Error())
	case errors.As(err, &ni):
		return newError(http.StatusNotImplemented, CodeNotImplemented, ni.Error())
	case errors.Is(err, pipeline.ErrInTrash):
		// 讀取與處理垃圾桶中的影音回 404；新增它回 409（DEL-R10），由新增的 handler 在呼叫 mapErr 之前處理。
		var tr *pipeline.InTrashError
		itemID := ""
		if errors.As(err, &tr) {
			itemID = tr.ItemID
		}
		return inTrashError(http.StatusNotFound, itemID)
	case errors.As(err, &ce):
		// NFR-REL-2：產出檔損毀，已標記並（能的話）從產生它的步驟重新處理。
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "讀到損毀的產出檔", slog.String("artifact", ce.ArtifactID), slog.Bool("rerun", ce.Rerun))
		return newError(http.StatusConflict, CodeArtifactCorrupt, ce.Error())
	case errors.Is(err, pipeline.ErrTaskNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.As(err, &ts):
		return newError(http.StatusConflict, ts.Code, ts.Error())
	case errors.Is(err, pipeline.ErrNoTranscript):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	rt.d.Log.LogAttrs(ctx, slog.LevelError, "處理請求時發生錯誤", slog.String("error", err.Error()))
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}

// itemID 把路徑上的 {id} 換成影音 ID：影音代號（yt:…、bili:…/p1 等，MCP 的 item 參數）以 pipeline.ResolveItem 查詢，
// 垃圾桶中的影音回 in_trash（DEL-R1）；其他字串原樣當成 ID（影音 ID 不會長得像代號）。
func (rt *routes) itemID(ctx context.Context, idOrRef string) (string, error) {
	if _, _, _, ok := source.SplitRef(idOrRef); !ok {
		return idOrRef, nil
	}
	it, err := rt.d.Pipeline.ResolveItem(ctx, idOrRef)
	if err != nil {
		return "", rt.mapErr(ctx, err)
	}
	return it.ID, nil
}

// noPipeline 回傳沒有注入 Pipeline 時的錯誤。這是設定錯誤，回 500；
// 501 在這個專案代表「功能還沒做」，測試會把它當成 pending，不能拿來表示設定錯誤（ADR-0015）。
func (rt *routes) noPipeline(ctx context.Context) error {
	rt.d.Log.LogAttrs(ctx, slog.LevelError, "處理請求時發生錯誤", slog.String("error", "api: 沒有設定 Pipeline"))
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}
