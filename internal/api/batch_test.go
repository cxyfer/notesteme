package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/diskusage"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

type batchRig struct {
	*itemsRig
	auth *auth.Service
	dir  string
}

func newBatchRig(t *testing.T, apiOpt ...func(*Deps)) *batchRig {
	t.Helper()
	br := &batchRig{}
	br.itemsRig = newItemsRigAPI(t, func(d *Deps) {
		br.auth = d.Auth
		for _, o := range apiOpt {
			o(d)
		}
	}, func(d *pipeline.Deps) { br.dir = d.DataDir })
	return br
}

// insert 直接在 store 建立一部影音。
func (r *batchRig) insert(platform, ext string, part int) store.Item {
	r.t.Helper()
	it, _, err := r.st.Write().InsertItemIfAbsent(context.Background(), store.NewItem{Platform: platform, ExternalID: ext, Part: part, URL: "https://example.test/" + ext})
	if err != nil {
		r.t.Fatal(err)
	}
	return it
}

func (r *batchRig) token(scopes ...string) string {
	r.t.Helper()
	tok, err := r.auth.CreateToken(context.Background(), strings.Join(scopes, "-"), scopes, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return tok.Token
}

func (r *batchRig) batch(body string) (*BatchResult, *Error, int) {
	r.t.Helper()
	rec := r.owner("POST", "/api/items/batch", body)
	if rec.Code != http.StatusOK {
		e := errBody(r.t, rec)
		return nil, &e, rec.Code
	}
	res := decode[BatchResult](r.t, rec)
	return &res, nil, rec.Code
}

// counts 讀出會因寫入而改變的資料表筆數（dry_run 不能改變它們，LIB-R14）。
func (r *batchRig) counts() map[string]int {
	r.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, "notesteme.db")+"?mode=ro")
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	out := map[string]int{}
	for _, tbl := range []string{"events", "stage_runs", "tasks", "cost_ledger", "settings", "config_snapshots"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			r.t.Fatal(err)
		}
		out[tbl] = n
	}
	var sig string
	if err := db.QueryRow("SELECT group_concat(id || read_state || COALESCE(deleted_at, '') || COALESCE(profile_id, ''), ',') FROM items").Scan(&sig); err != nil {
		r.t.Fatal(err)
	}
	out["items:"+sig] = 1
	return out
}

// LIB-R13：驗證失敗時回 422 invalid_request，errors[].path 是欄位名稱，什麼都不執行。
func TestBatchValidation(t *testing.T) {
	r := newBatchRig(t)
	it := r.insert("youtube", "aaaaaaaaaaa", 0)
	long := strings.Repeat("x", 100)
	many := make([]string, 0, 501)
	for i := range 501 {
		ext := fmt.Sprintf("v%010d", i)
		r.insert("youtube", ext, 0)
		many = append(many, `"yt:`+ext+`"`)
	}
	dup := make([]string, 0, 600)
	for range 600 {
		dup = append(dup, `"`+it.ID+`"`)
	}
	tests := []struct {
		name, body, path, msg string
	}{
		{"空的 ids", `{"action":"mark_read","ids":[]}`, "ids", "至少要選一部影音。"},
		{"沒有 ids", `{"action":"mark_read"}`, "ids", "至少要選一部影音。"},
		{"不支援的動作", `{"action":"pause","ids":["x"]}`, "action", "不支援的批次動作 pause。"},
		{"沒有動作", `{"ids":["x"]}`, "action", "不支援的批次動作 。"},
		{"動作太長", `{"action":"` + long + `","ids":["x"]}`, "action", "不支援的批次動作 " + strings.Repeat("x", 64) + "…。"},
		{"from 不是步驟", `{"action":"rerun","ids":["x"],"from":"proofread"}`, "from", "from 必須是 pipeline 的步驟。"},
		{"沒有 from", `{"action":"rerun","ids":["x"]}`, "from", "from 必須是 pipeline 的步驟。"},
		{"change_profile 沒有 from", `{"action":"change_profile","ids":["x"],"profile":"default"}`, "from", "from 必須是 pipeline 的步驟。"},
		{"處理方案不存在", `{"action":"change_profile","ids":["x"],"profile":"nope","from":"correct"}`, "profile", "找不到處理方案 nope。"},
		{"沒有處理方案", `{"action":"change_profile","ids":["x"],"from":"correct"}`, "profile", "找不到處理方案 。"},
		{"超過 500 部", `{"action":"mark_read","ids":[` + strings.Join(many, ",") + `]}`, "ids", "一次最多選 500 部影音。"},
	}
	before := r.counts()
	for _, tt := range tests {
		_, e, code := r.batch(tt.body)
		if code != 422 || e.Code != CodeInvalidInput || len(e.Errors) != 1 || e.Errors[0].Path != tt.path || e.Errors[0].Message != tt.msg {
			t.Errorf("%s：%d %+v", tt.name, code, e)
		}
	}
	if after := r.counts(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("驗證失敗不能執行任何東西：\n%v\n%v", before, after)
	}
	// 重複的 600 個去掉重複之後是 1 部：不超過上限。
	res, e, code := r.batch(`{"action":"mark_read","dry_run":true,"ids":[` + strings.Join(dup, ",") + `]}`)
	if code != 200 || res.Count != 1 || len(res.Items) != 1 {
		t.Errorf("重複的 ids：%d %+v %+v", code, res, e)
	}
	// 不認識的欄位與太大的本體由 Huma 拒絕。
	if rec := r.owner("POST", "/api/items/batch", `{"action":"mark_read","ids":["x"],"nope":1}`); rec.Code != 422 {
		t.Errorf("不認識的欄位：%d", rec.Code)
	}
	if rec := r.owner("POST", "/api/items/batch", `{"action":"mark_read","ids":["`+strings.Repeat("a", batchMaxBodyBytes)+`"]}`); rec.Code != 413 {
		t.Errorf("太大的本體：%d", rec.Code)
	}
}

// LIB-R16：scope 依動作，在讀取影音之前判斷；缺少時 403 與 required_scope，不執行任何影音。
func TestBatchScopes(t *testing.T) {
	r := newBatchRig(t)
	it := r.insert("youtube", "aaaaaaaaaaa", 0)
	read, ingest, admin := r.token(auth.ScopeRead), r.token(auth.ScopeIngest), r.token(auth.ScopeAdmin)
	ids := `"ids":["` + it.ID + `"]`
	tests := []struct {
		tok, body string
		code      int
		scope     string
	}{
		{ingest, `{"action":"delete","dry_run":true,` + ids + `}`, 403, auth.ScopeAdmin},
		{read, `{"action":"delete",` + ids + `}`, 403, auth.ScopeAdmin},
		{read, `{"action":"mark_read",` + ids + `}`, 403, auth.ScopeIngest},
		{read, `{"action":"mark_unread",` + ids + `}`, 403, auth.ScopeIngest},
		{read, `{"action":"rerun","from":"correct","dry_run":true,` + ids + `}`, 403, auth.ScopeIngest},
		{admin, `{"action":"change_profile","from":"correct","profile":"nope",` + ids + `}`, 403, auth.ScopeIngest},
		{admin, `{"action":"export_bundle","dry_run":true,` + ids + `}`, 403, auth.ScopeRead},
		// 有 scope 的：只有 ingest（沒有 read）也能標為已讀；read 可以 dry_run 匯出。
		{ingest, `{"action":"mark_read",` + ids + `}`, 200, ""},
		{read, `{"action":"export_bundle","dry_run":true,` + ids + `}`, 200, ""},
		{admin, `{"action":"delete","dry_run":true,` + ids + `}`, 200, ""},
	}
	for i, tt := range tests {
		rec := r.do("POST", "/api/items/batch", tt.body, withBearer(tt.tok))
		if rec.Code != tt.code || (tt.scope != "" && errBody(t, rec).RequiredScope != tt.scope) {
			t.Errorf("[%d] %s：%d %s", i, tt.body, rec.Code, rec.Body)
		}
	}
	if got, _ := r.st.Read().ItemByID(context.Background(), it.ID); got.DeletedAt != nil {
		t.Error("沒有權限的請求不能刪除")
	}
	// 沒有登入：401，不讀本體。
	if rec := r.do("POST", "/api/items/batch", `{"action":"mark_read",`+ids+`}`, nil); rec.Code != 401 {
		t.Errorf("沒有登入：%d", rec.Code)
	}
}

// LIB-R15、R19：逐部執行，一部失敗不影響其他部；items 依請求的順序；寫一筆 items.batch；read_state 可以篩選。
func TestBatchMarkReadPartialFailure(t *testing.T) {
	r := newBatchRig(t)
	ctx := context.Background()
	a := r.insert("youtube", "aaaaaaaaaaa", 0)
	b := r.insert("youtube", "bbbbbbbbbbb", 0)
	gone := r.insert("youtube", "ccccccccccc", 0)
	if rec := r.owner("DELETE", "/api/items/"+gone.ID, ""); rec.Code != 200 {
		t.Fatalf("刪除 = %d %s", rec.Code, rec.Body)
	}
	evBefore, _ := r.st.Read().CountEvents(ctx)
	res, e, code := r.batch(`{"action":"mark_read","ids":["yt:aaaaaaaaaaa","yt:missing0000","` + a.ID + `","yt:ccccccccccc","` + b.ID + `"]}`)
	if code != 200 {
		t.Fatalf("%d %+v", code, e)
	}
	if res.Action != "mark_read" || res.DryRun || res.Count != 4 || *res.Succeeded != 2 || *res.Failed != 2 || res.EstimatedUSD != nil || res.Budget != nil {
		t.Errorf("結果 = %+v", res)
	}
	want := []BatchItem{
		{ID: a.ID, Ref: "yt:aaaaaaaaaaa", OK: true},
		{Ref: "yt:missing0000", Code: CodeNotFound, Message: "找不到這部影音。"},
		{ID: gone.ID, Ref: "yt:ccccccccccc", Code: CodeInTrash, Message: "這部影音在垃圾桶中。"},
		{ID: b.ID, Ref: "yt:bbbbbbbbbbb", OK: true},
	}
	if fmt.Sprint(res.Items) != fmt.Sprint(want) {
		t.Errorf("items = %+v\n預期 %+v", res.Items, want)
	}
	if evAfter, _ := r.st.Read().CountEvents(ctx); evAfter != evBefore+1 {
		t.Errorf("items.batch 事件：%d → %d", evBefore, evAfter)
	}

	list := func(q string) []Item {
		t.Helper()
		return decode[struct{ Items []Item }](t, r.owner("GET", "/api/items"+q, "")).Items
	}
	if got := list("?read_state=read"); len(got) != 2 || got[0].ReadState != "read" {
		t.Errorf("已讀 = %+v", got)
	}
	if got := list("?read_state=unread"); len(got) != 0 {
		t.Errorf("未讀 = %+v", got)
	}
	if d := decode[ItemDetail](t, r.owner("GET", "/api/items/"+a.ID, "")); d.ReadState != "read" {
		t.Errorf("詳情 = %q", d.ReadState)
	}
	if _, _, code := r.batch(`{"action":"mark_unread","ids":["` + a.ID + `"]}`); code != 200 {
		t.Fatal(code)
	}
	if got := list("?read_state=unread"); len(got) != 1 || got[0].ID != a.ID || got[0].ReadState != "unread" {
		t.Errorf("改回未讀 = %+v", got)
	}
}

// LIB-R14：每個動作的 dry_run 都不改變任何資料、不寫事件；count 是 ok 的影音數。
func TestBatchDryRunWritesNothing(t *testing.T) {
	r := newBatchRig(t)
	ctx := context.Background()
	a := r.insert("youtube", "aaaaaaaaaaa", 0)
	_, err := r.st.Write().InsertCostRow(ctx, store.CostRow{ItemID: a.ID, Stage: "correct", Provider: "p", Model: "m", UnitType: "tokens", USD: 0.42, State: store.CostActual, At: r.clk.Now()})
	if err != nil {
		t.Fatal(err)
	}
	ids := `"ids":["` + a.ID + `","yt:missing0000"]`
	before := r.counts()
	for _, body := range []string{
		`{"action":"rerun","from":"normalize","dry_run":true,` + ids + `}`,
		`{"action":"change_profile","from":"normalize","profile":"default","dry_run":true,` + ids + `}`,
		`{"action":"delete","dry_run":true,"delete_outputs":true,` + ids + `}`,
		`{"action":"mark_read","dry_run":true,` + ids + `}`,
		`{"action":"mark_unread","dry_run":true,` + ids + `}`,
		`{"action":"export_bundle","dry_run":true,` + ids + `}`,
	} {
		res, e, code := r.batch(body)
		if code != 200 {
			t.Fatalf("%s：%d %+v", body, code, e)
		}
		if !res.DryRun || res.Count != 1 || len(res.Items) != 2 || !res.Items[0].OK || res.Items[1].OK || res.Items[1].Code != CodeNotFound ||
			res.EstimatedUSD == nil || res.UnknownEstimates == nil || res.Budget == nil || res.Budget.RemainingUSD != nil || res.Budget.Exceeds ||
			res.Items[0].Title == nil || res.Succeeded != nil {
			t.Errorf("%s：%+v", body, res)
		}
		if (res.Deletion != nil) != strings.Contains(body, "delete") {
			t.Errorf("%s：deletion = %+v", body, res.Deletion)
		}
		if res.Deletion != nil && (res.Deletion.SpentUSD != 0.42 || res.Deletion.Outputs != 0) {
			t.Errorf("deletion = %+v", res.Deletion)
		}
	}
	if after := r.counts(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("dry_run 不能寫入：\n%v\n%v", before, after)
	}
}

// LIB-R14：exceeds 是估算大於 remaining_usd；本月已經超支（remaining 0）而估算是 0 時不算超過。
func TestBatchBudget(t *testing.T) {
	r := newBatchRig(t)
	ctx := context.Background()
	a := r.insert("youtube", "aaaaaaaaaaa", 0)
	svc := cost.New(r.clk, nil, cost.Options{})
	b, errs := cost.ParseBudget(map[string]any{"monthly_usd": 0.5})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := r.st.Tx(ctx, func(rp *store.Repo) error { return svc.SetBudget(ctx, rp, b) }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.Write().InsertCostRow(ctx, store.CostRow{ItemID: a.ID, Stage: "correct", Provider: "p", Model: "m", UnitType: "tokens", USD: 0.7, State: store.CostActual, At: r.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	res, e, _ := r.batch(`{"action":"export_bundle","dry_run":true,"ids":["` + a.ID + `"]}`)
	if e != nil || res.Budget.RemainingUSD == nil || *res.Budget.RemainingUSD != 0 || res.Budget.Exceeds || *res.EstimatedUSD != 0 {
		t.Errorf("超支後估算 0 = %+v %+v", res, e)
	}
	exceeds, err := newBatchRig(t).batchBudget(ctx, 1)
	if err != nil || exceeds.Exceeds || exceeds.RemainingUSD != nil {
		t.Errorf("沒有預算 = %+v %v", exceeds, err)
	}
}

func (r *batchRig) batchBudget(ctx context.Context, est cost.Micro) (*BatchBudget, error) {
	rt := &routes{d: Deps{Store: r.st, Cost: cost.New(r.clk, nil, cost.Options{})}}
	return rt.batchBudget(ctx, est)
}

// LIB-R18、R20：delete 實際執行移到垃圾桶；export_bundle 逐部回傳下載路徑；不帶分P的 bili:<BV> 展開。
func TestBatchDeleteAndExport(t *testing.T) {
	r := newBatchRig(t)
	ctx := context.Background()
	p1 := r.insert("bilibili", "BV1xx411c7mD", 1)
	p2 := r.insert("bilibili", "BV1xx411c7mD", 2)
	res, e, _ := r.batch(`{"action":"export_bundle","ids":["bili:BV1xx411c7mD"]}`)
	if e != nil || res.Count != 2 || *res.Succeeded != 2 || res.Items[0].BundleURL != "/api/items/"+p1.ID+"/bundle" || res.Items[1].BundleURL != "/api/items/"+p2.ID+"/bundle" {
		t.Errorf("匯出 = %+v %+v", res, e)
	}
	res, e, _ = r.batch(`{"action":"delete","ids":["bili:BV1xx411c7mD/p2","bili:BV1xx411c7mD"]}`)
	if e != nil || res.Count != 2 || *res.Succeeded != 2 || res.Items[0].ID != p2.ID || res.Items[1].ID != p1.ID {
		t.Errorf("刪除 = %+v %+v", res, e)
	}
	trash, err := r.st.Read().ListTrash(ctx)
	if err != nil || len(trash) != 2 {
		t.Errorf("垃圾桶 = %d %v", len(trash), err)
	}
	// 已經在垃圾桶中：in_trash，不計入 dry_run 的合計。
	res, _, _ = r.batch(`{"action":"delete","dry_run":true,"ids":["` + p1.ID + `"]}`)
	if res.Count != 0 || res.Items[0].Code != CodeInTrash || res.Deletion.Artifacts != 0 {
		t.Errorf("垃圾桶中的 dry_run = %+v", res)
	}
	res, _, _ = r.batch(`{"action":"export_bundle","ids":["` + p1.ID + `"]}`)
	if *res.Failed != 1 || res.Items[0].BundleURL != "" || res.Items[0].Code != CodeInTrash {
		t.Errorf("垃圾桶中的匯出 = %+v", res.Items)
	}
}

// LIB-R17：rerun 對每部影音等同單一影音的重跑；從還沒實作或不正確的狀態重跑的錯誤和單一影音相同。
func TestBatchRerun(t *testing.T) {
	r := newBatchRig(t)
	id := r.ingestOne(abcURL)
	res, e, _ := r.batch(`{"action":"rerun","from":"normalize","ids":["` + id + `","yt:missing0000"]}`)
	if e != nil || res.Count != 2 || *res.Succeeded != 1 || *res.Failed != 1 || !res.Items[0].OK {
		t.Fatalf("重跑 = %+v %+v", res, e)
	}
	d := decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, ""))
	var normalize Stage
	for _, s := range d.Stages {
		if s.Stage == "normalize" {
			normalize = s
		}
	}
	if normalize.Status != "pending" && normalize.Status != "running" {
		t.Errorf("normalize = %+v", normalize)
	}
	res, _, _ = r.batch(`{"action":"change_profile","from":"normalize","profile":"default","ids":["` + id + `"]}`)
	if *res.Succeeded != 1 {
		t.Errorf("換處理方案 = %+v", res)
	}
	got, _ := r.st.Read().ItemByID(context.Background(), id)
	if got.ProfileID != "default" {
		t.Errorf("處理方案 = %q", got.ProfileID)
	}
}

// LIB-R21、R22：總覽的區塊、tz 的驗證與預設、磁碟用量。
func TestOverviewRoute(t *testing.T) {
	r := newBatchRig(t, func(d *Deps) { d.Disk = diskusage.New(t.TempDir(), d.Clock) })
	id := r.ingestOne(abcURL)
	rec := r.owner("GET", "/api/overview", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var ov OverviewResult
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Month.Month != "2026-11" || ov.Month.Timezone != "UTC" || ov.Month.MonthlyUSD != nil || ov.Disk.MeasuredAt == nil || ov.Notices == nil || ov.Today == nil || ov.RecentDone == nil {
		t.Errorf("總覽 = %+v", ov)
	}
	if len(ov.Today) == 0 || ov.Today[len(ov.Today)-1].Type != "item.created" {
		t.Errorf("今天 = %+v", ov.Today)
	}
	// 這個 rig 的 sponsorblock 會失敗：需要你處理列出它（failed 的 reason 是 error_class）。
	if ov.NeedsYou.Total != 1 || ov.NeedsYou.Items[0].Ref != "yt:abc123xxxxx" || ov.NeedsYou.Items[0].Status != "failed" || ov.NeedsYou.Items[0].Reason == "" {
		t.Errorf("需要你處理 = %+v", ov.NeedsYou)
	}
	for _, raw := range []string{`"now":{"items":[]`, `"monthly_usd":null`} {
		if !strings.Contains(rec.Body.String(), raw) {
			t.Errorf("回應要有 %s：%s", raw, rec.Body)
		}
	}
	for _, tz := range []string{"Mars/Olympus", "Local", "..%2Fetc", "%00"} {
		rec := r.owner("GET", "/api/overview?tz="+tz, "")
		if e := errBody(t, rec); rec.Code != 422 || e.Code != CodeInvalidInput || !strings.HasPrefix(e.Message, "不認識的時區 ") {
			t.Errorf("tz=%s：%d %s", tz, rec.Code, rec.Body)
		}
	}
	// tz 決定「今天」的起點：UTC+14 的今天從 UTC 前一天的 10:00 開始，比影音新增早，所以事件仍在。
	r.clk.Advance(20 * time.Hour)
	if got := decode[OverviewResult](t, r.owner("GET", "/api/overview?tz=UTC", "")); len(got.Today) != 0 {
		t.Errorf("UTC 的今天 = %+v", got.Today)
	}
	if got := decode[OverviewResult](t, r.owner("GET", "/api/overview?tz=Pacific/Kiritimati", "")); len(got.Today) == 0 {
		t.Errorf("UTC+14 的今天 = %+v", got.Today)
	}
	// read 就能看；垃圾桶中的影音不出現。
	if rec := r.do("GET", "/api/overview", "", withBearer(r.token(auth.ScopeIngest))); rec.Code != 403 {
		t.Errorf("沒有 read：%d", rec.Code)
	}
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	got := decode[OverviewResult](t, r.owner("GET", "/api/overview", ""))
	if got.NeedsYou.Total != 0 || got.Now.Total != 0 || len(got.RecentDone) != 0 {
		t.Errorf("垃圾桶中的影音 = %+v", got)
	}
}
