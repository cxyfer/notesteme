package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/digest"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// newDigestRig 建立有彙整 service 的 rig，並新增一個勾選 digest 的 notifier「phone」。
func newDigestRig(t *testing.T) (*itemsRig, *auth.Service) {
	t.Helper()
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) {
		svc = d.Auth
		d.Digests = digest.New(digest.Deps{Store: d.Store, Clock: d.Clock, Outbox: &notify.Outbox{Clock: d.Clock}, Summaries: d.Pipeline})
	})
	err := r.st.Tx(context.Background(), func(rp *store.Repo) error {
		return rp.InsertNotifier(context.Background(), store.Notifier{ID: "phone", Kind: notify.KindNtfy, Nonce: []byte{0}, Ciphertext: []byte{0}, PublicJSON: `{}`, Events: []string{notify.EventDigest}, Enabled: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, svc
}

// awaitSegment 把影音的 segment 改成等你決定（模擬摘要後暫停）。
func (r *itemsRig) awaitSegment(itemID string) {
	r.t.Helper()
	ctx := context.Background()
	err := r.st.Tx(ctx, func(rp *store.Repo) error {
		runs, err := rp.StageRunsByItem(ctx, itemID)
		if err != nil {
			return err
		}
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].Stage == pipeline.StageSegment {
				return rp.SetStageState(ctx, runs[i].ID, store.StageState{Status: store.StatusBlocked, BlockReason: pipeline.BlockAwaitingUser})
			}
		}
		r.t.Fatal("沒有 segment 步驟")
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func TestContinueRoutes(t *testing.T) {
	r, svc := newDigestRig(t)
	id := r.ingestOne(abcURL)

	if rec := r.owner("POST", "/api/items/"+id+"/continue", ""); rec.Code != 409 || errBody(t, rec).Code != CodeNotAwaitingUser || !strings.Contains(rec.Body.String(), "這部影音沒有在等待你決定是否繼續。") {
		t.Fatalf("沒有暫停 = %d %s", rec.Code, rec.Body)
	}
	r.awaitSegment(id)
	if rec := r.owner("POST", "/api/items/yt:abc123xxxxx/continue", ""); rec.Code != 202 || strings.TrimSpace(rec.Body.String()) != `{"continued":"segment"}` {
		t.Fatalf("繼續 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/items/yt:zzzzzzzzzzz/continue", ""); rec.Code != 404 {
		t.Errorf("不存在 = %d %s", rec.Code, rec.Body)
	}

	// 批次：個別失敗不影響其他影音，id 原樣回傳。
	_ = r.worker.RunUntilIdle(context.Background())
	r.awaitSegment(id)
	body := `{"ids":["yt:abc123xxxxx","yt:zzzzzzzzzzz","01a20000-0000-7000-8000-000000000000","not a ref","yt:abc123xxxxx"]}`
	rec := r.owner("POST", "/api/items/continue", body)
	want := `{"continued":["yt:abc123xxxxx"],"skipped":[{"id":"yt:zzzzzzzzzzz","code":"not_found"},{"id":"01a20000-0000-7000-8000-000000000000","code":"not_found"},{"id":"not a ref","code":"not_found"},{"id":"yt:abc123xxxxx","code":"not_awaiting_user"}]}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("批次 = %d %s", rec.Code, rec.Body)
	}
	for name, b := range map[string]string{"0 個": `{"ids":[]}`, "101 個": `{"ids":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",") + `]}`} {
		rec := r.owner("POST", "/api/items/continue", b)
		if e := errBody(t, rec); rec.Code != 422 || len(e.Errors) != 1 || e.Errors[0].Path != "ids" {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}

	// 垃圾桶中的影音：in_trash。
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code >= 300 {
		t.Fatalf("刪除 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/items/continue", `{"ids":["`+id+`"]}`)
	if !strings.Contains(rec.Body.String(), `"code":"in_trash"`) {
		t.Errorf("垃圾桶 = %d %s", rec.Code, rec.Body)
	}

	// scope：read 不能繼續。
	tok, err := svc.CreateToken(context.Background(), "r", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/items/" + id + "/continue", "/api/items/continue"} {
		rec := r.do("POST", p, `{"ids":["x"]}`, withBearer(tok.Token))
		if e := errBody(t, rec); rec.Code != 403 || e.RequiredScope != auth.ScopeIngest {
			t.Errorf("%s read = %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestDigestRoutes(t *testing.T) {
	r, svc := newDigestRig(t)
	rec := r.owner("POST", "/api/digests", `{"id":"daily","period":"daily","at":"09:30","timezone":"Asia/Taipei","notifiers":["phone"],"scope":"all"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"next_run_at":"2026-11-13T01:30:00Z"`) || !strings.Contains(rec.Body.String(), `"weekday":null`) {
		t.Fatalf("建立 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/digests", `{"id":"daily","period":"daily","notifiers":["phone"]}`); rec.Code != 409 || errBody(t, rec).Code != CodeDigestExists {
		t.Errorf("重複 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/digests", `{"id":"bad","period":"daily","timezone":"Mars/Base","notifiers":["ghost"]}`)
	if e := errBody(t, rec); rec.Code != 422 || len(e.Errors) != 2 || e.Errors[0].Path != "timezone" || e.Errors[1].Path != "notifiers" {
		t.Errorf("422 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/digests", `{"id":"x","period":"daily","notifiers":["phone"],"extra":1}`); rec.Code != 422 {
		t.Errorf("多餘欄位 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/digests", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"daily"`) {
		t.Errorf("列出 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("PATCH", "/api/digests/daily", `{"enabled":false}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"next_run_at":null`) {
		t.Errorf("停用 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("PATCH", "/api/digests/nope", `{"enabled":false}`); rec.Code != 404 {
		t.Errorf("PATCH 不存在 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/digests/daily/preview", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("預覽 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/digests/nope/preview", ""); rec.Code != 404 {
		t.Errorf("預覽不存在 = %d", rec.Code)
	}
	if rec := r.owner("DELETE", "/api/digests/daily", ""); rec.Code != 204 {
		t.Errorf("刪除 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("DELETE", "/api/digests/daily", ""); rec.Code != 404 {
		t.Errorf("再刪除 = %d", rec.Code)
	}
	// 彙整設定需要 admin。
	tok, err := svc.CreateToken(context.Background(), "ri", []string{auth.ScopeRead, auth.ScopeIngest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"GET", "/api/digests"}, {"POST", "/api/digests"}, {"PATCH", "/api/digests/x"}, {"DELETE", "/api/digests/x"}, {"POST", "/api/digests/x/preview"}} {
		rec := r.do(c[0], c[1], `{}`, withBearer(tok.Token))
		if e := errBody(t, rec); rec.Code != 403 || e.RequiredScope != auth.ScopeAdmin {
			t.Errorf("%s %s = %d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

// 沒有彙整 service 時回 501。
func TestDigestRoutesDisabled(t *testing.T) {
	r := newItemsRigAPI(t, nil)
	if rec := r.owner("GET", "/api/digests", ""); rec.Code != 501 {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
}
