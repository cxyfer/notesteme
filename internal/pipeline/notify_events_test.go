package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestRunEvent(t *testing.T) {
	it := store.Item{ID: "i1"}
	allDone := map[string]store.StageRun{}
	for i, st := range Stages {
		status := store.StatusDone
		if i%2 == 1 {
			status = store.StatusSkipped
		}
		allDone[st] = store.StageRun{ID: "r-" + st, Stage: st, Status: status}
	}
	ev, ok := runEvent(it, allDone, StagePublish)
	if !ok || ev.Name != notify.EventItemDone || ev.DedupeKey != "item.done:i1:r-publish" {
		t.Errorf("全部 done／skipped：%+v %v", ev, ok)
	}
	// until 提前結束（seeding、MCP 的 until: transcript）不通知；until 是最後一個步驟時等同沒有 until。
	if _, ok := runEvent(store.Item{ID: "i1", UntilStage: StageTranscript}, allDone, StageTranscript); ok {
		t.Error("until 提前結束不應通知")
	}
	if _, ok := runEvent(store.Item{ID: "i1", UntilStage: StagePublish}, allDone, StagePublish); !ok {
		t.Error("until 是 publish 時仍算完成")
	}
	// 缺少後來才加的步驟（舊影音）不影響判斷。
	partial := map[string]store.StageRun{StageMetadata: {ID: "a", Status: store.StatusDone}, StagePublish: {ID: "b", Status: store.StatusDone}}
	if ev, ok := runEvent(it, partial, StagePublish); !ok || ev.DedupeKey != "item.done:i1:b" {
		t.Errorf("舊影音：%+v %v", ev, ok)
	}
	pending := map[string]store.StageRun{StageMetadata: {Status: store.StatusDone}, StageTranscript: {Status: store.StatusPending}}
	if _, ok := runEvent(it, pending, StageMetadata); ok {
		t.Error("最後一個步驟還是 pending 不算完成")
	}
	// 從 publish 重跑（seeding 之後）：更早的步驟從來沒有執行過也算完成。
	late := map[string]store.StageRun{StageTranscript: {Status: store.StatusPending}, StagePublish: {ID: "p2", Status: store.StatusSkipped}}
	if ev, ok := runEvent(it, late, StagePublish); !ok || ev.DedupeKey != "item.done:i1:p2" {
		t.Errorf("從 publish 重跑：%+v %v", ev, ok)
	}
	failed := map[string]store.StageRun{StageMetadata: {Status: store.StatusFailed, ErrorClass: "permanent", Message: "影片不存在。", Actions: []string{"刪除"}}}
	if ev, ok := runEvent(it, failed, StageMetadata); !ok || ev.Name != notify.EventItemFailed || ev.DedupeKey != "item.failed:i1:metadata" ||
		ev.Stage != StageMetadata || ev.ErrorClass != "permanent" || ev.Message != "影片不存在。" || len(ev.Actions) != 1 {
		t.Errorf("failed：%+v %v", ev, ok)
	}
	for reason, want := range map[string]bool{"needs_action": true, "budget_blocked": true, "awaiting_user": false} {
		b := map[string]store.StageRun{StageTranscript: {Status: store.StatusBlocked, BlockReason: reason}}
		ev, ok := runEvent(it, b, StageTranscript)
		if ok != want || (ok && ev.DedupeKey != "item.blocked:i1:transcript:"+reason) {
			t.Errorf("blocked %s：%+v %v", reason, ev, ok)
		}
	}
	if _, ok := runEvent(it, allDone, "unknown"); ok {
		t.Error("沒有這個步驟的紀錄")
	}
	if _, ok := runEvent(it, map[string]store.StageRun{}, ""); ok {
		t.Error("沒有任何紀錄")
	}
}

func TestEmitRunEventWritesTheOutbox(t *testing.T) {
	ctx := context.Background()
	e := newListEnv(t)
	tx := e.st.Write()
	it := e.add(ctx, tx, lItem{platform: "youtube", ext: "abc123xxxxx", title: "T", channel: "Chan", stages: map[string]string{StageMetadata: store.StatusFailed}})
	// 沒有設定通知：什麼都不做。
	if err := e.p.emitRunEvent(ctx, tx, it, StageMetadata); err != nil {
		t.Fatal(err)
	}
	if err := tx.InsertNotifier(ctx, store.Notifier{ID: "hook", Kind: "webhook", Nonce: []byte{1}, Ciphertext: []byte{1}, Events: []string{"item.failed"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.p.notify = &notify.Outbox{Clock: e.clk}
	if err := e.st.Tx(ctx, func(r *store.Repo) error { return e.p.emitRunEvent(ctx, r, it, StageMetadata) }); err != nil {
		t.Fatal(err)
	}
	rows, err := e.st.Read().ListOutbox(ctx, "", 10)
	if err != nil || len(rows) != 1 || rows[0].Event != "item.failed" || rows[0].DedupeKey != "item.failed:"+it.ID+":metadata" {
		t.Fatalf("outbox = %+v %v", rows, err)
	}
	var ev notify.Event
	if err := json.Unmarshal([]byte(rows[0].PayloadJSON), &ev); err != nil || ev.Item == nil || ev.Item.Title != "T" || ev.Item.Channel != "Chan" || ev.Item.Ref != "yt:abc123xxxxx" {
		t.Errorf("payload = %s %v", rows[0].PayloadJSON, err)
	}
	// 沒有事件時不寫入（例如只有 pending 的影音）。
	it2 := e.add(ctx, tx, lItem{platform: "youtube", ext: "def456xxxxx", stages: map[string]string{StageMetadata: store.StatusPending}})
	if err := e.p.emitRunEvent(ctx, tx, it2, StageMetadata); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.st.Read().ListOutbox(ctx, "", 10); len(rows) != 1 {
		t.Errorf("不應多寫：%+v", rows)
	}
}
