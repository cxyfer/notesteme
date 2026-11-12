package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type runsBody struct {
	Runs []struct {
		ID          string          `json:"id"`
		Stage       string          `json:"stage"`
		Status      string          `json:"status"`
		StartedAt   time.Time       `json:"started_at"`
		FinishedAt  *time.Time      `json:"finished_at"`
		CacheKey    string          `json:"cache_key"`
		ImplVersion int             `json:"impl_version"`
		Config      json.RawMessage `json:"config"`
	} `json:"runs"`
}

func TestRunsListsExecutedStagesNewestFirstWithConfig(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items",
		`{"url":"https://youtu.be/abc123","overrides":{"language":{"targets":["en"],"fallback":["supadata"]}},"until":"transcript"}`)).Items[0].ID

	// 還沒有步驟開始：空陣列（不是 null）。
	rec := r.owner("GET", "/api/items/"+id+"/runs", "")
	if rec.Code != 200 || rec.Body.String() == "" {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || string(raw["runs"]) != "[]" {
		t.Fatalf("還沒執行時 runs 應是 []：%s", rec.Body)
	}

	r.clk.Advance(time.Second)
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec = r.owner("GET", "/api/items/"+id+"/runs", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	got := decode[runsBody](t, rec)
	// until 是 transcript，所以 normalize 沒有執行，不在紀錄中；最新的 transcript 排在最前面。
	if len(got.Runs) != 2 || got.Runs[0].Stage != "transcript" || got.Runs[1].Stage != "metadata" {
		t.Fatalf("runs = %s", rec.Body)
	}
	run := got.Runs[0]
	if run.ID == "" || run.Status != "done" || run.FinishedAt == nil || run.CacheKey == "" || run.ImplVersion != 1 ||
		!run.StartedAt.Equal(time.Date(2026, 11, 12, 10, 0, 1, 0, time.UTC)) {
		t.Errorf("transcript run = %+v", run)
	}
	var cfg struct {
		Language struct {
			Targets  []string `json:"targets"`
			Fallback []string `json:"fallback"`
		} `json:"language"`
	}
	if err := json.Unmarshal(run.Config, &cfg); err != nil || len(cfg.Language.Targets) != 1 || cfg.Language.Targets[0] != "en" ||
		len(cfg.Language.Fallback) != 1 || cfg.Language.Fallback[0] != "supadata" {
		t.Errorf("config = %s（%v）", run.Config, err)
	}
}

func TestRunsOfUnknownItemIsNotFound(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("GET", "/api/items/nope/runs", "")
	if rec.Code != 404 || decode[Error](t, rec).Code != CodeNotFound {
		t.Errorf("不存在的影音：%d %s", rec.Code, rec.Body)
	}
}

func TestRunsRequiresAuthentication(t *testing.T) {
	r := newItemsRig(t)
	if rec := r.do("GET", "/api/items/nope/runs", "", nil); rec.Code != 401 {
		t.Errorf("未登入：%d %s", rec.Code, rec.Body)
	}
}
