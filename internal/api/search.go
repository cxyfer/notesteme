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
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/search"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// CodeUnsupportedMode：搜尋方式不支援（SRCH-R10、MCP-R6）。
const CodeUnsupportedMode = "unsupported_mode"

// maxModeEcho 是錯誤訊息中回顯 mode 的長度上限（code point），超過時截斷，避免把很長的輸入原樣放進回應。
const maxModeEcho = 32

func (rt *routes) registerSearch(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "search", Method: http.MethodGet, Path: "/api/search",
		Summary: "字幕全文搜尋：結果以字幕句為單位，帶時間戳、句子編號與命中範圍（SRCH-R3～R10）",
	}, auth.ScopeRead, rt.search)
}

type searchInput struct {
	Q    string `query:"q" doc:"要搜尋的文字：照字面比對（不是查詢語法），不分大小寫，去掉前後空白後 1～200 個字"`
	Mode string `query:"mode" doc:"搜尋方式；只支援 fulltext（預設）"`
	Lang string `query:"lang" doc:"只搜尋這個語言的字幕（BCP 47，完全相等）"`
	// Limit 用字串自己解析，錯誤的 path 才會是 limit（SRCH-R5）。
	Limit  string `query:"limit" doc:"單頁筆數，1～100，預設 20"`
	Cursor string `query:"cursor" doc:"上一頁回傳的 next_cursor"`
}

// SearchItem 是命中所屬的影音。
type SearchItem struct {
	ID    string `json:"id"`
	Ref   string `json:"ref" doc:"影音的短代號，例如 yt:abc123xxxxx"`
	Title string `json:"title"`
}

// SearchResult 是一個命中的字幕句（SRCH-R6）。text、before、after 是純文字，不含任何標記。
type SearchResult struct {
	Item       SearchItem    `json:"item"`
	Lang       string        `json:"lang"`
	Variant    string        `json:"variant" doc:"命中的是哪一份字幕"`
	Cue        int           `json:"cue" doc:"字幕句在這份字幕中的編號，從 1 起，與 GET /api/items/{id}/cues 的順序相同"`
	Start      string        `json:"start" example:"00:00:04.000"`
	End        string        `json:"end" example:"00:00:08.000"`
	Text       string        `json:"text"`
	Highlights []search.Span `json:"highlights" doc:"命中範圍在 text 中的位移（UTF-16 code unit，end 不含），依序、不重疊"`
	Before     *string       `json:"before,omitempty" doc:"前一句的純文字；沒有時省略"`
	After      *string       `json:"after,omitempty" doc:"後一句的純文字；沒有時省略"`
}

type searchOutput struct {
	Body struct {
		Results []SearchResult `json:"results"`
		Total   int            `json:"total" doc:"符合條件的字幕句總數"`
		// NextCursor 在最後一頁省略。
		NextCursor string `json:"next_cursor,omitempty" doc:"下一頁的游標；最後一頁沒有這個欄位"`
	}
}

func errUnsupportedMode(mode string) *Error {
	if utf8.RuneCountInString(mode) > maxModeEcho {
		mode = string([]rune(mode)[:maxModeEcho]) + "…"
	}
	if !utf8.ValidString(mode) {
		mode = strings.ToValidUTF8(mode, "�")
	}
	return newError(http.StatusUnprocessableEntity, CodeUnsupportedMode, "不支援這種搜尋方式："+mode+"。")
}

func (rt *routes) search(ctx context.Context, in *searchInput) (*searchOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if in.Mode != "" && in.Mode != "fulltext" {
		return nil, errUnsupportedMode(in.Mode)
	}
	q, err := search.NormalizeQuery(in.Q)
	if err != nil {
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
		e.Errors = []validate.FieldError{{Path: "q", Message: err.Error()}}
		return nil, e
	}
	limit := search.DefaultLimit
	if in.Limit != "" {
		n, err := strconv.Atoi(in.Limit)
		if err != nil || n < 1 || n > search.MaxLimit {
			msg := fmt.Sprintf("limit 必須是 1～%d 的整數。", search.MaxLimit)
			e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
			e.Errors = []validate.FieldError{{Path: "limit", Message: msg}}
			return nil, e
		}
		limit = n
	}
	if len(in.Lang) > maxIDLen {
		msg := "lang 太長。"
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
		e.Errors = []validate.FieldError{{Path: "lang", Message: msg}}
		return nil, e
	}
	folded := search.Fold(q)
	sq := store.SearchQuery{Folded: folded, Lang: in.Lang, Limit: limit}
	fp := searchFingerprint(folded, in.Lang)
	if in.Cursor != "" {
		if sq.After, err = decodeSearchCursor(in.Cursor, rt.d.CursorKey, fp); err != nil {
			return nil, errBadCursor()
		}
	}
	out := &searchOutput{}
	out.Body.Results = []SearchResult{}
	if rt.d.Store == nil {
		return out, nil
	}
	page, err := rt.d.Store.Read().Search(ctx, sq)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	for _, h := range page.Hits {
		out.Body.Results = append(out.Body.Results, SearchResult{
			Item:    SearchItem{ID: h.ItemID, Ref: source.Ref(h.Platform, h.ExternalID, h.Part), Title: h.Title},
			Lang:    h.Lang, Variant: h.Variant, Cue: h.Cue,
			Start: subtitle.FormatDisplayTime(h.StartMS), End: subtitle.FormatDisplayTime(h.EndMS),
			Text: h.Text, Highlights: search.Highlights(h.Text, folded), Before: h.Before, After: h.After,
		})
	}
	out.Body.Total = page.Total
	if page.Next != nil {
		out.Body.NextCursor = encodeSearchCursor(*page.Next, rt.d.CursorKey, fp)
	}
	return out, nil
}

// maxIDLen 是 lang 等識別字參數的長度上限（位元組）。
const maxIDLen = 256

// ---- 游標（格式與失效規則同 GET /api/items，SRCH-R5）----

// searchCursor 是搜尋游標的內容：版本、排序鍵與 MAC（涵蓋版本、排序鍵與查詢條件的指紋）。
type searchCursor struct {
	V  int    `json:"v"`
	T  int64  `json:"t"`
	ID string `json:"id"`
	R  int    `json:"r"`
	L  string `json:"l"`
	C  int    `json:"c"`
	F  string `json:"f"`
}

// searchFingerprint 是查詢條件（摺疊後的查詢與 lang）的指紋；limit 與 cursor 不算在內。
func searchFingerprint(folded, lang string) string {
	b, _ := json.Marshal([]string{"search", folded, lang})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func searchCursorMAC(key []byte, c searchCursor, fp string) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "search\x00%d\x00%d\x00%s\x00%d\x00%s\x00%d\x00%s", c.V, c.T, c.ID, c.R, c.L, c.C, fp)
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func encodeSearchCursor(k store.SearchCursor, key []byte, fp string) string {
	c := searchCursor{V: 1, T: k.CreatedAtMs, ID: k.ItemID, R: k.Translation, L: k.Lang, C: k.Cue}
	c.F = searchCursorMAC(key, c, fp)
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeSearchCursor 嚴格解析搜尋游標（同 decodeItemCursor）：長度上限、標準 base64url、不接受未知欄位與多餘內容、
// 版本為 1、id 是標準形式的 UUID、MAC 與內容及目前的查詢條件相符。任何不符都回傳錯誤，不 panic。
func decodeSearchCursor(s string, key []byte, wantFP string) (*store.SearchCursor, error) {
	if len(s) > maxCursorLen {
		return nil, errors.New("游標太長")
	}
	enc := base64.RawURLEncoding.Strict()
	raw, err := enc.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if enc.EncodeToString(raw) != s {
		return nil, errors.New("游標不是標準的 base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c searchCursor
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("游標有多餘的內容")
	}
	if c.V != 1 || c.T < 0 || c.C < 1 || (c.R != 0 && c.R != 1) {
		return nil, errors.New("游標的版本或位置不正確")
	}
	parsed, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, err
	}
	if c.ID != parsed.String() {
		return nil, errors.New("游標的 id 不是標準的 uuid")
	}
	want := c
	want.F = ""
	if !hmac.Equal([]byte(c.F), []byte(searchCursorMAC(key, want, wantFP))) {
		return nil, errors.New("游標被竄改或查詢條件已經改變")
	}
	return &store.SearchCursor{CreatedAtMs: c.T, ItemID: c.ID, Translation: c.R, Lang: c.L, Cue: c.C}, nil
}
