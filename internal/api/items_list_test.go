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
		// LIB-R19：M4 起接受 read、unread，其他值 422（L-M7）。
		{"?read_state=bogus", 422},
		{"?read_state=READ", 422},
	}
	for _, tt := range tests {
		rec := r.owner("GET", "/api/items"+tt.query, "")
		if rec.Code != tt.code {
			t.Errorf("%s：%d，預期 %d\n%s", tt.query, rec.Code, tt.code, rec.Body)
		}
	}
	for _, ok := range []string{"?read_state=read", "?read_state=unread", "?limit=1", "?limit=200", "?q=" + strings.Repeat("a", 200), "?q=" + strings.Repeat(url.QueryEscape("字"), 200)} {
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

var testCursorKey = []byte("test-cursor-key")

func TestDecodeItemCursorNeverPanics(t *testing.T) {
	fp := "0123456789abcdef"
	good := encodeItemCursor(pipeline.ItemCursor{CreatedAtMs: 42, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}, testCursorKey, fp, false)
	c, err := decodeItemCursor(good, testCursorKey, fp, false)
	if err != nil || c.CreatedAtMs != 42 || c.ID != "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5" {
		t.Fatalf("往返：%+v, %v", c, err)
	}
	if len(good) > maxCursorLen {
		t.Errorf("正常的游標不該超過上限：%d", len(good))
	}
	for _, in := range []string{"", "e30", "bnVsbA", "W10", "InN0ciI", "e30K", "eyJ2IjoxfQ", strings.Repeat("e", 600), "\x00\xff", fmt.Sprintf("%s.%s", good, good)} {
		if _, err := decodeItemCursor(in, testCursorKey, fp, false); err == nil {
			t.Errorf("%q 不該被接受", in)
		}
	}
	if _, err := decodeItemCursor(good, testCursorKey, "ffffffffffffffff", false); err == nil {
		t.Error("指紋不符不該被接受")
	}
	if _, err := decodeItemCursor(good, []byte("another-key"), fp, false); err == nil {
		t.Error("其他金鑰簽的游標不該被接受")
	}
}

// 改掉位置（t 或 id）再重新編碼、保留原本的 f，仍要被拒絕：MAC 涵蓋整個游標內容。
func TestDecodeItemCursorRejectsTamperedPosition(t *testing.T) {
	const fp = "0123456789abcdef"
	good := encodeItemCursor(pipeline.ItemCursor{CreatedAtMs: 42, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}, testCursorKey, fp, false)
	raw, err := base64.RawURLEncoding.DecodeString(good)
	if err != nil {
		t.Fatal(err)
	}
	var c itemCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*itemCursor){
		"改 t":  func(c *itemCursor) { c.T = 7 },
		"改 id": func(c *itemCursor) { c.ID = "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a6" },
	} {
		tc := c
		edit(&tc)
		b, _ := json.Marshal(tc)
		if _, err := decodeItemCursor(base64.RawURLEncoding.EncodeToString(b), testCursorKey, fp, false); err == nil {
			t.Errorf("%s：被竄改的游標不該被接受", name)
		}
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
	// MAC 用標準形式的 id 計算，非標準寫法的 uuid 要在格式檢查就被擋下，而不是只因 MAC 不符。
	mac := cursorMAC(testCursorKey, 1, "", 42, id, fp)
	body := func(id string) string {
		return fmt.Sprintf(`{"v":1,"t":42,"id":%q,"f":%q}`, id, mac)
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
	if _, err := decodeItemCursor(good, testCursorKey, fp, false); err != nil {
		t.Fatalf("前置條件：標準形式要能通過：%v", err)
	}
	spare := enc(withSpareBits(id))
	if _, err := decodeItemCursor(spare, testCursorKey, fp, false); err != nil {
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
		if _, err := decodeItemCursor(tt.cursor, testCursorKey, fp, false); err == nil {
			t.Errorf("%s：不該被接受 %q", tt.name, tt.cursor)
		}
	}
}

// 沒有注入 Pipeline 時，列表也要先驗證參數：不合法回 422，合法才回空列表。
func TestListItemsValidatesParamsWithoutPipeline(t *testing.T) {
	r := newItemsRigAPI(t, func(d *Deps) { d.Pipeline = nil; d.CursorKey = testCursorKey })
	valid := encodeItemCursor(pipeline.ItemCursor{CreatedAtMs: 1, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}, testCursorKey, filterFingerprint(pipeline.ItemQuery{}), false)
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

// 沒有指定 CursorKey 時，同一個 process 的 TCP 與本機 socket handler 共用金鑰：
// 從一個 listener 拿到的游標，在另一個 listener 也能繼續讀下一頁。
func TestListItemsCursorWorksAcrossListeners(t *testing.T) {
	var ad Deps
	r := newItemsRigAPI(t, func(d *Deps) { ad = *d })
	seedLibrary(t, r)
	local := NewLocal(ad)
	first := decode[struct {
		NextCursor string `json:"next_cursor"`
	}](t, r.owner("GET", "/api/items?limit=2", ""))
	if first.NextCursor == "" {
		t.Fatal("前置條件：第一頁要有 next_cursor")
	}
	rec := httptest.NewRecorder()
	local.ServeHTTP(rec, httptest.NewRequest("GET", "/api/items?limit=2&cursor="+first.NextCursor, nil))
	if rec.Code != 200 {
		t.Errorf("本機 socket 拒絕了 TCP 發出的游標：%d %s", rec.Code, rec.Body)
	}
}

// blocked 篩選（DIG-R6 的「等你決定」等）：只看各步驟最新的一次執行；status 必須是 blocked；垃圾桶不列出；
// 可以與 status 篩選組合；游標換了 blocked 的值就失效；格式不對回 422。
func TestListItemsBlockedFilter(t *testing.T) {
	r := newItemsRig(t)
	ctx := context.Background()
	w := r.st.Write()
	snap, err := w.EnsureSnapshot(ctx, "sha", "{}")
	if err != nil {
		t.Fatal(err)
	}
	type run struct{ stage, status, reason string }
	mk := func(ext string, runs ...run) string {
		t.Helper()
		it, _, err := w.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "https://example.test/" + ext})
		if err != nil {
			t.Fatal(err)
		}
		for _, rr := range runs {
			sr, err := w.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: rr.stage, Status: store.StatusPending, ConfigSnapshotID: snap})
			if err != nil {
				t.Fatal(err)
			}
			if rr.status != store.StatusPending {
				if err := w.SetStageState(ctx, sr.ID, store.StageState{Status: rr.status, BlockReason: rr.reason}); err != nil {
					t.Fatal(err)
				}
			}
			r.clk.Advance(time.Millisecond)
		}
		r.clk.Advance(time.Second)
		return toItem(it).Ref
	}
	seg, norm := pipeline.StageSegment, pipeline.StageNormalize
	awaiting := mk("await00001x", run{seg, store.StatusBlocked, "awaiting_user"})
	budget := mk("budget0001x", run{seg, store.StatusBlocked, "budget_blocked"})
	// 之前停住、之後重跑：最新的一次不是 blocked，不算。
	mk("rerun00001x", run{seg, store.StatusBlocked, "awaiting_user"}, run{seg, store.StatusPending, ""})
	// 原因相同但狀態不是 blocked（例如 skipped 留下的原因）不算。
	mk("skipped001x", run{seg, store.StatusSkipped, "awaiting_user"})
	// 兩個步驟各自停在不同原因：兩種篩選都列出。
	both := mk("both000001x", run{norm, store.StatusBlocked, "needs_action"}, run{seg, store.StatusBlocked, "awaiting_user"})
	trashed := mk("trash00001x", run{seg, store.StatusBlocked, "awaiting_user"})
	tr, err := r.st.Read().ItemByKey(ctx, "youtube", "trash00001x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := r.owner("DELETE", "/api/items/"+tr.ID, ""); rec.Code/100 != 2 {
		t.Fatalf("刪除 %s = %d %s", trashed, rec.Code, rec.Body)
	}

	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"?blocked=awaiting_user", []string{both, awaiting}},
		{"?blocked=budget_blocked", []string{budget}},
		{"?blocked=needs_action", []string{both}},
		{"?blocked=needs_action&status=segment:blocked", []string{both}},
		{"?blocked=awaiting_user&status=normalize:pending", []string{awaiting}},
		{"?blocked=no_such_reason", nil},
	} {
		rec, b := r.list(tt.query)
		if rec.Code != 200 {
			t.Errorf("%s：%d %s", tt.query, rec.Code, rec.Body)
			continue
		}
		if got := refsOf(b); !slices.Equal(got, nonNilStr(tt.want)) || b.Total != len(tt.want) {
			t.Errorf("%s：items=%v total=%d，預期 %v", tt.query, got, b.Total, tt.want)
		}
	}

	// 游標：同一個篩選可以接著讀；換成別的 blocked 值或拿掉 blocked 都回 422。
	rec := r.owner("GET", "/api/items?blocked=awaiting_user&limit=1", "")
	cur, _ := decodeBody(t, rec)["next_cursor"].(string)
	if cur == "" {
		t.Fatalf("第一頁 = %s", rec.Body)
	}
	if rec, b := r.list("?blocked=awaiting_user&limit=1&cursor=" + url.QueryEscape(cur)); rec.Code != 200 || !slices.Equal(refsOf(b), []string{awaiting}) {
		t.Errorf("第二頁 = %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"?blocked=budget_blocked&cursor=", "?cursor="} {
		if rec := r.owner("GET", "/api/items"+q+url.QueryEscape(cur), ""); rec.Code != 422 {
			t.Errorf("%s：%d，預期 422", q, rec.Code)
		}
	}

	// 不合格式的值（含 SQL 與萬用字元）一律 422，不會變成不篩選。
	for _, v := range []string{"AWAITING_USER", "awaiting-user", "awaiting_user,needs_action", "%", "_%", "' OR 1=1 --",
		" awaiting_user", "awaiting_user\x00", strings.Repeat("a", 65), "等你決定"} {
		if rec := r.owner("GET", "/api/items?blocked="+url.QueryEscape(v), ""); rec.Code != 422 {
			t.Errorf("blocked=%q：%d，預期 422", v, rec.Code)
		}
	}
	if rec := r.owner("GET", "/api/items?blocked="+strings.Repeat("a", 64), ""); rec.Code != 200 {
		t.Errorf("64 個字元應該可以：%d", rec.Code)
	}
}

type groupedBody struct {
	Items      []ListedItem `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"next_cursor"`
}

// LIB-R4 的 group_by：每部影音帶 group 與 channel_name；分組的游標（版本 2）只能用在同樣的分組，
// 拿去不分組或其他分組的請求回 422；不認識的 group_by 也是 422。
func TestListItemsGroupBy(t *testing.T) {
	r := newItemsRig(t)
	ctx := context.Background()
	refs := seedLibrary(t, r)
	// 讓 pg100 與 other 屬於同一個頻道。
	chID, err := r.st.Write().UpsertChannel(ctx, store.Channel{Platform: "youtube", ExternalID: "UCx", Title: "Fireship"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"pg100xxxxxx", "other000001"} {
		it, err := r.st.Read().ItemByKey(ctx, "youtube", ext, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.st.Write().UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: it.Title, ChannelID: chID}); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	cursor := ""
	for range 10 {
		rec := r.owner("GET", "/api/items?group_by=channel&limit=1"+cursor, "")
		if rec.Code != 200 {
			t.Fatalf("group_by=channel = %d %s", rec.Code, rec.Body)
		}
		b := decode[groupedBody](t, rec)
		if b.Total != 5 || len(b.Items) != 1 {
			t.Fatalf("頁 = %+v", b)
		}
		it := b.Items[0]
		got = append(got, it.Ref+"|"+it.Group+"|"+it.ChannelName)
		if b.NextCursor == "" {
			break
		}
		// 分組的游標拿去不分組、或其他分組：422。
		for _, q := range []string{"", "&group_by=status"} {
			if rec := r.owner("GET", "/api/items?limit=1&cursor="+b.NextCursor+q, ""); rec.Code != 422 {
				t.Errorf("分組的游標用在%q = %d", q, rec.Code)
			}
		}
		cursor = "&cursor=" + b.NextCursor
	}
	pg, ot := refs[0], refs[3]
	want := []string{ot + "|" + chID + "|Fireship", pg + "|" + chID + "|Fireship", refs[4] + "||", refs[2] + "||", refs[1] + "||"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("依頻道分組 = %v，預期 %v", got, want)
	}
	// 不分組的游標拿去分組的請求也是 422；不分組時沒有 group 欄位，但有頻道名稱。
	rec := r.owner("GET", "/api/items?limit=1", "")
	b := decode[groupedBody](t, rec)
	if b.Items[0].Group != "" || b.Items[0].ChannelName != "" || b.NextCursor == "" {
		t.Fatalf("不分組 = %+v", b)
	}
	if rec := r.owner("GET", "/api/items?limit=1&group_by=channel&cursor="+b.NextCursor, ""); rec.Code != 422 {
		t.Errorf("不分組的游標用在分組 = %d", rec.Code)
	}
	if rec := r.owner("GET", "/api/items?group_by=title", ""); rec.Code != 422 {
		t.Errorf("不認識的 group_by = %d %s", rec.Code, rec.Body)
	}
	b = decode[groupedBody](t, r.owner("GET", "/api/items?limit=5", ""))
	names := 0
	for _, it := range b.Items {
		if it.ChannelName == "Fireship" {
			names++
		}
	}
	if names != 2 {
		t.Errorf("列表應帶頻道名稱：%+v", b.Items)
	}
}

// 版本 2 的游標：分組鍵在 MAC 內，改掉分組鍵就被拒絕；版本 1 不能帶分組鍵。
func TestGroupedCursorMACCoversGroup(t *testing.T) {
	const fp = "0123456789abcdef"
	c := pipeline.ItemCursor{Group: "0Fireship\x1fch", CreatedAtMs: 42, ID: "01a1dc0b-d8dd-78d5-8f34-3a708ef8a2a5"}
	good := encodeItemCursor(c, testCursorKey, fp, true)
	if got, err := decodeItemCursor(good, testCursorKey, fp, true); err != nil || *got != c {
		t.Fatalf("往返 = %+v, %v", got, err)
	}
	if _, err := decodeItemCursor(good, testCursorKey, fp, false); err == nil {
		t.Error("版本 2 的游標不能用在不分組的列表")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["g"] = "0Alpha\x1fch"
	b, _ := json.Marshal(m)
	if _, err := decodeItemCursor(base64.RawURLEncoding.EncodeToString(b), testCursorKey, fp, true); err == nil {
		t.Error("改掉分組鍵的游標不該被接受")
	}
	m["v"], m["g"] = 1.0, "x"
	b, _ = json.Marshal(m)
	if _, err := decodeItemCursor(base64.RawURLEncoding.EncodeToString(b), testCursorKey, fp, false); err == nil {
		t.Error("版本 1 不能帶分組鍵")
	}
	long := encodeItemCursor(pipeline.ItemCursor{Group: strings.Repeat("g", maxCursorGroupLen+1), CreatedAtMs: 1, ID: c.ID}, testCursorKey, fp, true)
	if _, err := decodeItemCursor(long, testCursorKey, fp, true); err == nil {
		t.Error("分組鍵太長不該被接受")
	}
}
