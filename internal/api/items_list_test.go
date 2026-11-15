package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// seedLibrary 建立 5 部影音（created_at 每部差 1 秒），回傳它們的 ref，依建立順序。
func seedLibrary(t *testing.T, r *itemsRig) []string {
	t.Helper()
	ctx := context.Background()
	snap, err := r.st.Write().EnsureSnapshot(ctx, "sha", "{}")
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct {
		platform, ext, title string
		normalize            string
	}{
		{"youtube", "pg100xxxxxx", "PostgreSQL in 100 Seconds", store.StatusDone},
		{"youtube", "agile43xxxx", "我们都只是在假装着做Agile", store.StatusPending},
		{"bilibili", "BV1xx411c7mD", "", ""},
		{"youtube", "other000001", "Other talk", store.StatusDone},
		{"podcast", "ep1", "A podcast episode", ""},
	}
	var refs []string
	for _, s := range specs {
		part := 0
		if s.platform == "bilibili" {
			part = 1
		}
		it, _, err := r.st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: s.platform, ExternalID: s.ext, Part: part, URL: "https://example.test/" + s.ext})
		if err != nil {
			t.Fatal(err)
		}
		if s.title != "" {
			if err := r.st.Write().UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: s.title}); err != nil {
				t.Fatal(err)
			}
		}
		if s.normalize != "" {
			run, err := r.st.Write().InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: pipeline.StageNormalize, Status: store.StatusPending, ConfigSnapshotID: snap})
			if err != nil {
				t.Fatal(err)
			}
			if s.normalize != store.StatusPending {
				if err := r.st.Write().SetStageState(ctx, run.ID, store.StageState{Status: s.normalize}); err != nil {
					t.Fatal(err)
				}
			}
		}
		refs = append(refs, toItem(it).Ref)
		r.clk.Advance(time.Second)
	}
	return refs
}

type listBody struct {
	Items []Item `json:"items"`
	Total int    `json:"total"`
}

func (r *itemsRig) list(query string) (*httptest.ResponseRecorder, listBody) {
	r.t.Helper()
	rec := r.owner("GET", "/api/items"+query, "")
	var b listBody
	if rec.Code == 200 {
		b = decode[listBody](r.t, rec)
	}
	return rec, b
}

func refsOf(b listBody) []string {
	out := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		out = append(out, it.Ref)
	}
	return out
}

func TestListItemsFilterParams(t *testing.T) {
	r := newItemsRig(t)
	refs := seedLibrary(t, r)
	pg, ag, bi, ot, po := refs[0], refs[1], refs[2], refs[3], refs[4]
	tests := []struct {
		query string
		want  []string // 新到舊
	}{
		{"", []string{po, ot, bi, ag, pg}},
		{"?platform=bilibili", []string{bi}},
		{"?platform=youtube&status=normalize:done", []string{ot, pg}},
		{"?status=normalize:done", []string{ot, pg}},
		{"?status=normalize:done&status=normalize:pending", []string{po, ot, bi, ag, pg}}, // 沒有 run 的步驟視為 pending
		{"?status=normalize:pending", []string{po, bi, ag}},
		{"?status=normalize:done&status=metadata:done", nil},
		{"?q=postgresql", []string{pg}},
		{"?q=POSTGRESQL", []string{pg}},
		{"?q=Agile", []string{ag}},
		{"?q=" + url.QueryEscape("假装"), []string{ag}},
		{"?q=" + url.QueryEscape("  postgresql  "), []string{pg}},
		{"?q=%25", nil},
		{"?q=" + url.QueryEscape("'"), nil},
		{"?timing=interpolated", nil},
		{"?error_class=permanent", nil},
		{"?lang=en", nil},
		{"?published_from=2026-01-01T00:00:00Z&published_to=2026-12-31T00:00:00Z", nil},
		{"?duration_min=0&duration_max=10", nil},
	}
	for _, tt := range tests {
		rec, b := r.list(tt.query)
		if rec.Code != 200 {
			t.Errorf("%s：%d %s", tt.query, rec.Code, rec.Body)
			continue
		}
		if got := refsOf(b); !slices.Equal(got, nonNilStr(tt.want)) || b.Total != len(tt.want) {
			t.Errorf("%s：items=%v total=%d，預期 %v", tt.query, got, b.Total, tt.want)
		}
	}
}

func nonNilStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestListItemsInvalidParams(t *testing.T) {
	r := newItemsRig(t)
	seedLibrary(t, r)
	tests := []struct {
		query string
		code  int
	}{
		{"?platform=vimeo", 422},
		{"?status=bogus:done", 422},
		{"?status=normalize:bogus", 422},
		{"?status=normalize", 422},
		{"?status=:done", 422},
		{"?timing=bogus", 422},
		{"?limit=0", 422},
		{"?limit=201", 422},
		{"?limit=abc", 422},
		{"?duration_min=-1", 422},
		{"?duration_max=-1", 422},
		{"?published_from=yesterday", 422},
		{"?published_to=2026-01-01", 422},
		{"?q=" + strings.Repeat("a", 201), 422},
		{"?q=" + strings.Repeat(url.QueryEscape("字"), 201), 422},
		{"?read_state=unread", 501},
	}
	for _, tt := range tests {
		rec := r.owner("GET", "/api/items"+tt.query, "")
		if rec.Code != tt.code {
			t.Errorf("%s：%d，預期 %d\n%s", tt.query, rec.Code, tt.code, rec.Body)
		}
	}
	for _, ok := range []string{"?limit=1", "?limit=200", "?q=" + strings.Repeat("a", 200), "?q=" + strings.Repeat(url.QueryEscape("字"), 200)} {
		if rec := r.owner("GET", "/api/items"+ok, ""); rec.Code != 200 {
			t.Errorf("%s 應該可以：%d %s", ok[:min(len(ok), 30)], rec.Code, rec.Body)
		}
	}
}

// 分頁：limit 生效、游標走完全部、最後一頁省略 next_cursor（不是 null）、total 不受分頁影響。
func TestListItemsCursorPaging(t *testing.T) {
	r := newItemsRig(t)
	refs := seedLibrary(t, r)
	var got []string
	cursor := ""
	for page := 0; ; page++ {
		if page > 5 {
			t.Fatal("分頁沒有結束")
		}
		q := "?limit=2"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := r.owner("GET", "/api/items"+q, "")
		if rec.Code != 200 {
			t.Fatalf("第 %d 頁：%d %s", page, rec.Code, rec.Body)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		b := decode[listBody](t, rec)
		if len(b.Items) > 2 || b.Total != 5 {
			t.Errorf("第 %d 頁：%d 筆 total=%d", page, len(b.Items), b.Total)
		}
		got = append(got, refsOf(b)...)
		next, has := raw["next_cursor"]
		if !has {
			if len(b.Items) == 0 {
				t.Error("最後一頁不該是空的")
			}
			break
		}
		if string(next) == "null" {
			t.Fatal("next_cursor 不得是 null")
		}
		if err := json.Unmarshal(next, &cursor); err != nil || cursor == "" {
			t.Fatalf("next_cursor = %s", next)
		}
		if strings.ContainsAny(cursor, "=+/") {
			t.Errorf("游標應該是不補 = 的 base64url：%q", cursor)
		}
	}
	want := slices.Clone(refs)
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Errorf("分頁結果 = %v，預期 %v", got, want)
	}
	// 剛好放得下時沒有 next_cursor。
	rec := r.owner("GET", "/api/items?limit=5", "")
	if strings.Contains(rec.Body.String(), "next_cursor") {
		t.Errorf("limit 等於總數時不該有 next_cursor：%s", rec.Body)
	}
}

func TestListItemsBadCursor(t *testing.T) {
	r := newItemsRig(t)
	seedLibrary(t, r)
	first := decode[struct {
		NextCursor string `json:"next_cursor"`
	}](t, r.owner("GET", "/api/items?limit=2", ""))
	if first.NextCursor == "" {
		t.Fatal("前置條件：第一頁要有 next_cursor")
	}
	good := first.NextCursor
	if rec := r.owner("GET", "/api/items?limit=2&cursor="+good, ""); rec.Code != 200 {
		t.Fatalf("前置條件：有效的游標應該可用：%d %s", rec.Code, rec.Body)
	}
	// 游標改了其中一個字元：不是 base64、不是 JSON，或是指紋不符。
	tampered := []byte(good)
	tampered[len(tampered)/2] ^= 0x01
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	tests := []struct{ name, query string }{
		{"被竄改", "?limit=2&cursor=" + string(tampered)},
		{"改了篩選條件", "?limit=2&platform=youtube&cursor=" + good},
		{"改了 q", "?limit=2&q=a&cursor=" + good},
		{"改了 status", "?limit=2&status=normalize:done&cursor=" + good},
		{"超過長度上限", "?limit=2&cursor=" + strings.Repeat("A", 513)},
		{"不是 base64url", "?limit=2&cursor=%21%21%21"},
		{"補了 =", "?limit=2&cursor=" + good + "%3D%3D"},
		{"不是 JSON", "?limit=2&cursor=" + b64("hello")},
		{"未知欄位", "?limit=2&cursor=" + b64(`{"v":1,"t":1,"id":"01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5","f":"0000000000000000","x":1}`)},
		{"id 不是 uuid", "?limit=2&cursor=" + b64(`{"v":1,"t":1,"id":"nope","f":"0000000000000000"}`)},
		{"版本不對", "?limit=2&cursor=" + b64(`{"v":2,"t":1,"id":"01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5","f":"0000000000000000"}`)},
		{"JSON 後面還有內容", "?limit=2&cursor=" + b64(`{"v":1,"t":1,"id":"01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5","f":"x"} {}`)},
	}
	for _, tt := range tests {
		rec := r.owner("GET", "/api/items"+tt.query, "")
		if rec.Code != 422 {
			t.Errorf("%s：%d，預期 422\n%s", tt.name, rec.Code, rec.Body)
			continue
		}
		e := decode[Error](t, rec)
		if e.Code != CodeInvalidInput || e.Message != "游標不正確或已失效，請從第一頁重新讀取。" {
			t.Errorf("%s：%+v", tt.name, e)
		}
	}
}

// 游標與 limit 無關：換 limit 繼續讀不算改了篩選條件。
func TestListItemsCursorSurvivesLimitChange(t *testing.T) {
	r := newItemsRig(t)
	seedLibrary(t, r)
	first := decode[struct {
		NextCursor string `json:"next_cursor"`
	}](t, r.owner("GET", "/api/items?limit=2", ""))
	rec := r.owner("GET", "/api/items?limit=1&cursor="+first.NextCursor, "")
	if rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestDecodeItemCursorNeverPanics(t *testing.T) {
	fp := "0123456789abcdef"
	good := encodeItemCursor(pipeline.ItemCursor{CreatedAtMs: 42, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}, fp)
	c, err := decodeItemCursor(good, fp)
	if err != nil || c.CreatedAtMs != 42 || c.ID != "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5" {
		t.Fatalf("往返：%+v, %v", c, err)
	}
	if len(good) > maxCursorLen {
		t.Errorf("正常的游標不該超過上限：%d", len(good))
	}
	for _, in := range []string{"", "e30", "bnVsbA", "W10", "InN0ciI", "e30K", "eyJ2IjoxfQ", strings.Repeat("e", 600), "\x00\xff", fmt.Sprintf("%s.%s", good, good)} {
		if _, err := decodeItemCursor(in, fp); err == nil {
			t.Errorf("%q 不該被接受", in)
		}
	}
	if _, err := decodeItemCursor(good, "ffffffffffffffff"); err == nil {
		t.Error("指紋不符不該被接受")
	}
}

func TestFilterFingerprintIgnoresLimitAndOrder(t *testing.T) {
	a := pipeline.ItemQuery{Platform: "youtube", Limit: 10, Status: map[string][]string{"normalize": {"done", "pending"}, "metadata": {"done"}}}
	b := pipeline.ItemQuery{Platform: "youtube", Limit: 99, Status: map[string][]string{"metadata": {"done"}, "normalize": {"pending", "done"}}}
	if filterFingerprint(a) != filterFingerprint(b) {
		t.Error("limit 與 status 值的順序不該影響指紋")
	}
	c := a
	c.Q = "x"
	if filterFingerprint(a) == filterFingerprint(c) {
		t.Error("q 不同，指紋應該不同")
	}
	if len(filterFingerprint(a)) != 16 {
		t.Errorf("指紋應該是 16 個 hex 字元：%q", filterFingerprint(a))
	}
}

// 游標只接受唯一的標準形式：同一個位置不能有多種字串表示（base64 的換行、尾端位元，uuid 的各種寫法）。
func TestDecodeItemCursorRejectsNonCanonicalForms(t *testing.T) {
	const fp = "0123456789abcdef"
	const id = "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	body := func(id string) string {
		return fmt.Sprintf(`{"v":1,"t":42,"id":%q,"f":%q}`, id, fp)
	}
	// 補上尾端空白，讓位元組數除以 3 餘 1，base64 的最後一個字元才有多餘的位元。
	withSpareBits := func(id string) string {
		s := body(id)
		for len(s)%3 != 1 {
			s += " "
		}
		return s
	}
	flipLastChar := func(s string) string {
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
		i := strings.IndexByte(alphabet, s[len(s)-1])
		return s[:len(s)-1] + string(alphabet[i^1])
	}
	good := enc(body(id))
	if _, err := decodeItemCursor(good, fp); err != nil {
		t.Fatalf("前置條件：標準形式要能通過：%v", err)
	}
	spare := enc(withSpareBits(id))
	if _, err := decodeItemCursor(spare, fp); err != nil {
		t.Fatalf("前置條件：尾端空白的游標要能通過：%v", err)
	}
	tests := []struct{ name, cursor string }{
		{"含換行", good[:10] + "\n" + good[10:]},
		{"含 CRLF", good[:10] + "\r\n" + good[10:]},
		{"尾端位元不為零", flipLastChar(spare)},
		{"uuid 用大括號", enc(body("{" + id + "}"))},
		{"uuid 用 urn:uuid:", enc(body("urn:uuid:" + id))},
		{"uuid 沒有連字號", enc(body(strings.ReplaceAll(id, "-", "")))},
		{"uuid 大寫", enc(body(strings.ToUpper(id)))},
	}
	for _, tt := range tests {
		if _, err := decodeItemCursor(tt.cursor, fp); err == nil {
			t.Errorf("%s：不該被接受 %q", tt.name, tt.cursor)
		}
	}
}

// 沒有注入 Pipeline 時，列表也要先驗證參數：不合法回 422，合法才回空列表。
func TestListItemsValidatesParamsWithoutPipeline(t *testing.T) {
	r := newItemsRigAPI(t, func(d *Deps) { d.Pipeline = nil })
	valid := encodeItemCursor(pipeline.ItemCursor{CreatedAtMs: 1, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}, filterFingerprint(pipeline.ItemQuery{}))
	for _, q := range []string{
		"?platform=vimeo", "?status=bogus:done", "?status=normalize:bogus", "?timing=bogus",
		"?duration_min=-1", "?q=" + strings.Repeat("a", 201), "?cursor=nope", "?platform=youtube&cursor=" + valid,
	} {
		if rec := r.owner("GET", "/api/items"+q, ""); rec.Code != 422 {
			t.Errorf("%s：%d，預期 422\n%s", q[:min(len(q), 30)], rec.Code, rec.Body)
		}
	}
	for _, q := range []string{"", "?platform=youtube&status=normalize:done", "?cursor=" + valid} {
		rec, b := r.list(q)
		if rec.Code != 200 || len(b.Items) != 0 || b.Total != 0 {
			t.Errorf("%s：%d %s，預期空列表", q, rec.Code, rec.Body)
		}
	}
}

// status 的值含逗號時，提示多個條件要重複 status 參數。
func TestListItemsStatusCommaHint(t *testing.T) {
	r := newItemsRig(t)
	seedLibrary(t, r)
	for _, v := range []string{"a:done,b:done", "normalize:done,pending", "normalize:done,normalize:pending"} {
		rec := r.owner("GET", "/api/items?status="+url.QueryEscape(v), "")
		if rec.Code != 422 {
			t.Errorf("%s：%d，預期 422", v, rec.Code)
			continue
		}
		if e := decode[Error](t, rec); !strings.Contains(e.Message, "重複 status 參數") {
			t.Errorf("%s：訊息要提示重複 status 參數：%q", v, e.Message)
		}
	}
}

func TestListItemsRequiresAuth(t *testing.T) {
	r := newItemsRig(t)
	if rec := r.do("GET", "/api/items?limit=1", "", nil); rec.Code != 401 {
		t.Errorf("沒有登入應回 401：%d", rec.Code)
	}
}
