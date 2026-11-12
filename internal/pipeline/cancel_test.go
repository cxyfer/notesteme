package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// 取消：所有未結束的步驟變成 skipped（原因 cancelled），pending 的任務變成 cancelled，之後不會再被領取。
func TestCancelItemSkipsUnfinishedStages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")

	ids, err := r.p.CancelItem(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("取消的任務 = %v，預期只有 metadata 的任務", ids)
	}
	for _, s := range Stages {
		run := r.stage(it, s)
		if run.Status != store.StatusSkipped || run.BlockReason != "cancelled" || run.FinishedAt == nil {
			t.Errorf("步驟 %s 應為 skipped（cancelled）：%+v", s, run)
		}
	}
	if got, _ := r.st.Read().Task(ctx, ids[0]); got.State != store.TaskCancelled {
		t.Errorf("任務應為 cancelled：%+v", got)
	}
	r.run()
	if r.yt.probes != 0 {
		t.Errorf("取消後不應再執行任何步驟，Probe 被呼叫 %d 次", r.yt.probes)
	}

	// 冪等：沒有未結束的步驟時什麼都不做。
	if ids, err := r.p.CancelItem(ctx, it.ID); err != nil || len(ids) != 0 {
		t.Errorf("再取消一次 = %v, %v", ids, err)
	}
}

// 已經完成的步驟保持原狀；只有還沒結束的被取消。
func TestCancelItemKeepsFinishedStages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if ids, err := r.p.CancelItem(ctx, it.ID); err != nil || len(ids) != 0 {
		t.Fatalf("全部完成後取消 = %v, %v", ids, err)
	}
	for _, s := range Stages {
		if run := r.stage(it, s); run.Status != store.StatusDone {
			t.Errorf("已完成的步驟 %s 不應改動：%+v", s, run)
		}
	}
}

func TestCancelItemOfUnknownItem(t *testing.T) {
	r := newRig(t)
	if _, err := r.p.CancelItem(context.Background(), "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v，預期 ErrNotFound", err)
	}
}

// 任務已被領取、還在執行時取消，之後它才做完：晚到的結果整份丟棄，
// 步驟維持 skipped、任務維持 cancelled，也不留下產出（PIPE-R15）。
func TestResultAfterCancelIsDiscarded(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")

	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	running := claimed[0]
	if run := r.stage(it, StageMetadata); run.Status != store.StatusRunning {
		t.Fatalf("領取後步驟應為 running：%+v", run)
	}

	ids, err := r.p.CancelItem(ctx, it.ID)
	if err != nil || len(ids) != 1 || ids[0] != running.ID {
		t.Fatalf("CancelItem = %v, %v", ids, err)
	}

	// 它在取消之前就已經做完，現在才寫回。
	if err := r.p.Handle(ctx, running); err != nil {
		t.Fatalf("租約失效不是錯誤：%v", err)
	}
	if run := r.stage(it, StageMetadata); run.Status != store.StatusSkipped || run.BlockReason != "cancelled" {
		t.Errorf("晚到的結果不應把步驟蓋回 done：%+v", run)
	}
	if got, _ := r.st.Read().Task(ctx, running.ID); got.State != store.TaskCancelled {
		t.Errorf("晚到的結果不應改動任務：%+v", got)
	}
	if arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, ""); len(arts) != 0 {
		t.Errorf("不應留下產出記錄：%+v", arts)
	}
	if files, _ := os.ReadDir(filepath.Join(r.dir, artifactsDir, it.ID)); len(files) != 0 {
		t.Errorf("被丟棄的產出檔要清掉，還有 %d 個", len(files))
	}
}
