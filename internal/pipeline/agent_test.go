package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

func agentCorrect() map[string]any {
	return stagesOver("correct", map[string]any{"executor": "agent"})
}

// newAgentRig 是有 prompt 庫的 rig：agent 的 plan 要讀 prompt 才能切 chunk、組固定前綴。
func newAgentRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) { d.Prompts = staticPrompts{} })
}

// AGT-R1（D15）：executor: agent 的 LLM 步驟前進時，內建 worker 先執行 plan（切 chunk），
// 再為每個 chunk 建立 executor=agent、資源類別 llm 的任務，步驟狀態是 awaiting_agent；
// 前面可以略過的步驟照常 skipped，後面的步驟等它完成。
func TestApplyChain_AgentExecutorAwaitingAgent(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	got := recordEvents(r.st)
	it := r.ingest(watch, agentCorrect(), "")
	r.run()

	if st := latestStage(t, r, it.ID, StageSegment); st.Status != store.StatusSkipped || st.BlockReason != SkipNotConfigured {
		t.Errorf("segment = %+v，預期 skipped（not_configured）", st)
	}
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusAwaitingAgent || st.Executor != ExecutorAgent || st.Message != "" || st.ErrorClass != "" {
		t.Fatalf("correct = %+v，預期 awaiting_agent、executor agent", st)
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Executor != "agent" || tk.ResourceClass != queue.ResourceLLM || tk.Kind != StageCorrect || tk.State != store.TaskPending ||
		tk.Attempts != 0 || tk.ChunkIndex == nil || tk.DeadlineAt == nil || !tk.DeadlineAt.Equal(r.clk.Now().Add(DefaultAgentTimeout)) {
		t.Errorf("correct 任務 = %+v，預期 executor=agent、llm、pending、chunk 任務、24 小時的領取期限", tk)
	}
	// 後面的步驟等它完成，沒有任務。
	for _, s := range []string{StageTranslate, StagePublish} {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusPending || hasTask(t, r, it.ID, s) {
			t.Errorf("%s = %+v，預期 pending 而且沒有任務", s, st)
		}
	}
	if ev := stageEvents(got()); ev["correct/awaiting_agent"] != 1 {
		t.Errorf("correct/awaiting_agent 事件 = %d，預期 1", ev["correct/awaiting_agent"])
	}
	// 詳情的狀態也是 awaiting_agent（API 讀的就是這個）。
	d, err := r.p.Item(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range d.Stages {
		if s.Stage == StageCorrect {
			found = s.Status == store.StatusAwaitingAgent
		}
	}
	if !found {
		t.Errorf("Item 詳情中 correct 不是 awaiting_agent：%+v", d.Stages)
	}
}

// 重跑落在 agent 的步驟不再是 501（stageImplemented 視 agent 為可以前進）：建立 stage run 與 agent 任務；
// 再重跑一次時，舊的 awaiting_agent 步驟被取代（skipped），舊任務不再被領取。
func TestRerun_AgentExecutorIsNotNotImplemented(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()

	// 來源是人工字幕：segment 預設略過（SEG-R1），on_manual: run 才會執行。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSegment, Overrides: stagesOver("segment", map[string]any{"executor": "agent", "on_manual": "run"})}); err != nil {
		t.Fatalf("重跑 agent 步驟：%v", err)
	}
	r.run()
	first := latestStage(t, r, it.ID, StageSegment)
	if first.Status != store.StatusAwaitingAgent || first.Executor != ExecutorAgent {
		t.Fatalf("segment = %+v", first)
	}
	if tk, err := r.st.Read().TaskByStageRun(ctx, first.ID); err != nil || tk.Executor != "agent" || tk.ResourceClass != queue.ResourceLLM {
		t.Fatalf("segment 任務 = %+v, %v", tk, err)
	}

	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSegment, Overrides: stagesOver("segment", map[string]any{"executor": "agent", "on_manual": "run"})}); err != nil {
		t.Fatal(err)
	}
	r.run()
	old, err := r.st.Read().StageRun(ctx, first.ID)
	if err != nil || old.Status != store.StatusSkipped || old.BlockReason != SkipCancelled {
		t.Errorf("被取代的舊步驟 = %+v, %v，預期 skipped（cancelled）", old, err)
	}
	if tk, err := r.st.Read().TaskByStageRun(ctx, first.ID); err != nil || tk.State == store.TaskPending {
		t.Errorf("舊的 agent 任務應被取消：%+v, %v", tk, err)
	}
	if now := latestStage(t, r, it.ID, StageSegment); now.ID == first.ID || now.Status != store.StatusAwaitingAgent {
		t.Errorf("新的 segment = %+v", now)
	}
}

// 內建 worker 只領 builtin 的任務：agent 任務一直是 pending、沒有租約，步驟維持 awaiting_agent，
// 就算時間過了很久、worker 再跑一輪也一樣（AGT-R1：直到任務被領取）。
func TestWorker_DoesNotClaimAgentTasks(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, agentCorrect(), "")
	r.run()
	r.clk.Advance(48 * time.Hour)
	r.run()

	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusAwaitingAgent {
		t.Fatalf("correct = %+v，預期仍是 awaiting_agent", st)
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, st.ID)
	if err != nil || tk.State != store.TaskPending || tk.LeaseToken != "" || tk.Attempts != 0 {
		t.Errorf("agent 任務 = %+v, %v，預期沒有被領取", tk, err)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskLeased); n != 0 {
		t.Errorf("leased 任務 = %d，預期 0", n)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 1 {
		t.Errorf("pending 任務 = %d，預期只有那個 chunk 的 agent 任務（4 句只切成一個 chunk）", n)
	}
	// 前面三個確定性步驟照常完成。
	for _, s := range Stages[:3] {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusDone {
			t.Errorf("%s = %+v", s, st)
		}
	}
}
