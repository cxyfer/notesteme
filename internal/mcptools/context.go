package mcptools

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source"
)

// get_video_context 的等待時間（MCP-R5）：預設 300 秒，上限 600 秒。
const (
	defaultContextTimeoutS = 300
	maxContextTimeoutS     = 600
	// retryAfterS 是還沒完成時建議的重試間隔。
	retryAfterS = 30
)

// contextUntil 是新增影音時做到的步驟：MCP-R5 的「做到字幕」是做到 normalize，
// 字幕在這之後才是可以直接閱讀的樣子（MCP 規格待決問題，待 owner 追認）。
const contextUntil = "normalize"

// readyStages 是「字幕已備妥」要完成的步驟：全部 done 或 skipped（steps.md 規則 2c）。
var readyStages = []string{"metadata", "transcript", "normalize"}

type contextArgs struct {
	URL      string `json:"url" jsonschema:"影片的網址（YouTube 或 Bilibili 的單一影片）"`
	TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"影片還沒處理好時最多等待幾秒，1～600，預設 300"`
	Lang     string `json:"lang,omitempty" jsonschema:"字幕的語言；沒有指定時使用影片的原文語言"`
}

// stageView 是 API 的步驟狀態中 get_video_context 需要的欄位。
type stageView struct {
	Stage       string   `json:"stage"`
	Status      string   `json:"status"`
	BlockReason string   `json:"block_reason,omitempty"`
	ErrorClass  string   `json:"error_class,omitempty"`
	Message     string   `json:"message,omitempty"`
	Actions     []string `json:"actions"`
}

// itemView 是 GET /api/items/{id} 的回應中 get_video_context 需要的欄位。
type itemView struct {
	ID           string      `json:"id"`
	Ref          string      `json:"ref"`
	Platform     string      `json:"platform"`
	URL          string      `json:"url"`
	Title        string      `json:"title"`
	ChannelID    string      `json:"channel_id,omitempty"`
	DurationS    *int        `json:"duration_s"`
	PublishedAt  *string     `json:"published_at"`
	OriginalLang string      `json:"original_lang,omitempty"`
	Stages       []stageView `json:"stages"`
}

// contextResult 是 get_video_context 的結果。status 是 ready、processing、blocked 或 failed。
type contextResult struct {
	Status       string      `json:"status"`
	Item         string      `json:"item"`
	Title        string      `json:"title"`
	URL          string      `json:"url,omitempty"`
	Platform     string      `json:"platform,omitempty"`
	ChannelID    string      `json:"channel_id,omitempty"`
	DurationS    *int        `json:"duration_s,omitempty"`
	PublishedAt  *string     `json:"published_at,omitempty"`
	OriginalLang string      `json:"original_lang,omitempty"`
	Stages       []stageView `json:"stages,omitempty"`
	// Transcript 只在 ready 時提供：字幕以結構化欄位提供（MCP-R7）。
	Transcript *contextTranscript `json:"transcript,omitempty"`
	// Stage、Message、Actions 在 blocked 或 failed 時指出卡在哪個步驟、原因與修正動作。
	Stage   string   `json:"stage,omitempty"`
	Message string   `json:"message,omitempty"`
	Actions []string `json:"actions,omitempty"`
	// RetryAfterS 在還沒完成時建議多久之後再呼叫一次。
	RetryAfterS int `json:"retry_after_s,omitempty"`
}

type contextTranscript struct {
	Variant string          `json:"variant"`
	Lang    string          `json:"lang"`
	Cues    json.RawMessage `json:"cues"`
}

func addGetVideoContext(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_video_context",
		Description: "回答「這部影片在說什麼」用的一次性呼叫：回傳影片的中繼資料與字幕。影片還不在媒體庫時先新增並處理到字幕（需要 ingest scope），" +
			"最多等待 timeout_s 秒；還沒完成時回傳 status: processing 與建議的重試時間，卡住時回傳 status: blocked、訊息與修正動作。需要 read scope。" +
			UntrustedNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a contextArgs) (*mcp.CallToolResult, any, error) {
		return getVideoContext(ctx, d, a), nil, nil
	})
}

func getVideoContext(ctx context.Context, d *Deps, a contextArgs) *mcp.CallToolResult {
	if d.Request != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(d.Request, cancel)()
	}
	if r := checkStrings(field{"url", a.URL, maxURLLen}, field{"lang", a.Lang, maxIDLen}); r != nil {
		return r
	}
	timeout := a.TimeoutS
	if timeout == 0 {
		timeout = defaultContextTimeoutS
	}
	if timeout < 1 || timeout > maxContextTimeoutS {
		return invalid("timeout_s 必須是 1～%d 的整數。", maxContextTimeoutS)
	}
	t, ok := source.Recognize(a.URL)
	if !ok {
		// 與 POST /api/items 相同的錯誤（MCP-R3）。
		return errorResult(apiError{Status: http.StatusUnprocessableEntity, Code: "unrecognized_url",
			Message: "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。"})
	}
	if t.Kind != source.KindVideo {
		return invalid("get_video_context 只接受單一影片的網址；播放清單或頻道請用 ingest。")
	}
	part := t.Part
	if t.Platform == source.PlatformBilibili && part == 0 {
		part = 1
	}
	ref := source.Ref(t.Platform, t.ExternalID, part)

	// 先訂閱再讀狀態：讀取之後發生的變更一定收得到（與 GET /api/events 相同的順序）。
	w := newWaiter(d, ref)
	defer w.stop()

	it, res := fetchItem(ctx, d, ref)
	if res != nil && notFound(res) {
		// 影片不在媒體庫：新增並處理到字幕。沒有 ingest scope 時由 API 回 403（MCP-R5）。
		resp, err := d.Backend.Do(ctx, http.MethodPost, "/api/items", nil,
			map[string]string{"url": t.URL, "until": contextUntil})
		if err != nil {
			return backendErrorResult(ctx, err)
		}
		if resp.Status/100 != 2 {
			return apiErrorResult(resp)
		}
		it, res = fetchItem(ctx, d, ref)
	}
	if res != nil {
		return res
	}
	w.id = it.ID

	// 等待的期間不佔用 worker：只是在狀態變更時重新讀取。等待的請求太多時不等待，直接回報目前的狀態。
	waiting := false
	if d.Waits != nil {
		select {
		case d.Waits <- struct{}{}:
			waiting = true
			defer func() { <-d.Waits }()
		default:
		}
	} else {
		waiting = true
	}
	deadline := d.Clock.NewTimer(time.Duration(timeout) * time.Second)
	defer deadline.Stop()
	for {
		out, done := evaluate(it)
		if done {
			if out.Status == "ready" {
				return readyResult(ctx, d, ref, a.Lang, out)
			}
			return jsonResult(out)
		}
		if !waiting {
			out.RetryAfterS = retryAfterS
			return jsonResult(out)
		}
		if !w.wait(ctx, deadline.C()) {
			if ctx.Err() != nil {
				return backendErrorResult(ctx, ctx.Err())
			}
			out.RetryAfterS = retryAfterS
			return jsonResult(out)
		}
		if it, res = fetchItem(ctx, d, ref); res != nil {
			return res
		}
	}
}

// waiter 包裝狀態變更的訂閱：只在這部影音有變更時重查；訂閱被斷開（跟不上）時重新訂閱並立刻重查；
// 無法訂閱時以 PollInterval 重查。
type waiter struct {
	d   *Deps
	sub *events.Subscription
	// ref 與 id 是要等的影音（事件的 Item 與 ItemID）；id 在讀到影音之後才知道。
	ref, id string
}

func newWaiter(d *Deps, ref string) *waiter {
	w := &waiter{d: d, ref: ref}
	w.subscribe()
	return w
}

func (w *waiter) subscribe() {
	if w.d.Events == nil {
		return
	}
	if sub, ok := w.d.Events.Subscribe(); ok {
		w.sub = sub
	}
}

// wait 等到下一次應該重查狀態；ctx 取消或 deadline 到期時回傳 false。
func (w *waiter) wait(ctx context.Context, deadline <-chan time.Time) bool {
	if w.sub == nil {
		t := w.d.Clock.NewTimer(PollInterval)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-t.C():
			return true
		}
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case ev := <-w.sub.C:
			if ev.Item == w.ref || (w.id != "" && ev.ItemID == w.id) {
				return true
			}
		case <-w.sub.Done():
			// 被斷開：重新訂閱，並立刻重查一次（斷開期間可能漏了事件）。
			w.sub = nil
			w.subscribe()
			return true
		}
	}
}

func (w *waiter) stop() {
	if w.sub != nil {
		w.sub.Close()
	}
}

// fetchItem 讀取影音；失敗時回傳錯誤結果。
func fetchItem(ctx context.Context, d *Deps, ref string) (itemView, *mcp.CallToolResult) {
	resp, err := d.Backend.Do(ctx, http.MethodGet, itemPath(ref, ""), nil, nil)
	if err != nil {
		return itemView{}, backendErrorResult(ctx, err)
	}
	if resp.Status/100 != 2 {
		return itemView{}, apiErrorResult(resp)
	}
	var it itemView
	if err := json.Unmarshal(resp.Body, &it); err != nil {
		return itemView{}, internalError()
	}
	return it, nil
}

// notFound 回報錯誤結果是不是「影音不存在」（不含在垃圾桶中的影音：in_trash 照原樣回報）。
func notFound(r *mcp.CallToolResult) bool {
	e, ok := r.StructuredContent.(apiError)
	return ok && e.Status == http.StatusNotFound && e.Code == "not_found"
}

// evaluate 依步驟狀態判斷影音目前的情況；done 為 true 代表不用再等（ready、blocked、failed）。
func evaluate(it itemView) (contextResult, bool) {
	out := contextResult{
		Item: it.Ref, Title: it.Title, URL: it.URL, Platform: it.Platform, ChannelID: it.ChannelID,
		DurationS: it.DurationS, PublishedAt: it.PublishedAt, OriginalLang: it.OriginalLang,
	}
	ready := true
	for _, name := range readyStages {
		i := slices.IndexFunc(it.Stages, func(s stageView) bool { return s.Stage == name })
		if i < 0 {
			ready = false
			continue
		}
		st := it.Stages[i]
		switch st.Status {
		case "done", "skipped":
		case "blocked", "failed":
			out.Status, out.Stage, out.Message, out.Actions = st.Status, st.Stage, st.Message, st.Actions
			return out, true
		default:
			ready = false
		}
	}
	if ready {
		out.Status = "ready"
		return out, true
	}
	out.Status = "processing"
	for _, st := range it.Stages {
		if slices.Contains(readyStages, st.Stage) {
			out.Stages = append(out.Stages, stageView{Stage: st.Stage, Status: st.Status})
		}
	}
	return out, false
}

// readyResult 補上字幕（cues）。字幕讀不到（例如沒有可用的字幕軌）時回報 API 的錯誤。
func readyResult(ctx context.Context, d *Deps, ref, lang string, out contextResult) *mcp.CallToolResult {
	tr := getTranscript(ctx, d, transcriptArgs{Item: ref, Lang: lang})
	if tr.IsError {
		return tr
	}
	var t transcriptResult
	if err := json.Unmarshal(tr.StructuredContent.(json.RawMessage), &t); err != nil {
		return internalError()
	}
	out.Transcript = &contextTranscript{Variant: t.Variant, Lang: t.Lang, Cues: t.Cues}
	return jsonResult(out)
}
