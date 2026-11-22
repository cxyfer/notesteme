package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// m2Stages 是 M1 之後新增的步驟。
var m2Stages = []string{StageSegment, StageCorrect, StageTranslate, StagePublish}

// hasTask 回報步驟最新的一筆是否建立過任務。
func hasTask(t *testing.T, r *rig, itemID, stage string) bool {
	t.Helper()
	_, err := r.st.Read().TaskByStageRun(context.Background(), latestStage(t, r, itemID, stage).ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// stageEvents 統計 stage.status 事件：key 是「步驟/狀態」。
func stageEvents(evs []events.Event) map[string]int {
	out := map[string]int{}
	for _, e := range evs {
		if e.Type == events.TypeStageStatus {
			out[e.Stage+"/"+e.Status]++
		}
	}
	return out
}

// 完整跑一次：M2 的 4 個步驟依序 skipped，不建任務、不帶 message（PIPE-R1、MCP-R5）。
func TestFullRunSkipsUnconfiguredStagesWithoutTasks(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	got := recordEvents(r.st)
	it := r.ingest(watch, nil, "")
	r.run()

	wantReason := map[string]string{
		StageSegment: SkipNotConfigured, StageCorrect: SkipNotConfigured, StageTranslate: SkipNotConfigured, StagePublish: SkipNoOutputs,
	}
	wantExecutor := map[string]string{StageSegment: ExecutorAPI, StageCorrect: ExecutorAPI, StageTranslate: ExecutorAPI, StagePublish: ExecutorLocal}
	for _, s := range m2Stages {
		st := latestStage(t, r, it.ID, s)
		if st.Status != store.StatusSkipped || st.BlockReason != wantReason[s] || st.Executor != wantExecutor[s] {
			t.Errorf("%s = status %q, reason %q, executor %q", s, st.Status, st.BlockReason, st.Executor)
		}
		// skipped 不帶 message 與 actions，才不會蓋掉 done 步驟上的提醒（SRC-R15）。
		if st.Message != "" || len(st.Actions) != 0 || st.ErrorClass != "" {
			t.Errorf("%s 不該帶 message 或 actions：%+v", s, st)
		}
		if st.StartedAt != nil || st.FinishedAt == nil {
			t.Errorf("%s 沒有開始時間、有結束時間：%+v", s, st)
		}
		if hasTask(t, r, it.ID, s) {
			t.Errorf("%s 不該有任務", s)
		}
	}
	for _, s := range Stages[:3] {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusDone || st.Executor != ExecutorLocal {
			t.Errorf("%s = %+v", s, st)
		}
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskDone); n != 3 {
		t.Errorf("完成的任務 = %d，預期 3", n)
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 0 {
		t.Errorf("pending 任務 = %d，預期 0", n)
	}
	// 每個步驟剛好一個 skipped 事件，事件格式不變（不帶原因）。
	ev := stageEvents(got())
	for _, s := range m2Stages {
		if ev[s+"/skipped"] != 1 || ev[s+"/pending"] != 1 {
			t.Errorf("%s 的事件：pending %d、skipped %d，預期各 1", s, ev[s+"/pending"], ev[s+"/skipped"])
		}
	}
}

// until 之前的步驟前進，之後的維持 pending、不建任務（PIPE-R1）。
func TestAdvanceStopsAtUntil(t *testing.T) {
	cases := []struct {
		until    string
		skipped  []string
		pending  []string
		finished bool
	}{
		{StageSegment, []string{StageSegment}, []string{StageCorrect, StageTranslate, StagePublish}, true},
		{StageCorrect, []string{StageSegment, StageCorrect}, []string{StageTranslate, StagePublish}, true},
		{StageNormalize, nil, m2Stages, true},
	}
	for _, c := range cases {
		r := newRig(t)
		r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
		it := r.ingest(watch, nil, c.until)
		r.run()
		for _, s := range c.skipped {
			if st := latestStage(t, r, it.ID, s); st.Status != store.StatusSkipped || st.BlockReason != SkipNotConfigured {
				t.Errorf("until=%s：%s = %+v，預期 skipped", c.until, s, st)
			}
		}
		for _, s := range c.pending {
			if st := latestStage(t, r, it.ID, s); st.Status != store.StatusPending || st.BlockReason != "" || st.Executor != "" {
				t.Errorf("until=%s：%s = %+v，預期 pending", c.until, s, st)
			}
			if hasTask(t, r, it.ID, s) {
				t.Errorf("until=%s：%s 不該有任務", c.until, s)
			}
		}
		if st := latestStage(t, r, it.ID, StageNormalize); st.Status != store.StatusDone {
			t.Errorf("until=%s：normalize = %+v", c.until, st)
		}
	}
}

// 步驟可以執行（overrides 指定了 provider 與 model）但還沒有 api 實作（不在 llmStages）時建立 llm 任務；
// 任務以 permanent 失敗，訊息告訴使用者步驟還沒有實作。（所有 LLM 步驟都已登記，所以暫時移除 translate 來走這條路徑。）
func TestRunnableLLMStageCreatesLLMTaskThatFailsPermanently(t *testing.T) {
	saved := llmStages[StageTranslate]
	delete(llmStages, StageTranslate)
	t.Cleanup(func() { llmStages[StageTranslate] = saved })
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	over := stagesOver("translate", map[string]any{"provider": "anthropic", "model": "m1"})
	it := r.ingest(watch, over, "")
	r.run()

	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusFailed || st.ErrorClass != string(errclass.ClassPermanent) || st.Message != "這個步驟還沒有實作（translate／api）。" {
		t.Fatalf("translate = %+v", st)
	}
	if st.Executor != ExecutorAPI {
		t.Errorf("executor = %q", st.Executor)
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, st.ID)
	if err != nil || tk.ResourceClass != queue.ResourceLLM || tk.State != store.TaskFailed {
		t.Fatalf("translate 任務 = %+v, %v", tk, err)
	}
	// 失敗之後不再往下前進。
	for _, s := range []string{StagePublish} {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusPending || hasTask(t, r, it.ID, s) {
			t.Errorf("%s = %+v", s, st)
		}
	}
}
