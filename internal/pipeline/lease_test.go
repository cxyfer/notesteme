package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 任務的租約在重啟時被放回並由新的 worker 重新領取之後，舊的 worker 晚到的結果必須整份丟棄：
// 寫回的交易回復（步驟、影音、任務都不變）、產出檔清掉，新的租約不受影響。
func TestLateResultAfterLeaseWasReclaimedIsDiscarded(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "metadata")
	r.run()
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Fatal(err)
	}

	// 舊的 worker 領取了 transcript 任務，還沒做完。
	old := queue.New(r.st, r.clk, "old")
	claimed, err := old.Claim(ctx, 5)
	if err != nil || len(claimed) != 1 || claimed[0].Kind != StageTranscript {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	oldTask := claimed[0]

	// 重啟：新的 worker 放回舊租約並重新領取，換了新的 lease_token。
	fresh := queue.New(r.st, r.clk, "new")
	if n, err := fresh.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStale = %d, %v", n, err)
	}
	again, err := fresh.Claim(ctx, 5)
	if err != nil || len(again) != 1 || again[0].ID != oldTask.ID || again[0].LeaseToken == oldTask.LeaseToken {
		t.Fatalf("重新領取 = %+v, %v", again, err)
	}
	newTask := again[0]

	// 舊的 worker 這時才做完。
	if err := r.p.Handle(ctx, oldTask); err != nil {
		t.Fatalf("租約失效不是錯誤：%v", err)
	}

	got, err := r.st.Read().Task(ctx, oldTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.TaskLeased || got.LeaseToken != newTask.LeaseToken || got.LeaseOwner != "new" {
		t.Errorf("新的租約不應被舊的結果改動：%+v", got)
	}
	if run := latestStage(t, r, it.ID, StageTranscript); run.Status != store.StatusRunning || run.ArtifactID != "" {
		t.Errorf("步驟不應被舊的結果改成完成：%+v", run)
	}
	if arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, ""); len(arts) != 0 {
		t.Errorf("不應留下產出記錄：%+v", arts)
	}
	if files, _ := os.ReadDir(filepath.Join(r.dir, artifactsDir, it.ID)); len(files) != 0 {
		t.Errorf("被丟棄的產出檔要清掉，還有 %d 個", len(files))
	}

	// 新的租約持有者照常完成。
	if err := r.p.Handle(ctx, newTask); err != nil {
		t.Fatal(err)
	}
	if run := latestStage(t, r, it.ID, StageTranscript); run.Status != store.StatusDone || run.ArtifactID == "" {
		t.Errorf("新的租約應能完成：%+v", run)
	}
	if got, _ := r.st.Read().Task(ctx, newTask.ID); got.State != store.TaskDone {
		t.Errorf("任務應完成：%+v", got)
	}
}

// artifactFiles 回傳影音的產出檔數量。
func artifactFiles(r *rig, itemID string) int {
	files, _ := os.ReadDir(filepath.Join(r.dir, artifactsDir, itemID))
	return len(files)
}

// 執行到一半 ctx 被取消（關閉中）：產出檔已經寫出但不會 commit，要清掉，不留孤兒檔。
func TestCancelledContextAfterWritingArtifactLeavesNoOrphanFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "metadata")
	r.run()
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.New(r.st, r.clk, "w2").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 || claimed[0].Kind != StageTranscript {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}

	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.yt.mu.Lock()
	r.yt.onVersion = cancel // 取消發生在字幕下載之後、產出檔寫出之前
	r.yt.mu.Unlock()
	if err := r.p.Handle(hctx, claimed[0]); err != nil {
		t.Fatalf("ctx 取消不是錯誤：%v", err)
	}
	if n := artifactFiles(r, it.ID); n != 0 {
		t.Errorf("ctx 取消後不該留下產出檔，還有 %d 個", n)
	}
	if run := latestStage(t, r, it.ID, StageTranscript); run.Status != store.StatusRunning {
		t.Errorf("取消時不該改變步驟狀態：%+v", run)
	}
}

// 寫回的交易因 ctx 取消而失敗：沒有記錄會指向產出檔，同樣要清掉。
func TestApplyFailureLeavesNoOrphanFile(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "metadata")
	run := r.stage(it, StageMetadata)
	art, err := r.p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource,
		Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "x", Timing: subtitle.TimingSource}}}, nil, json.RawMessage(`{"source":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	if artifactFiles(r, it.ID) != 1 {
		t.Fatal("前置條件：產出檔應已寫出")
	}

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := result{task: taskDone, artifact: art, stage: &store.StageResult{CacheKey: "k"}, state: store.StageState{Status: store.StatusDone}}
	if err := r.p.apply(cctx, store.Task{ID: "t"}, run, it, res); err == nil {
		t.Fatal("ctx 已取消，寫回應該失敗")
	}
	if n := artifactFiles(r, it.ID); n != 0 {
		t.Errorf("寫回失敗後不該留下產出檔，還有 %d 個", n)
	}
}

func TestIngestPriorityDefaultsToManual(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for _, tt := range []struct {
		url      string
		priority int
		want     int
	}{
		{"https://www.youtube.com/watch?v=aaa", 0, queue.PriorityManual},
		{"https://www.youtube.com/watch?v=bbb", queue.PriorityBackfill, queue.PriorityBackfill},
		{"https://www.youtube.com/watch?v=ccc", queue.PrioritySubscription, queue.PrioritySubscription},
	} {
		res, err := r.p.Ingest(ctx, IngestRequest{URL: tt.url, Priority: tt.priority})
		if err != nil {
			t.Fatal(err)
		}
		run, err := r.st.Read().StageRunsByItem(ctx, res[0].Item.ID)
		if err != nil {
			t.Fatal(err)
		}
		var task store.Task
		for _, s := range run {
			if s.Stage == StageMetadata {
				if task, err = r.st.Read().TaskByStageRun(ctx, s.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
		if task.Priority != tt.want {
			t.Errorf("%s 的優先級 = %d，預期 %d", tt.url, task.Priority, tt.want)
		}
	}
}

// latestStage 回傳步驟最新的一筆 stage run（重跑之後同一個步驟有多筆）。
func latestStage(t *testing.T, r *rig, itemID, stage string) store.StageRun {
	t.Helper()
	runs, err := r.st.Read().StageRunsByItem(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	return latestRuns(runs)[stage]
}
