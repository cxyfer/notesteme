package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// insertItem 直接在 store 建立一部影音（批次解析的測試不需要跑 pipeline）。
func insertItem(t *testing.T, r *rig, platform, ext string, part int) store.Item {
	t.Helper()
	it, _, err := r.st.Write().InsertItemIfAbsent(context.Background(), store.NewItem{Platform: platform, ExternalID: ext, Part: part, URL: "https://example.test/" + ext})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// LIB-R13：代號與 ID 都能用；bili:<BV> 展開成所有分P；同一部影音只留第一次；找不到的以原始寫法去重複；垃圾桶中的回 InTrashError。
func TestResolveBatch(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	yt := insertItem(t, r, source.PlatformYouTube, "aaaaaaaaaaa", 0)
	p1 := insertItem(t, r, source.PlatformBilibili, "BV1xx411c7mD", 1)
	p2 := insertItem(t, r, source.PlatformBilibili, "BV1xx411c7mD", 2)
	gone := insertItem(t, r, source.PlatformYouTube, "bbbbbbbbbbb", 0)
	if _, err := r.p.Trash(ctx, gone.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := r.p.ResolveBatch(ctx, []string{
		"yt:aaaaaaaaaaa", yt.ID, "bili:BV1xx411c7mD/p2", "bili:BV1xx411c7mD", "yt:missing0000", "yt:missing0000",
		"yt:bbbbbbbbbbb", "bili:BVnope", "' OR 1=1 --",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		raw, id string
		err     error
	}{
		{"yt:aaaaaaaaaaa", yt.ID, nil},
		{"bili:BV1xx411c7mD/p2", p2.ID, nil},
		{"bili:BV1xx411c7mD", p1.ID, nil},
		{"yt:missing0000", "", store.ErrNotFound},
		{"yt:bbbbbbbbbbb", gone.ID, ErrInTrash},
		{"bili:BVnope", "", store.ErrNotFound},
		{"' OR 1=1 --", "", store.ErrNotFound},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d 筆：%+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Raw != w.raw || g.Item.ID != w.id || (w.err == nil) != (g.Err == nil) || (w.err != nil && !errors.Is(g.Err, w.err)) {
			t.Errorf("[%d] = %+v（%v），預期 %+v", i, g.Raw, g.Err, w)
		}
	}
}

// LIB-R19：mark_read／mark_unread 只改 read_state；不存在、在垃圾桶中與不正確的值回錯誤。
func TestSetReadState(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := insertItem(t, r, source.PlatformYouTube, "aaaaaaaaaaa", 0)
	if it.ReadState != store.ReadUnread {
		t.Fatalf("新增時 = %q", it.ReadState)
	}
	if err := r.p.SetReadState(ctx, it.ID, store.ReadRead); err != nil {
		t.Fatal(err)
	}
	got, _ := r.st.Read().ItemByID(ctx, it.ID)
	if got.ReadState != store.ReadRead {
		t.Fatalf("標為已讀之後 = %q", got.ReadState)
	}
	if err := r.p.SetReadState(ctx, it.ID, "bogus"); err == nil {
		t.Error("不正確的值要回錯誤")
	}
	if err := r.p.SetReadState(ctx, "nope", store.ReadRead); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在 = %v", err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SetReadState(ctx, it.ID, store.ReadUnread); !errors.Is(err, ErrInTrash) {
		t.Errorf("垃圾桶 = %v", err)
	}
	got, _ = r.st.Read().ItemByID(ctx, it.ID)
	if got.ReadState != store.ReadRead {
		t.Errorf("垃圾桶中的影音不能改：%q", got.ReadState)
	}
}

// rowCounts 是會因寫入而改變的計數：成本紀錄、stage run、任務與事件。
func rowCounts(t *testing.T, c *costRig) [4]int {
	t.Helper()
	ctx := context.Background()
	r := c.st.Read()
	rows, err := r.ItemCostRows(ctx, c.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := r.StageRunsByItem(ctx, c.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := r.ItemTaskIDs(ctx, c.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := r.CountEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return [4]int{len(rows), len(runs), len(tasks), int(evs)}
}

// LIB-R14：估算 = 每個 chunk 一個請求依 COST-R5 估算的總和；測試控制的金額優先；找不到單價時計入 Unknown；
// 估算不寫入任何資料。
func TestEstimateRerun(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	eff := c.effective(t)
	job, err := c.p.prepareLLM(ctx, c.it, StageCorrect, eff, "")
	if err != nil {
		t.Fatal(err)
	}
	chunks := len(job.chunks)
	if chunks < 2 {
		t.Fatalf("測試需要多個 chunk：%d", chunks)
	}
	before := rowCounts(t, c)
	est, err := c.p.EstimateRerun(ctx, c.it.ID, StageCorrect, "")
	if err != nil {
		t.Fatal(err)
	}
	// 每個 chunk 依單價估算：輸入 $1／百萬 token，所以總和大於 0；全部都有單價。
	var want cost.Micro
	for _, ch := range job.chunks {
		req, err := chunkRequestFor(job, ch)
		if err != nil {
			t.Fatal(err)
		}
		in, out := requestTokens(req)
		m, _, err := c.svc.Estimate(ctx, c.st.Read(), cost.Call{Stage: StageCorrect, Provider: "anthropic", Model: "m", InputTokens: in, Output: out, CustomBase: c.p.customBase("anthropic")})
		if err != nil {
			t.Fatal(err)
		}
		want += m
	}
	if est.USD != want || want <= 0 || est.Unknown != 0 {
		t.Errorf("估算 = %+v，預期 %d", est, want)
	}
	if after := rowCounts(t, c); after != before {
		t.Errorf("估算不能寫入：%v → %v", before, after)
	}

	// 從 segment 起：segment 沒有設定 provider（not_configured）略過，只算 correct。
	if seg, err := c.p.EstimateRerun(ctx, c.it.ID, StageSegment, ""); err != nil || seg != est {
		t.Errorf("從 segment = %+v, %v", seg, err)
	}
	// 從 publish 起：沒有付費步驟。
	if pub, err := c.p.EstimateRerun(ctx, c.it.ID, StagePublish, ""); err != nil || pub != (RerunEstimate{}) {
		t.Errorf("從 publish = %+v, %v", pub, err)
	}
	// 錯誤和 Rerun 相同。
	if _, err := c.p.EstimateRerun(ctx, "nope", StageCorrect, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在 = %v", err)
	}
	if _, err := c.p.EstimateRerun(ctx, c.it.ID, "proofread", ""); err == nil {
		t.Error("不認識的步驟要回錯誤")
	}
	if _, err := c.p.EstimateRerun(ctx, c.it.ID, StageCorrect, "nope"); err == nil {
		t.Error("不存在的處理方案要回錯誤")
	}
}

// 測試控制的金額（`the cost estimate of every … task is …`）取代每個請求的估算。
func TestEstimateRerunUsesTestEstimates(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{Estimates: map[string]float64{StageCorrect: 0.04}}, nil)
	job, err := c.p.prepareLLM(ctx, c.it, StageCorrect, c.effective(t), "")
	if err != nil {
		t.Fatal(err)
	}
	est, err := c.p.EstimateRerun(ctx, c.it.ID, StageCorrect, "")
	if err != nil {
		t.Fatal(err)
	}
	if est.USD != cost.Micro(40000*len(job.chunks)) || est.Unknown != 0 {
		t.Errorf("估算 = %+v，chunk 數 %d", est, len(job.chunks))
	}
}

// 找不到單價的 provider：每個請求計入 Unknown，不加總。
func TestEstimateRerunUnpriced(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	err := c.st.Tx(ctx, func(r *store.Repo) error {
		for _, unit := range []string{cost.UnitInputMTok, cost.UnitOutputMTok} {
			if err := c.svc.DeletePrice(ctx, r, map[string]any{"provider": "anthropic", "model": "m", "unit_type": unit}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := c.p.prepareLLM(ctx, c.it, StageCorrect, c.effective(t), "")
	if err != nil {
		t.Fatal(err)
	}
	est, err := c.p.EstimateRerun(ctx, c.it.ID, StageCorrect, "")
	if err != nil {
		t.Fatal(err)
	}
	if est.USD != 0 || est.Unknown != len(job.chunks) {
		t.Errorf("估算 = %+v，chunk 數 %d", est, len(job.chunks))
	}
}

// 沒有輸入字幕時（例如影音還沒有轉錄）LLM 步驟無法估算：計入一個 Unknown。
func TestEstimateRerunWithoutInput(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	bare, _, err := c.st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: source.PlatformYouTube, ExternalID: "bare0000001", URL: "https://example.test/bare",
		Overrides: json.RawMessage(`{"stages":{"correct":{"executor":"api","provider":"anthropic","model":"m","prompt":"correct-default"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	est, err := c.p.EstimateRerun(ctx, bare.ID, StageCorrect, "")
	if err != nil {
		t.Fatal(err)
	}
	if est.Unknown != 1 || est.USD != 0 {
		t.Errorf("估算 = %+v", est)
	}
}

func chunkRequestFor(job *llmJob, c llm.Chunk) (llm.Request, error) {
	return llm.ChunkRequest(llm.RunOptions{Model: job.cfg.Model, Input: job.chunkInput(c), Spec: job.spec, Fixed: job.fixed})
}

// effective 是影音目前處理方案的生效設定（與 prepareRerun 相同的組合方式）。
// 同時把 c.it 換成資料庫中目前的影音（metadata 完成後才有標題，標題會進 prompt 的固定前綴）。
func (c *costRig) effective(t *testing.T) Effective {
	t.Helper()
	prep, err := c.p.prepareRerun(context.Background(), c.it.ID, RerunRequest{From: StageCorrect})
	if err != nil {
		t.Fatal(err)
	}
	c.it = prep.it
	return prep.eff
}

// LIB-R21：現在、需要你處理、今天、最近完成；只看每個步驟最新的一次執行；垃圾桶中的影音不出現。
func TestOverview(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	since := r.clk.Now().Add(-time.Hour)
	snap, err := r.st.Write().EnsureSnapshot(ctx, "sha-ov", "{}")
	if err != nil {
		t.Fatal(err)
	}
	add := func(it store.Item, stage, status string, st store.StageState) store.StageRun {
		t.Helper()
		run, err := r.st.Write().InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			t.Fatal(err)
		}
		st.Status = status
		if err := r.st.Write().SetStageState(ctx, run.ID, st); err != nil {
			t.Fatal(err)
		}
		return run
	}
	// done：每個步驟都結束（done 或 skipped）。
	done := r.ingest(watch, nil, "")
	if _, err := r.p.CancelItem(ctx, done.ID); err != nil {
		t.Fatal(err)
	}
	for i, stage := range Stages {
		status := store.StatusDone
		if i%2 == 1 {
			status = store.StatusSkipped
		}
		add(done, stage, status, store.StageState{})
	}

	ov, err := r.p.Overview(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if ov.NowTotal != 0 || ov.NeedsTotal != 0 || len(ov.RecentDone) != 1 || ov.RecentDone[0].ID != done.ID || ov.RecentDone[0].Ref != "yt:abc123xxxxx" {
		t.Fatalf("全部完成之後 = %+v", ov)
	}
	if len(ov.Today) == 0 {
		t.Fatalf("今天 = %+v", ov.Today)
	}
	for _, e := range ov.Today {
		if e.Type == "stage.status" && e.Item != "yt:abc123xxxxx" {
			t.Errorf("事件缺少影音：%+v", e)
		}
	}

	// 其他影音：metadata running、transcript failed、correct blocked。
	a := insertItem(t, r, source.PlatformYouTube, "running0001", 0)
	b := insertItem(t, r, source.PlatformYouTube, "failing0001", 0)
	add(a, StageMetadata, store.StatusRunning, store.StageState{})
	// b 的 correct 先 failed、後來重跑成 blocked：只看最新的一次。transcript 也 failed，pipeline 順序在前。
	r.clk.Advance(time.Second)
	add(b, StageCorrect, store.StatusFailed, store.StageState{ErrorClass: "permanent"})
	r.clk.Advance(time.Second)
	add(b, StageCorrect, store.StatusBlocked, store.StageState{BlockReason: "budget_blocked", Message: "超過預算", Actions: []string{"調整預算"}})
	add(b, StageTranscript, store.StatusFailed, store.StageState{ErrorClass: "permanent", Message: "壞了"})

	ov, err = r.p.Overview(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if ov.NowTotal != 1 || len(ov.Now) != 1 || ov.Now[0].ID != a.ID || ov.Now[0].Stage != StageMetadata {
		t.Errorf("現在 = %+v", ov.Now)
	}
	if ov.NeedsTotal != 1 || len(ov.Needs) != 1 {
		t.Fatalf("需要你處理 = %+v", ov.Needs)
	}
	if n := ov.Needs[0]; n.ID != b.ID || n.Stage != StageTranscript || n.Status != store.StatusFailed || n.Reason != "permanent" || n.Message != "壞了" || n.Actions == nil {
		t.Errorf("第一個需要處理的步驟 = %+v", n)
	}

	// 垃圾桶中的影音不出現在任何區塊；item.deleted 事件保留（帶著當時的標題）。
	for _, it := range []store.Item{a, b, done} {
		if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	ov, err = r.p.Overview(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if ov.NowTotal != 0 || ov.NeedsTotal != 0 || len(ov.RecentDone) != 0 {
		t.Errorf("垃圾桶中的影音 = %+v", ov)
	}
	for _, e := range ov.Today {
		if e.Type != "item.deleted" && e.Item == "yt:abc123xxxxx" {
			t.Errorf("垃圾桶中的影音的事件不能出現：%+v", e)
		}
	}
	if ov.Today[0].Type != "item.deleted" {
		t.Errorf("最新的事件 = %+v", ov.Today[0])
	}

	// since 之後沒有事件。
	ov, err = r.p.Overview(ctx, r.clk.Now().Add(time.Hour))
	if err != nil || len(ov.Today) != 0 {
		t.Errorf("今天之後 = %+v, %v", ov.Today, err)
	}
}

// 「現在」最多 3 部，依最早開始的排序。
func TestOverviewNowLimit(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	snap, err := r.st.Write().EnsureSnapshot(ctx, "sha-now", "{}")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ext := range []string{"run00000001", "run00000002", "run00000003", "run00000004"} {
		it := insertItem(t, r, source.PlatformYouTube, ext, 0)
		run, err := r.st.Write().InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: StageMetadata, Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.st.Write().SetStageState(ctx, run.ID, store.StageState{Status: store.StatusRunning}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
		r.clk.Advance(time.Second)
	}
	ov, err := r.p.Overview(ctx, r.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ov.NowTotal != 4 || len(ov.Now) != OverviewNowLimit {
		t.Fatalf("現在 = %+v", ov)
	}
	for i, n := range ov.Now {
		if n.ID != ids[i] {
			t.Errorf("[%d] = %s，預期 %s", i, n.ID, ids[i])
		}
	}
}
