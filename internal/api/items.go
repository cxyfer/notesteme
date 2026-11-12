package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	Ref          string         `json:"ref" doc:"影音的短代號，例如 yt:abc123"`
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
}

// Stage 是影音某個步驟的狀態（plan 第 5.3 節）。
type Stage struct {
	Stage            string     `json:"stage"`
	Status           string     `json:"status"`
	BlockReason      string     `json:"block_reason,omitempty"`
	ErrorClass       string     `json:"error_class,omitempty"`
	NotBefore        *time.Time `json:"not_before"`
	Message          string     `json:"message,omitempty"`
	Actions          []string   `json:"actions"`
	ConfigSnapshotID string     `json:"config_snapshot_id"`
	ArtifactID       string     `json:"artifact_id,omitempty"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
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
}

// ItemDetail 是影音詳情。
type ItemDetail struct {
	Item
	Stages  []Stage  `json:"stages"`
	Sources []Source `json:"sources"`
}

func toItem(it store.Item) Item {
	out := Item{
		ID: it.ID, Ref: source.Ref(it.Platform, it.ExternalID, it.Part), Platform: it.Platform, ExternalID: it.ExternalID,
		Part: it.Part, URL: it.URL, Title: it.Title, ChannelID: it.ChannelID, DurationS: it.DurationS,
		PublishedAt: it.PublishedAt, OriginalLang: it.OriginalLang, LangSource: it.LangSource, LangMixed: it.LangMixed,
		Availability: it.Availability, Until: it.UntilStage, CreatedAt: it.CreatedAt,
		Overrides: map[string]any{},
	}
	_ = json.Unmarshal(it.Overrides, &out.Overrides)
	return out
}

func toStage(s store.StageRun) Stage {
	actions := s.Actions
	if actions == nil {
		actions = []string{}
	}
	return Stage{
		Stage: s.Stage, Status: s.Status, BlockReason: s.BlockReason, ErrorClass: s.ErrorClass, NotBefore: s.NotBefore,
		Message: s.Message, Actions: actions, ConfigSnapshotID: s.ConfigSnapshotID, ArtifactID: s.ArtifactID,
		StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
	}
}

// ---- 新增 ----

type createItemInput struct {
	Body struct {
		URL       string         `json:"url,omitempty" doc:"影音的網址"`
		Path      string         `json:"path,omitempty" doc:"本機檔案的路徑（尚未實作）"`
		Parts     []int          `json:"parts,omitempty" doc:"Bilibili 要新增的分P；沒有指定時新增全部"`
		Episodes  []string       `json:"episodes,omitempty" doc:"Podcast 要新增的集（guid）"`
		Profile   string         `json:"profile,omitempty" doc:"處理方案；沒有指定時使用預設"`
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
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	switch {
	case in.Body.Path != "":
		// TODO(ING-05、GRD-R7)：本機檔案；路徑必須在 inbox 或 media_roots 之內。
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "新增本機檔案尚未實作。")
	case in.Body.URL == "":
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供要新增的網址（url）。")
	}
	res, err := rt.d.Pipeline.Ingest(ctx, pipeline.IngestRequest{
		URL: in.Body.URL, Parts: in.Body.Parts, Episodes: in.Body.Episodes, Profile: in.Body.Profile,
		Overrides: in.Body.Overrides, Until: in.Body.Until,
	})
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &createItemOutput{Status: http.StatusOK}
	out.Body.Items = make([]IngestedItem, 0, len(res))
	for _, r := range res {
		out.Body.Items = append(out.Body.Items, IngestedItem{Item: toItem(r.Item), AlreadyInLibrary: r.AlreadyInLibrary})
		if !r.AlreadyInLibrary {
			out.Status = http.StatusCreated
		}
	}
	return out, nil
}

// ---- 查詢 ----

type listItemsOutput struct {
	Body struct {
		Items []Item `json:"items"`
	}
}

func (rt *routes) listItems(ctx context.Context, _ *struct{}) (*listItemsOutput, error) {
	out := &listItemsOutput{}
	out.Body.Items = []Item{}
	if rt.d.Pipeline == nil {
		return out, nil
	}
	items, err := rt.d.Pipeline.Items(ctx)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	for _, it := range items {
		out.Body.Items = append(out.Body.Items, toItem(it))
	}
	return out, nil
}

type itemInput struct {
	ID string `path:"id" doc:"影音 ID"`
}

type itemDetailOutput struct{ Body ItemDetail }

func (rt *routes) getItem(ctx context.Context, in *itemInput) (*itemDetailOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	d, err := rt.d.Pipeline.Item(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &itemDetailOutput{Body: toDetail(d)}, nil
}

func toDetail(d pipeline.Detail) ItemDetail {
	out := ItemDetail{Item: toItem(d.Item), Stages: []Stage{}, Sources: []Source{}}
	for _, s := range d.Stages {
		out.Stages = append(out.Stages, toStage(s))
	}
	for _, p := range d.Sources {
		out.Sources = append(out.Sources, Source{Source: p.SourceID, Lang: p.Lang, Kind: p.Kind, State: p.State, Reason: p.Reason})
	}
	return out
}

// ---- 重跑 ----

type rerunInput struct {
	ID   string `path:"id" doc:"影音 ID"`
	Body struct {
		From      string         `json:"from" minLength:"1" doc:"從哪個步驟開始重新處理"`
		Overrides map[string]any `json:"overrides,omitempty" doc:"這次重跑的單次覆寫"`
		Until     string         `json:"until,omitempty" doc:"做到哪個步驟為止（含）；沒有指定時做到最後"`
	}
}

type rerunOutput struct{ Body ItemDetail }

// rerun 從指定步驟重新建立 stage run 與任務（PIPE-R7）：產出新版本，舊版本標記 superseded_by。
func (rt *routes) rerun(ctx context.Context, in *rerunInput) (*rerunOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	d, err := rt.d.Pipeline.Rerun(ctx, in.ID, pipeline.RerunRequest{From: in.Body.From, Overrides: in.Body.Overrides, Until: in.Body.Until})
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
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	q, err := cuesQuery(in.Variant, in.Lang, in.From, in.To)
	if err != nil {
		return nil, err
	}
	tr, err := rt.d.Pipeline.Cues(ctx, in.ID, q)
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
	Section string `path:"section" doc:"設定區段；目前只有 language"`
}

type putSettingsInput struct {
	Section string `path:"section" doc:"設定區段；目前只有 language"`
	// Body 用指標：沒有內容時不被 Huma 擋下，還沒實作的區段不論有沒有內容都回 501。
	Body *map[string]any
}

type settingsOutput struct{ Body map[string]any }

func (rt *routes) getSettings(ctx context.Context, in *settingsInput) (*settingsOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	v, err := rt.d.Pipeline.GetSettings(ctx, in.Section)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &settingsOutput{Body: v}, nil
}

func (rt *routes) putSettings(ctx context.Context, in *putSettingsInput) (*settingsOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	var body map[string]any
	if in.Body != nil {
		body = *in.Body
	}
	if body == nil && in.Section == "language" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供 JSON 物件格式的設定內容。")
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
	)
	switch {
	case errors.Is(err, pipeline.ErrUnrecognizedURL):
		return newError(http.StatusUnprocessableEntity, CodeUnrecognizedURL, err.Error())
	case errors.Is(err, pipeline.ErrChannel):
		return newError(http.StatusUnprocessableEntity, CodeChannel, err.Error())
	case errors.As(err, &inv):
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
	case errors.As(err, &ni):
		return newError(http.StatusNotImplemented, CodeNotImplemented, ni.Error())
	case errors.Is(err, pipeline.ErrNoTranscript):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	rt.d.Log.LogAttrs(ctx, slog.LevelError, "處理請求時發生錯誤", slog.String("error", err.Error()))
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}
