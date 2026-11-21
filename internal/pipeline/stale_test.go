package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// preferAuto 是把字幕優先順序改成「原文自動字幕」的單次覆寫。
var preferAuto = map[string]any{
	"language": map[string]any{"caption_priority": []any{map[string]any{"lang": "original", "kind": "auto"}}},
}

// currentVariant 回傳某個變體唯一的現行產出；不是剛好一個就結束測試。
func currentVariant(t *testing.T, r *rig, itemID, variant string) store.Artifact {
	t.Helper()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), itemID, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Artifact
	for _, a := range arts {
		if a.Variant == variant {
			out = append(out, a)
		}
	}
	if len(out) != 1 {
		t.Fatalf("%s 的現行產出 = %d 個：%+v", variant, len(out), out)
	}
	return out[0]
}

func (r *rig) rerun(itemID, from string, overrides map[string]any) {
	r.t.Helper()
	if _, err := r.p.Rerun(context.Background(), itemID, RerunRequest{From: from, Overrides: overrides}); err != nil {
		r.t.Fatal(err)
	}
	r.run()
}

// 設定 A → 產出 A；改設定重跑 → 產出 B、A 被取代；改回設定 A → 命中 A，現行版本必須回到 A。
func TestCacheHitRestoresSupersededArtifactAsCurrent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.tagKind = true
	r.yt.info.Language = "en"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}, {Lang: "en", Kind: "auto"}}
	it := r.ingest(watch, nil, "")
	r.run()
	srcA, normA := currentVariant(t, r, it.ID, "source"), currentVariant(t, r, it.ID, "normalized")

	r.rerun(it.ID, StageTranscript, preferAuto)
	srcB := currentVariant(t, r, it.ID, "source")
	if srcB.ID == srcA.ID || srcB.SHA256 == srcA.SHA256 {
		t.Fatalf("設定 B 應產生不同的產出：A=%+v B=%+v", srcA, srcB)
	}
	fetched := len(r.yt.fetched)

	r.rerun(it.ID, StageTranscript, nil) // 改回設定 A
	if len(r.yt.fetched) != fetched {
		t.Errorf("命中快取不應重新下載：%d → %d", fetched, len(r.yt.fetched))
	}
	if got := currentVariant(t, r, it.ID, "source"); got.ID != srcA.ID {
		t.Errorf("現行 source = %s，預期回到 A（%s）", got.ID, srcA.ID)
	}
	if got := currentVariant(t, r, it.ID, "normalized"); got.ID != normA.ID {
		t.Errorf("現行 normalized = %s，預期回到 A（%s）", got.ID, normA.ID)
	}
	runs, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
	if last := latestRuns(runs)[StageTranscript]; last.ArtifactID != srcA.ID {
		t.Errorf("最新的 transcript 步驟指向 %s，預期 %s", last.ArtifactID, srcA.ID)
	}
	if b, _ := r.st.Read().Artifact(ctx, srcB.ID); b.SupersededBy != srcA.ID {
		t.Errorf("B 應被 A 取代：%+v", b)
	}
	if a, _ := r.st.Read().Artifact(ctx, srcA.ID); a.SupersededBy != "" {
		t.Errorf("A 不應再是被取代狀態：%+v", a)
	}
	// 也要能經由 API 讀到 A 的內容。
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) == 0 || !strings.Contains(tr.Cues[0].Text, "manual") {
		t.Errorf("Cues = %+v, %v", tr, err)
	}
}

// 快取鍵不含標題這類 metadata，但含實際的字幕軌清單。
func TestTranscriptCacheKeyIgnoresTitleButNotTrackList(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Language = "en"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	versions := func() int {
		n, err := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "source", "")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if len(r.yt.fetched) != 1 || versions() != 1 {
		t.Fatalf("初始：下載 %d 次、%d 版", len(r.yt.fetched), versions())
	}

	// 只改標題：快取仍然有效。
	r.yt.info.Title = "A brand new title"
	r.rerun(it.ID, StageMetadata, nil)
	if got, _ := r.st.Read().ItemByID(ctx, it.ID); got.Title != "A brand new title" {
		t.Fatalf("標題沒有更新：%q", got.Title)
	}
	if len(r.yt.fetched) != 1 || versions() != 1 {
		t.Errorf("只改標題不應讓快取失效：下載 %d 次、%d 版", len(r.yt.fetched), versions())
	}

	// 新增字幕軌：快取失效，重新下載並產生新版本。
	r.yt.info.Tracks = append(r.yt.info.Tracks, youtube.Track{Lang: "en", Kind: "auto"})
	r.rerun(it.ID, StageMetadata, nil)
	if len(r.yt.fetched) != 2 || versions() != 2 {
		t.Errorf("新增字幕軌應讓快取失效：下載 %d 次、%d 版", len(r.yt.fetched), versions())
	}
}

// 舊任務比新的重跑晚完成：它的結果必須丟棄，不能蓋掉新的現行版本，也不留下孤兒檔。
func TestStaleStageRunResultIsDiscarded(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.tagKind = true
	r.yt.info.Language = "en"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}, {Lang: "en", Kind: "auto"}}
	it := r.ingest(watch, nil, "metadata")
	r.run()

	// 第一次重跑（設定 A，選人工字幕）：任務被領取，先不處理。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	oldTask := claimed[0]

	// 第二次重跑（設定 B，選自動字幕）先完成。
	r.rerun(it.ID, StageTranscript, preferAuto)
	srcB := currentVariant(t, r, it.ID, "source")

	// 舊任務現在才完成。
	if err := r.p.Handle(ctx, oldTask); err != nil {
		t.Fatal(err)
	}

	if got := currentVariant(t, r, it.ID, "source"); got.ID != srcB.ID {
		t.Errorf("舊結果蓋掉了新的現行版本：現行 %s，預期 %s", got.ID, srcB.ID)
	}
	for _, v := range []string{"source", "normalized"} {
		if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", v, ""); n != 1 {
			t.Errorf("%s 版本數 = %d，預期 1", v, n)
		}
	}
	files, _ := os.ReadDir(filepath.Join(r.dir, artifactsDir, it.ID))
	if len(files) != 2 {
		t.Errorf("產出檔 %d 個，預期 2（舊結果的檔案要清掉）", len(files))
	}

	runs, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
	var tr []store.StageRun
	for _, s := range runs {
		if s.Stage == StageTranscript {
			tr = append(tr, s)
		}
	}
	if len(tr) != 3 || tr[0].Status != store.StatusSkipped || tr[1].Status != store.StatusSkipped || tr[1].ArtifactID != "" || tr[2].Status != store.StatusDone {
		t.Errorf("transcript 步驟 = %+v", tr)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskLeased); n != 0 {
		t.Errorf("舊任務不應維持 leased：%d", n)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 0 {
		t.Errorf("舊任務不應被放回佇列：%d", n)
	}
}

// 被重跑取代的舊步驟不能殘留成 pending。
func TestRerunMarksSupersededPendingRunsSkipped(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	runs, err := r.st.Read().StageRunsByItem(ctx, it.ID)
	if err != nil || len(runs) != 2*len(Stages) {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}
	latest := latestRuns(runs)
	for _, s := range runs {
		want := store.StatusSkipped
		if latest[s.Stage].ID == s.ID {
			want = store.StatusPending
		}
		if s.Status != want {
			t.Errorf("%s %s：狀態 %s，預期 %s", s.Stage, s.ID, s.Status, want)
		}
	}
	// 取消的任務不會被執行：只有最新那一筆 metadata 會跑。
	r.run()
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskDone); n != 3 {
		t.Errorf("完成的任務 = %d，預期 3", n)
	}
}

// from 在 until 之後沒有意義：回傳 422（PIPE-R1）。
func TestRerunRejectsFromAfterUntil(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "metadata")
	var inv *InvalidError
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Until: StageTranscript}); !errors.As(err, &inv) {
		t.Fatalf("err = %v，預期 *InvalidError", err)
	}
	// 沒有留下副作用。
	runs, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
	if len(runs) != len(Stages) {
		t.Errorf("被拒絕的重跑不應建立步驟：%d", len(runs))
	}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Errorf("from == until 應該可以：%v", err)
	}
}
