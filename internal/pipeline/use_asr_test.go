package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

// SRC-R6「現在改用語音轉錄」：等待中的 transcript 立刻改用 fallback，不等 2 小時、不消耗重試次數。
func TestUseASRNowSkipsTheWait(t *testing.T) {
	r, asr := newFreshRig(t)
	ctx := context.Background()
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusWaiting {
		t.Fatalf("transcript = %+v", st)
	}
	if got := ActionCodes(st.Actions); !slices.Equal(got, []string{ActionCodeUseASRNow}) {
		t.Errorf("動作代碼 = %v", got)
	}

	run, err := r.p.UseASRNow(ctx, it.ID)
	if err != nil || run.Status != store.StatusPending || run.Stage != StageTranscript {
		t.Fatalf("UseASRNow = %+v，%v", run, err)
	}
	if st := r.stage(it, StageTranscript); st.Status != store.StatusPending || len(st.Actions) != 0 || st.NotBefore != nil {
		t.Errorf("略過後的步驟 = %+v", st)
	}
	// 時鐘沒有前進：任務立刻可以領取。
	r.run()
	st = r.stage(it, StageTranscript)
	if st.Status != store.StatusDone || asr.calls() != 1 {
		t.Fatalf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
	tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID)
	if tk.Attempts != 1 {
		t.Errorf("等待不計入次數，只有真正執行的一次：attempts = %d", tk.Attempts)
	}
	// 已經不是 waiting：409。
	if _, err := r.p.UseASRNow(ctx, it.ID); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("不是 waiting 時應回 ErrNotWaiting：%v", err)
	}
}

// 旗標留在任務的 payload：同一個任務之後被放回佇列（例如 fallback 暫時失敗）再執行時也不會回到等待。
func TestUseASRNowFlagSurvivesRequeue(t *testing.T) {
	r, asr := newFreshRig(t)
	ctx := context.Background()
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	asr.err = &errclass.Error{Class: errclass.ClassRetryable, Source: "asr-fake", Detail: "HTTP 503"}
	if _, err := r.p.UseASRNow(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status == store.StatusWaiting {
		t.Fatalf("暫時失敗後不應回到等待：%+v", st)
	}
	asr.err = nil
	r.clk.Advance(st.NotBefore.Sub(r.clk.Now()) + 1)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 2 {
		t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
}

// 不在等待、影音不存在、任務正被領取：都拒絕；payload 不合法時當成沒有旗標。
func TestUseASRNowRejects(t *testing.T) {
	r, _ := newFreshRig(t)
	ctx := context.Background()
	if _, err := r.p.UseASRNow(ctx, "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageMetadata)
	r.run()
	if _, err := r.p.UseASRNow(ctx, it.ID); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("沒有 transcript：%v", err)
	}
	if skipAutoWait(withTaskFlags(ctx, store.Task{Payload: []byte(`{"skip_auto_wait":"yes"`)})) ||
		skipAutoWait(withTaskFlags(ctx, store.Task{Payload: []byte(`{"skip_auto_wait":false}`)})) ||
		!skipAutoWait(withTaskFlags(ctx, store.Task{Payload: []byte(`{"x":1,"skip_auto_wait":true}`)})) {
		t.Error("withTaskFlags")
	}
	if got := ActionCodes([]string{"重試", "現在改用語音轉錄"}); !slices.Equal(got, []string{ActionCodeUseASRNow}) || ActionCodes(nil) != nil {
		t.Errorf("ActionCodes = %v", got)
	}
}

// 任務正在執行（租約中）時不能略過：執行結束時又會排回等待，旗標會失效。
func TestUseASRNowRejectsLeasedTask(t *testing.T) {
	r, _ := newFreshRig(t)
	ctx := context.Background()
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID)
	// 直接把任務標成被領取（模擬 worker 正在執行）。
	r.clk.Advance(3 * time.Hour)
	claimed, err := r.st.Write().ClaimTasks(ctx, store.ClaimOptions{Owner: "w", Executor: "builtin", Max: 10, Lease: time.Minute, AgingInterval: time.Hour})
	if err != nil || !slices.ContainsFunc(claimed, func(c store.Task) bool { return c.ID == tk.ID }) {
		t.Fatalf("領取 = %+v，%v", claimed, err)
	}
	// 領取時步驟會變成 running：被拒絕。
	if _, err := r.p.UseASRNow(ctx, it.ID); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("執行中的步驟：%v", err)
	}
	// 防護：步驟仍顯示 waiting、任務卻已被領取（狀態不同步）時也拒絕，不改步驟的狀態。
	if err := r.st.Write().SetStageState(ctx, st.ID, store.StageState{Status: store.StatusWaiting}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.UseASRNow(ctx, it.ID); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("租約中的任務：%v", err)
	}
	if got := r.stage(it, StageTranscript); got.Status != store.StatusWaiting {
		t.Errorf("被拒絕時不應改變步驟：%+v", got)
	}
}
