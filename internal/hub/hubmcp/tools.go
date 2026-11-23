package hubmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- search_versions（HREP-R16 的篩選） ----

// maxSearchLimit 是 search_versions 的 limit 上限與預設值（HMCP-R3）。
const maxSearchLimit = 50

type searchArgs struct {
	Platform          string `json:"platform,omitempty" jsonschema:"youtube、bilibili 或 podcast"`
	Channel           string `json:"channel,omitempty" jsonschema:"頻道的 external_id"`
	Video             string `json:"video,omitempty" jsonschema:"<platform>:<external_id>，Bilibili 的分P加 /p<n>"`
	Lang              string `json:"lang,omitempty" jsonschema:"語言（BCP 47）"`
	Kind              string `json:"kind,omitempty" jsonschema:"transcript、chapters、note 或 frames"`
	Variant           string `json:"variant,omitempty" jsonschema:"source、normalized、segmented、corrected、translated 或 edited"`
	TranscribeModel   string `json:"transcribe_model,omitempty" jsonschema:"轉錄模型，例如 groq/whisper-large-v3"`
	CorrectModel      string `json:"correct_model,omitempty" jsonschema:"校正模型"`
	TranslateModel    string `json:"translate_model,omitempty" jsonschema:"翻譯模型"`
	ExcludeUnverified bool   `json:"exclude_unverified,omitempty" jsonschema:"true 時排除 model_verified 為 false 的版本"`
	Sort              string `json:"sort,omitempty" jsonschema:"score（預設）或 new"`
	Limit             int    `json:"limit,omitempty" jsonschema:"單頁筆數，1～50，預設 50"`
	Cursor            string `json:"cursor,omitempty" jsonschema:"上一頁回傳的 next_cursor"`
}

func addSearchVersions(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_versions",
		Description: "搜尋 Hub 上看得到的字幕、章節與筆記版本，可依平台、頻道、影片、語言、種類、變體與模型篩選，行為與 GET /api/v1/versions 相同；" +
			"還有下一頁時回傳 next_cursor。需要 read 權限。" + UntrustedNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, any, error) {
		return searchVersions(ctx, d, a), nil, nil
	})
}

func searchVersions(ctx context.Context, d *Deps, a searchArgs) *mcp.CallToolResult {
	if r := checkStrings(
		field{"platform", a.Platform, maxParamLen}, field{"channel", a.Channel, maxParamLen * 4},
		field{"video", a.Video, maxParamLen * 4}, field{"lang", a.Lang, maxParamLen},
		field{"kind", a.Kind, maxParamLen}, field{"variant", a.Variant, maxParamLen},
		field{"transcribe_model", a.TranscribeModel, maxParamLen * 4}, field{"correct_model", a.CorrectModel, maxParamLen * 4},
		field{"translate_model", a.TranslateModel, maxParamLen * 4}, field{"sort", a.Sort, maxParamLen},
		field{"cursor", a.Cursor, maxCursor}); r != nil {
		return r
	}
	limit := a.Limit
	switch {
	case limit == 0:
		limit = maxSearchLimit
	case limit < 1 || limit > maxSearchLimit:
		return invalid("limit 必須是 1～" + strconv.Itoa(maxSearchLimit) + "。")
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	set(q, "platform", a.Platform)
	set(q, "channel", a.Channel)
	set(q, "video", a.Video)
	set(q, "lang", a.Lang)
	set(q, "kind", a.Kind)
	set(q, "variant", a.Variant)
	set(q, "transcribe_model", a.TranscribeModel)
	set(q, "correct_model", a.CorrectModel)
	set(q, "translate_model", a.TranslateModel)
	set(q, "sort", a.Sort)
	set(q, "cursor", a.Cursor)
	if a.ExcludeUnverified {
		q.Set("exclude_unverified", "true")
	}
	body, r := get(ctx, d, "/api/v1/versions", q)
	if r != nil {
		return r
	}
	return jsonResult(json.RawMessage(body))
}

// ---- get_version（HREP-R17 的欄位） ----

type versionArgs struct {
	ID string `json:"id" jsonschema:"版本 ID"`
}

func addGetVersion(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_version",
		Description: "版本的詳情：影片、種類、變體、語言、模型、授權、分數、上傳者與來源紀錄（衍生關係），行為與 GET /api/v1/versions/{id} 相同。" +
			"需要 read 權限。" + UntrustedNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a versionArgs) (*mcp.CallToolResult, any, error) {
		if r := checkVersionID("id", a.ID); r != nil {
			return r, nil, nil
		}
		body, r := get(ctx, d, versionPath(a.ID, ""), nil)
		if r != nil {
			return r, nil, nil
		}
		return jsonResult(json.RawMessage(body)), nil, nil
	})
}

func checkVersionID(name, id string) *mcp.CallToolResult {
	if r := required(name, id); r != nil {
		return r
	}
	return checkStrings(field{name, id, maxIDLen})
}

// versionPath 組出 /api/v1/versions/<id><suffix>；id 是不可信的輸入，要 escape（不能用 ../ 跳到其他路由）。
func versionPath(id, suffix string) string {
	return "/api/v1/versions/" + url.PathEscape(id) + suffix
}

// ---- list_video_versions（HREP-R17 的影片頁） ----

type videoArgs struct {
	Platform   string `json:"platform" jsonschema:"youtube、bilibili 或 podcast"`
	ExternalID string `json:"external_id" jsonschema:"平台上的影片 ID"`
	Part       int    `json:"part,omitempty" jsonschema:"Bilibili 的分P（1 起算）；省略時是沒有分P的影片"`
}

func addListVideoVersions(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_video_versions",
		Description: "列出一部影片在 Hub 上看得到的版本，依種類、變體與語言分組，組內依分數由高到低，行為與 GET /api/v1/videos/{platform}/{external_id} 相同。" +
			"需要 read 權限。" + UntrustedNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a videoArgs) (*mcp.CallToolResult, any, error) {
		return listVideoVersions(ctx, d, a), nil, nil
	})
}

func listVideoVersions(ctx context.Context, d *Deps, a videoArgs) *mcp.CallToolResult {
	if r := required("platform", a.Platform); r != nil {
		return r
	}
	if r := required("external_id", a.ExternalID); r != nil {
		return r
	}
	if r := checkStrings(field{"platform", a.Platform, maxParamLen}, field{"external_id", a.ExternalID, maxParamLen * 4}); r != nil {
		return r
	}
	var q url.Values
	if a.Part != 0 {
		if a.Part < 1 || a.Part > maxPart {
			return invalid("part 必須是 1～" + strconv.Itoa(maxPart) + "。")
		}
		q = url.Values{"part": {strconv.Itoa(a.Part)}}
	}
	path := "/api/v1/videos/" + url.PathEscape(a.Platform) + "/" + url.PathEscape(a.ExternalID)
	body, r := get(ctx, d, path, q)
	if r != nil {
		return r
	}
	return jsonResult(json.RawMessage(body))
}

// ---- get_transcript ----

func addGetTranscript(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_transcript",
		Description: "讀取字幕版本的內容：format 為 cues-json（預設，cues 陣列）、srt、vtt 或 txt（text 欄位）；from、to 為時間碼（HH:MM:SS.mmm），" +
			"只回傳開始時間在 [from, to) 內的字幕。結果太大時截到最後一個完整的字幕，回傳 truncated: true 與 next_from，用 next_from 當 from 繼續取。" +
			"需要 download 權限。" + UntrustedNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a transcriptArgs) (*mcp.CallToolResult, any, error) {
		return getTranscript(ctx, d, a), nil, nil
	})
}

func getTranscript(ctx context.Context, d *Deps, a transcriptArgs) *mcp.CallToolResult {
	if r := checkVersionID("version", a.Version); r != nil {
		return r
	}
	if r := checkStrings(field{"format", a.Format, maxParamLen}, field{"from", a.From, maxTimeLen}, field{"to", a.To, maxTimeLen}); r != nil {
		return r
	}
	req, r := parseTranscriptArgs(a)
	if r != nil {
		return r
	}
	// 一律取 cues JSON（需要 download，與 tool 的權限相同），在這裡篩選、渲染與截斷。
	resp, err := d.Backend.Get(ctx, versionPath(a.Version, "/download"), url.Values{"format": {formatCuesJSON}})
	if err != nil {
		return backendErrorResult(ctx, err)
	}
	if resp.Status == http.StatusUnprocessableEntity {
		// download 的 422 只有 format 不符：版本不是字幕。
		return invalid("get_transcript 只能讀取字幕版本（kind: transcript）。")
	}
	if resp.Status/100 != 2 {
		return apiErrorResult(resp)
	}
	out, err := buildTranscript(bytes.NewReader(resp.Body), a.Version, req, d.MaxResultBytes)
	if err != nil {
		return internalError()
	}
	return jsonResult(out)
}
