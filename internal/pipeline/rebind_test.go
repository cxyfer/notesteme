package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// snapshotsOf 回傳影音各步驟最新一筆 stage run 綁定的快照 ID。
func snapshotsOf(t *testing.T, r *rig, itemID string) map[string]string {
	t.Helper()
	runs, err := r.st.Read().StageRunsByItem(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for s, run := range latestRuns(runs) {
		out[s] = run.ConfigSnapshotID
	}
	return out
}

// distinct 回傳 m 的值有幾種。
func distinct(m map[string]string) int {
	seen := map[string]bool{}
	for _, v := range m {
		seen[v] = true
	}
	return len(seen)
}

// 頻道要到 metadata 完成才知道（D6）：新增時同一批步驟共用一份沒有頻道層的快照；
// metadata 完成之後，還沒開始的下游步驟改綁到加上頻道層的新快照，metadata 自己維持原來的。
func TestRebind_AfterMetadataAppliesChannel(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.ChannelID, r.yt.info.Channel = "UCfix", "Fixture"
	ov, err := r.p.PatchChannelOverrides(ctx, "youtube:UCfix", timingCfg("forbid"))
	if err != nil {
		t.Fatal(err)
	}
	// 沒有 yt-dlp 探測：新增當下不知道頻道，所以沒有頻道層。
	it := r.ingest(watch, nil, "")
	before := snapshotsOf(t, r, it.ID)
	if distinct(before) != 1 {
		t.Fatalf("新增時同一批步驟應共用一份快照：%v", before)
	}
	if l := layersOf(t, r, it.ID, StageTranscript); l.Channel.ID != "" || splitTimingOf(t, r, it.ID, StageTranscript) != nil {
		t.Fatalf("新增時不該有頻道層：%+v", l)
	}

	r.run()
	after := snapshotsOf(t, r, it.ID)
	if after[StageMetadata] != before[StageMetadata] {
		t.Errorf("metadata 的快照不該改變：%s → %s", before[StageMetadata], after[StageMetadata])
	}
	newSnap := after[StageTranscript]
	if newSnap == before[StageTranscript] {
		t.Fatal("下游步驟應改綁到新快照")
	}
	for _, s := range Stages[1:] {
		if after[s] != newSnap {
			t.Errorf("%s 綁定 %s，預期同一份新快照 %s", s, after[s], newSnap)
		}
	}
	for _, s := range Stages[1:] {
		if got := splitTimingOf(t, r, it.ID, s); got != "forbid" {
			t.Errorf("%s 的 split_timing = %v，預期 forbid（頻道層）", s, got)
		}
	}
	if got := splitTimingOf(t, r, it.ID, StageMetadata); got != nil {
		t.Errorf("metadata 的設定不含頻道層：%v", got)
	}
	l := layersOf(t, r, it.ID, StageTranscript)
	if l.Channel.ID != ov.ChannelID || l.Profile.Name != "default" {
		t.Errorf("layers = %+v，預期頻道 %s、處理方案 default", l, ov.ChannelID)
	}
	if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.ChannelID != ov.ChannelID {
		t.Errorf("item 的頻道 = %q，預期用 natural key 建立的那一個 %q", cur.ChannelID, ov.ChannelID)
	}
}

// until=metadata 時也要改綁：改綁在 nextStage 檢查之前。
func TestRebind_AppliesEvenWhenUntilMetadata(t *testing.T) {
	r := newRig(t)
	r.yt.info.ChannelID = "UCfix"
	if _, err := r.p.PatchChannelOverrides(context.Background(), "youtube:UCfix", timingCfg("free")); err != nil {
		t.Fatal(err)
	}
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	if st := latestStage(t, r, it.ID, StageTranscript); st.Status != store.StatusPending {
		t.Fatalf("until=metadata 之後的步驟應維持 pending：%+v", st)
	}
	if got := splitTimingOf(t, r, it.ID, StageTranscript); got != "free" {
		t.Errorf("until=metadata 時下游的 split_timing = %v，預期 free", got)
	}
}

// 沒有設定任何頻道覆寫：頻道一確定還是會改綁（layers 記下頻道），但生效設定（config_json）不變。
func TestRebind_WithoutOverridesKeepsConfig(t *testing.T) {
	r := newRig(t)
	r.yt.info.ChannelID = "UCplain"
	it := r.ingest(watch, nil, "")
	cfgBefore, _, err := r.p.StageConfig(context.Background(), it.ID, StageTranscript)
	if err != nil {
		t.Fatal(err)
	}
	r.run()
	cfgAfter, _, err := r.p.StageConfig(context.Background(), it.ID, StageTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Errorf("沒有頻道覆寫時 config_json 不該改變：\n前 %s\n後 %s", cfgBefore, cfgAfter)
	}
	if l := layersOf(t, r, it.ID, StageTranscript); l.Channel.ID == "" {
		t.Error("layers 應記下頻道")
	}
}

// M1 的舊快照（layers_json = '{}'）不重建：下游步驟維持原來的快照。
func TestRebind_LegacySnapshotUntouched(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.ChannelID = "UCfix"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	if _, err := r.p.PatchChannelOverrides(ctx, "youtube:UCfix", timingCfg("forbid")); err != nil {
		t.Fatal(err)
	}
	eff, err := BuildEffective(nil)
	if err != nil {
		t.Fatal(err)
	}
	sha, cfg, err := eff.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var itemID, legacy string
	err = r.st.Tx(ctx, func(tx *store.Repo) error {
		it, _, err := tx.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: watch})
		if err != nil {
			return err
		}
		itemID = it.ID
		if legacy, err = tx.EnsureSnapshot(ctx, sha, cfg); err != nil {
			return err
		}
		var first store.StageRun
		for _, s := range Stages {
			run, err := tx.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: s, Status: store.StatusPending, ConfigSnapshotID: legacy})
			if err != nil {
				return err
			}
			if s == StageMetadata {
				first = run
			}
		}
		_, err = insertStageTask(ctx, tx, it.ID, first, queue.PriorityManual, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r.run()

	for s, snap := range snapshotsOf(t, r, itemID) {
		if snap != legacy {
			t.Errorf("%s 綁定 %s，舊快照不該被改綁（%s）", s, snap, legacy)
		}
	}
	if got := splitTimingOf(t, r, itemID, StageTranscript); got != nil {
		t.Errorf("舊快照不含頻道層：%v", got)
	}
	if st := latestStage(t, r, itemID, StageNormalize); st.Status != store.StatusDone {
		t.Errorf("舊影音照常完成：%+v", st)
	}
}

// 已經開始的步驟不改綁；還沒開始的才改。
func TestRebind_StartedRunsUntouched(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.ChannelID = "UCfix"
	if _, err := r.p.PatchChannelOverrides(ctx, "youtube:UCfix", timingCfg("forbid")); err != nil {
		t.Fatal(err)
	}
	it := r.ingest(watch, nil, "")
	old := snapshotsOf(t, r, it.ID)[StageNormalize]
	// normalize 已經開始（running，有開始時間）；其他步驟還沒有。
	if err := r.st.Write().SetStageState(ctx, r.stage(it, StageNormalize).ID, store.StageState{Status: store.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	r.run()
	after := snapshotsOf(t, r, it.ID)
	if after[StageNormalize] != old {
		t.Errorf("已開始的 normalize 不該改綁：%s → %s", old, after[StageNormalize])
	}
	if after[StageTranscript] == old || after[StageSegment] == old {
		t.Errorf("還沒開始的步驟應該改綁：%v", after)
	}
}

// metadata 完成後重新 Compose 失敗（頻道覆寫在語意上不正確）：保留舊快照，metadata 與整條鏈照常完成。
func TestRebind_ComposeFailureKeepsSnapshot(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.ChannelID = "UCbroken"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	id, err := r.st.Write().UpsertChannel(ctx, store.Channel{Platform: "youtube", ExternalID: "UCbroken"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Write().SetChannelOverrides(ctx, id, `{"language":{"targets":[]}}`); err != nil {
		t.Fatal(err)
	}
	it := r.ingest(watch, nil, "")
	old := snapshotsOf(t, r, it.ID)
	r.run()
	if st := latestStage(t, r, it.ID, StageMetadata); st.Status != store.StatusDone {
		t.Fatalf("metadata = %+v，預期 done", st)
	}
	after := snapshotsOf(t, r, it.ID)
	for s, snap := range after {
		if snap != old[s] {
			t.Errorf("%s 的快照被改動：%s → %s", s, old[s], snap)
		}
	}
	if st := latestStage(t, r, it.ID, StageNormalize); st.Status != store.StatusDone {
		t.Errorf("normalize = %+v，預期 done", st)
	}
}

// 改綁要同步更新交易開始時載入的 latest：後面的前進（planChain、applyChain）讀的是它，
// 漏掉這一步就會用舊快照前進。直接呼叫 rebindChannel，檢查傳進去的 latest。
func TestRebind_UpdatesLatestBeforeAdvance(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	runs, err := r.st.Read().StageRunsByItem(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest := latestRuns(runs)
	meta := latest[StageMetadata]
	oldSnap := meta.ConfigSnapshotID

	// 模擬 metadata 的結果：影音指向有覆寫的頻道。
	chID, err := r.st.Write().UpsertChannel(ctx, store.Channel{Platform: "youtube", ExternalID: "UCfix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Write().SetChannelOverrides(ctx, chID, `{"timing":{"split_timing":"forbid"}}`); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Write().SetItemChannel(ctx, it.ID, chID); err != nil {
		t.Fatal(err)
	}

	err = r.st.Tx(ctx, func(tx *store.Repo) error { return r.p.rebindChannel(ctx, tx, it.ID, meta, latest) })
	if err != nil {
		t.Fatal(err)
	}
	dbSnaps := snapshotsOf(t, r, it.ID)
	newSnap := dbSnaps[StageTranscript]
	if newSnap == oldSnap {
		t.Fatal("資料庫中的下游步驟應該已經改綁")
	}
	for _, s := range Stages[1:] {
		if latest[s].ConfigSnapshotID != newSnap {
			t.Errorf("latest[%s].ConfigSnapshotID = %s，預期同步更新為 %s", s, latest[s].ConfigSnapshotID, newSnap)
		}
	}
	if latest[StageMetadata].ConfigSnapshotID != oldSnap || dbSnaps[StageMetadata] != oldSnap {
		t.Error("metadata 自己不改綁")
	}
	// 同一個頻道再呼叫一次：沒有東西可改，latest 不變。
	again := latest[StageTranscript]
	err = r.st.Tx(ctx, func(tx *store.Repo) error { return r.p.rebindChannel(ctx, tx, it.ID, again, latest) })
	if err != nil || latest[StageTranscript].ConfigSnapshotID != newSnap {
		t.Errorf("重複改綁 = %v、latest = %s", err, latest[StageTranscript].ConfigSnapshotID)
	}
}

// metadata 還沒完成就從下游步驟重跑：新建的 run 綁的是另一份沒有頻道層的快照；
// 頻道確定之後每一份有還沒開始的最新步驟綁定的快照都要併入頻道層，不只 metadata 自己的（Codex P2）。
func TestRebind_RerunSnapshotsGetChannelLayer(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Overrides: timingCfg("free")}); err != nil {
		t.Fatal(err)
	}
	runs, err := r.st.Read().StageRunsByItem(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	latest := latestRuns(runs)
	meta := latest[StageMetadata]
	if latest[StageNormalize].ConfigSnapshotID == meta.ConfigSnapshotID || latest[StageTranscript].ConfigSnapshotID != meta.ConfigSnapshotID {
		t.Fatalf("前提：normalize 之後的步驟應綁另一份快照：%v", snapshotsOf(t, r, it.ID))
	}

	chID, err := r.st.Write().UpsertChannel(ctx, store.Channel{Platform: "youtube", ExternalID: "UCfix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Write().SetChannelOverrides(ctx, chID, `{"timing":{"split_timing":"forbid"}}`); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Write().SetItemChannel(ctx, it.ID, chID); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Tx(ctx, func(tx *store.Repo) error { return r.p.rebindChannel(ctx, tx, it.ID, meta, latest) }); err != nil {
		t.Fatal(err)
	}
	for _, s := range Stages[1:] {
		if l := layersOf(t, r, it.ID, s); l.Channel.ID != chID {
			t.Errorf("%s 的頻道層 = %+v，預期頻道 %s", s, l.Channel, chID)
		}
		if latest[s].ConfigSnapshotID != snapshotsOf(t, r, it.ID)[s] {
			t.Errorf("latest[%s] 沒有同步更新", s)
		}
	}
	// 重跑的覆寫仍在：normalize 之後的步驟用單次覆寫的 free，其餘用頻道的 forbid。
	if got := splitTimingOf(t, r, it.ID, StageNormalize); got != "free" {
		t.Errorf("normalize 的 split_timing = %v，預期單次覆寫的 free", got)
	}
	if got := splitTimingOf(t, r, it.ID, StageTranscript); got != "forbid" {
		t.Errorf("transcript 的 split_timing = %v，預期頻道的 forbid", got)
	}
}
