package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
)

// NFR-REL-1 的回歸（故障注入發現）：選圖任務在寫回之前被 kill -9，重新啟動後重跑選圖時必須還找得到候選。
// 原本 framesSelect 在 commit 之前就刪掉 candidates.json，重跑變成 permanent「找不到候選畫面」，下游一直 pending。
// 現在只在寫回的交易 commit 之後才刪。
func TestFramesSelectIsIdempotentUntilCommit(t *testing.T) {
	r := newFramesRig(t, framesRigOpts{scenes: []fakeScene{{At: 30, Score: 0.9, Color: gridColor(1)}, {At: 200, Score: 0.9, Color: gridColor(2)}}})
	ctx := context.Background()
	if _, err := r.p.Rerun(ctx, r.item.ID, RerunRequest{From: StageFrames}); err != nil {
		t.Fatal(err)
	}
	// 自己領任務：候選任務（以及前面的其他任務）照常執行並寫回，領到選圖任務時停下來測試。
	q := queue.New(r.st, r.clk, "restart-test")
	found := false
	for range 20 {
		claimed, err := q.Claim(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) == 0 {
			break
		}
		tk := claimed[0]
		if tk.Kind != TaskFramesSelect {
			if err := r.p.Handle(ctx, tk); err != nil {
				t.Fatal(err)
			}
			continue
		}
		found = true
		eff := mustEffective(t, stagesOver(StageFrames, map[string]any{"executor": "rules", "min_interval_s": 3600}))
		run := r.stage(r.item, StageFrames)
		var pl framesPayload
		if err := json.Unmarshal(tk.Payload, &pl); err != nil {
			t.Fatal(err)
		}
		cands := filepath.Join(framesDir(r.dir, r.item.ID, pl.Version), candidatesFile)
		// 第一次執行：做完工作但沒有寫回，也不執行任何清理（等同在 stage.mid、stage.commit 被 kill -9）。
		if _, err := r.p.runFrames(ctx, tk, run, r.item, eff); err != nil {
			t.Fatalf("第一次選圖：%v", err)
		}
		if _, err := os.Stat(cands); err != nil {
			t.Fatalf("寫回之前 candidates.json 就不見了：%v", err)
		}
		// 重新啟動後重跑：照樣成功；commit 之後才刪掉中間檔。
		res, err := r.p.runFrames(ctx, tk, run, r.item, eff)
		if err != nil {
			t.Fatalf("重跑選圖：%v", err)
		}
		if res.afterCommit == nil {
			t.Fatal("選圖的結果沒有 afterCommit：candidates.json 不會被清掉")
		}
		res.afterCommit()
		if _, err := os.Stat(cands); !os.IsNotExist(err) {
			t.Errorf("commit 之後 candidates.json 應該被刪掉：%v", err)
		}
		break
	}
	if !found {
		t.Fatal("沒有領到選圖任務")
	}
}
