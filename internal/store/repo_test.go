package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// seedItem 建立一部影音與它的 metadata 步驟，回傳 item 與 stage run。
func seedItem(t *testing.T, s *Store, ext string) (Item, StageRun) {
	t.Helper()
	ctx := context.Background()
	var it Item
	var sr StageRun
	err := s.Tx(ctx, func(r *Repo) error {
		var err error
		if it, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: ext, URL: "https://www.youtube.com/watch?v=" + ext}); err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, "sha-"+ext, `{}`)
		if err != nil {
			return err
		}
		sr, err = r.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: snap})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return it, sr
}

func TestInsertItemIfAbsentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)

	a, created, err := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"})
	if err != nil || !created {
		t.Fatalf("first insert = %v, %v", created, err)
	}
	if a.ID[14] != '7' {
		t.Errorf("id = %q，預期 UUIDv7", a.ID)
	}
	if !a.CreatedAt.Equal(clk.Now()) {
		t.Errorf("created_at = %v，預期取自注入的 clock %v", a.CreatedAt, clk.Now())
	}
	b, created, err := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "other"})
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("second insert = %+v, %v, %v；預期回傳既有的影音", b, created, err)
	}
	// 分P 不同就是不同的影音。
	if _, created, _ := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "bilibili", ExternalID: "BV1", Part: 1, URL: "u"}); !created {
		t.Error("bilibili p1 應新增")
	}
	if _, created, _ := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "bilibili", ExternalID: "BV1", Part: 2, URL: "u"}); !created {
		t.Error("bilibili p2 應新增")
	}
	items, err := s.Read().ListItems(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("ListItems = %d, %v", len(items), err)
	}
}

func TestUpdateItemMetaKeepsMissingFields(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "abc123")

	dur := 600
	ch, err := s.Write().UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC1", Title: "Chan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write().UpdateItemMeta(ctx, it.ID, ItemMeta{Title: "T", DurationS: &dur, OriginalLang: "en", LangSource: "metadata", ChannelID: ch, Metadata: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	// 第二次沒有提供語言與標題：維持原值。
	if err := s.Write().UpdateItemMeta(ctx, it.ID, ItemMeta{Availability: "public"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().ItemByID(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "T" || got.OriginalLang != "en" || got.LangSource != "metadata" || got.Availability != "public" ||
		got.DurationS == nil || *got.DurationS != 600 || got.ChannelID != ch {
		t.Errorf("item = %+v", got)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "x", URL: "u"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if items, _ := s.Read().ListItems(ctx); len(items) != 0 {
		t.Errorf("回復後不應有影音，實際 %d", len(items))
	}
}

// callback panic 時必須回復並釋放唯一的寫入連線，否則之後的寫入會永遠卡住。
func TestTxRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic 應該被重新拋出")
			}
		}()
		_ = s.Tx(ctx, func(r *Repo) error {
			if _, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "p", URL: "u"}); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, _, err := s.Write().InsertItemIfAbsent(wctx, NewItem{Platform: "youtube", ExternalID: "after", URL: "u"}); err != nil {
		t.Fatalf("panic 之後的寫入應該能完成：%v", err)
	}
	items, _ := s.Read().ListItems(ctx)
	if len(items) != 1 || items[0].ExternalID != "after" {
		t.Errorf("panic 的交易應已回復，實際 %+v", items)
	}
}

func claimOpts(owner string, max int) ClaimOptions {
	return ClaimOptions{Owner: owner, Max: max, Lease: 10 * time.Minute, Executor: "builtin", AgingInterval: 10 * time.Minute}
}

func claim(t *testing.T, s *Store, o ClaimOptions) []Task {
	t.Helper()
	var out []Task
	err := s.Tx(context.Background(), func(r *Repo) error {
		var err error
		out, err = r.ClaimTasks(context.Background(), o)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newTask(t *testing.T, s *Store, sr StageRun, prio int, nb *time.Time) Task {
	t.Helper()
	tk, err := s.Write().InsertTask(context.Background(), NewTask{StageRunID: sr.ID, Kind: "metadata", Executor: "builtin", ResourceClass: "download", Priority: prio, NotBefore: nb})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestClaimTasksOrdersByPriorityAndHonoursNotBefore(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	_, sr1 := seedItem(t, s, "a")
	_, sr2 := seedItem(t, s, "b")
	_, sr3 := seedItem(t, s, "c")

	low := newTask(t, s, sr1, 1, nil)
	manual := newTask(t, s, sr2, 3, nil)
	future := clk.Now().Add(time.Hour)
	later := newTask(t, s, sr3, 3, &future)

	got := claim(t, s, claimOpts("w1", 10))
	if len(got) != 2 || got[0].ID != manual.ID || got[1].ID != low.ID {
		t.Fatalf("領取順序 = %v，預期手動新增在前、not_before 未到的不領取", ids(got))
	}
	for _, g := range got {
		if g.State != TaskLeased || g.LeaseOwner != "w1" || g.LeaseToken == "" || g.Attempts != 1 || g.LeaseUntil == nil {
			t.Errorf("task = %+v", g)
		}
	}
	if got[0].LeaseToken == got[1].LeaseToken {
		t.Error("每個任務應有自己的 lease token")
	}
	run, _ := s.Read().StageRun(ctx, sr2.ID)
	if run.Status != StatusRunning || run.StartedAt == nil {
		t.Errorf("領取後步驟應為 running：%+v", run)
	}
	// 時間到了才能領取。
	if again := claim(t, s, claimOpts("w1", 10)); len(again) != 0 {
		t.Fatalf("不應再有可領取的任務：%v", ids(again))
	}
	clk.Advance(time.Hour)
	if again := claim(t, s, claimOpts("w1", 10)); len(again) != 1 || again[0].ID != later.ID {
		t.Fatalf("not_before 到期後應領取 %s：%v", later.ID, ids(again))
	}
}

func TestClaimTasksAgingLiftsOldLowPriorityTasks(t *testing.T) {
	s, clk := openTest(t)
	_, sr1 := seedItem(t, s, "a")
	_, sr2 := seedItem(t, s, "b")

	old := newTask(t, s, sr1, 1, nil) // 回補
	clk.Advance(21 * time.Minute)     // 等了兩個 aging 區間，優先度 1 + 2 = 3
	manual := newTask(t, s, sr2, 2, nil)

	got := claim(t, s, claimOpts("w1", 1))
	if len(got) != 1 || got[0].ID != old.ID {
		t.Fatalf("aging 之後等待較久的任務應先領取，實際 %v（old=%s manual=%s）", ids(got), old.ID, manual.ID)
	}
}

func TestClaimTasksFiltersAndLimits(t *testing.T) {
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "a")
	for i := 0; i < 3; i++ {
		newTask(t, s, sr, 3, nil)
	}
	if got := claim(t, s, ClaimOptions{Owner: "w", Max: 2, Lease: time.Minute, Executor: "agent"}); len(got) != 0 {
		t.Errorf("執行者不符不應領取：%v", ids(got))
	}
	o := claimOpts("w", 2)
	o.ResourceClasses = []string{"asr"}
	if got := claim(t, s, o); len(got) != 0 {
		t.Errorf("資源類別不符不應領取：%v", ids(got))
	}
	o.ResourceClasses = []string{"download", "asr"}
	if got := claim(t, s, o); len(got) != 2 {
		t.Errorf("Max=2 應只領取 2 個，實際 %d", len(got))
	}
}

func TestReleaseLeasesRestoresTasksWithoutCountingAttempt(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "a")
	tk := newTask(t, s, sr, 3, nil)
	if got := claim(t, s, claimOpts("old-worker", 1)); len(got) != 1 {
		t.Fatal("應領取到任務")
	}

	// 自己持有的租約不放回；別的 worker 持有的放回（PIPE-R11）。
	if n, err := s.Write().ReleaseLeases(ctx, "old-worker"); err != nil || n != 0 {
		t.Fatalf("ReleaseLeases(old-worker) = %d, %v", n, err)
	}
	n, err := s.Write().ReleaseLeases(ctx, "new-worker")
	if err != nil || n != 1 {
		t.Fatalf("ReleaseLeases(new-worker) = %d, %v", n, err)
	}
	got, _ := s.Read().Task(ctx, tk.ID)
	if got.State != TaskPending || got.Attempts != 0 || got.LeaseOwner != "" {
		t.Errorf("task = %+v", got)
	}
	run, _ := s.Read().StageRun(ctx, sr.ID)
	if run.Status != StatusPending {
		t.Errorf("步驟應回到 pending，實際 %s", run.Status)
	}
}

func TestReleaseLeasesReclaimsExpiredLeases(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	_, sr := seedItem(t, s, "a")
	newTask(t, s, sr, 3, nil)
	claim(t, s, claimOpts("w", 1))
	clk.Advance(11 * time.Minute)
	if n, err := s.Write().ReleaseLeases(ctx, "w"); err != nil || n != 1 {
		t.Fatalf("到期的租約應放回：%d, %v", n, err)
	}
}

func TestRequeueTask(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	_, sr := seedItem(t, s, "a")
	tk := newTask(t, s, sr, 3, nil)
	claim(t, s, claimOpts("w", 1))

	nb := clk.Now().Add(30 * time.Second)
	if err := s.Write().RequeueTask(ctx, tk.ID, &nb, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read().Task(ctx, tk.ID)
	if got.State != TaskPending || got.Attempts != 1 || got.NotBefore == nil || !got.NotBefore.Equal(nb) {
		t.Errorf("計入重試：%+v", got)
	}
	claim(t, s, claimOpts("w", 1)) // 未到期，不會領取
	clk.Advance(time.Minute)
	claim(t, s, claimOpts("w", 1))
	if err := s.Write().RequeueTask(ctx, tk.ID, &nb, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read().Task(ctx, tk.ID)
	if got.Attempts != 1 {
		t.Errorf("wait 不計入重試，attempts = %d，預期 1", got.Attempts)
	}
}

func TestNextTaskTime(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	if _, ok, err := s.Read().NextTaskTime(ctx, "builtin"); err != nil || ok {
		t.Fatalf("沒有任務：%v, %v", ok, err)
	}
	_, sr := seedItem(t, s, "a")
	future := clk.Now().Add(time.Hour)
	newTask(t, s, sr, 3, &future)
	at, ok, err := s.Read().NextTaskTime(ctx, "builtin")
	if err != nil || !ok || !at.Equal(future) {
		t.Fatalf("NextTaskTime = %v, %v, %v", at, ok, err)
	}
	newTask(t, s, sr, 3, nil)
	at, _, _ = s.Read().NextTaskTime(ctx, "builtin")
	if !at.Equal(clk.Now()) {
		t.Errorf("已有可領取的任務時應是現在，實際 %v", at)
	}
}

func TestArtifactRequiresProvenanceAndSupersedesPrevious(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	base := NewArtifact{ItemID: it.ID, Kind: "transcript", Variant: "source", Lang: "en", Format: "notesteme-cues+json", Path: "artifacts/x.json", SHA256: "abc", Size: 3}

	for _, bad := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`[]`), json.RawMessage(`null`)} {
		n := base
		n.Provenance = bad
		if _, err := s.Write().InsertArtifact(ctx, n); !errors.Is(err, ErrNoProvenance) {
			t.Errorf("provenance %q：err = %v，預期 ErrNoProvenance", bad, err)
		}
	}
	n := base
	n.Provenance = json.RawMessage(`{"source":"youtube-manual"}`)
	first, err := s.Write().InsertArtifact(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Write().InsertArtifact(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := s.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil || len(cur) != 1 || cur[0].ID != second.ID {
		t.Fatalf("現行版本 = %+v, %v", cur, err)
	}
	old, _ := s.Read().Artifact(ctx, first.ID)
	if old.SupersededBy != second.ID {
		t.Errorf("舊版本的 superseded_by = %q，預期 %s", old.SupersededBy, second.ID)
	}
	if v, _ := s.Read().ArtifactVersions(ctx, it.ID, "transcript", "source", ""); v != 2 {
		t.Errorf("版本數 = %d，預期 2", v)
	}
}

func TestLatestProbesReturnsOnlyTheLastRun(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "a")
	w := s.Write()
	if err := w.InsertProbes(ctx, it.ID, []NewProbe{{SourceID: "youtube-manual", State: "unavailable"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertProbes(ctx, it.ID, []NewProbe{
		{SourceID: "youtube-manual", Lang: "en", Kind: "manual", Available: true, State: "used"},
		{SourceID: "supadata", State: "unavailable", Reason: "not_configured"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().LatestProbes(ctx, it.ID)
	if err != nil || len(got) != 2 || got[0].SourceID != "youtube-manual" || got[0].State != "used" || got[1].SourceID != "supadata" {
		t.Fatalf("LatestProbes = %+v, %v", got, err)
	}
}

func TestSnapshotsAreDeduplicatedBySHA(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a, err := s.Write().EnsureSnapshot(ctx, "sha", `{"language":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Write().EnsureSnapshot(ctx, "sha", `{"language":{}}`)
	if a != b {
		t.Errorf("相同 sha256 應回傳同一份快照：%s vs %s", a, b)
	}
	if cfg, err := s.Read().Snapshot(ctx, a); err != nil || cfg != `{"language":{}}` {
		t.Errorf("Snapshot = %q, %v", cfg, err)
	}
	if _, err := s.Read().Snapshot(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestDoneStageByCacheKey(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, sr := seedItem(t, s, "a")
	if _, err := s.Read().DoneStageByCacheKey(ctx, it.ID, "metadata", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	w := s.Write()
	_ = w.SetStageResult(ctx, sr.ID, StageResult{Executor: "builtin", CacheKey: "k", ImplVersion: 1})
	_ = w.SetStageState(ctx, sr.ID, StageState{Status: StatusDone})
	got, err := s.Read().DoneStageByCacheKey(ctx, it.ID, "metadata", "k")
	if err != nil || got.ID != sr.ID || got.FinishedAt == nil {
		t.Fatalf("DoneStageByCacheKey = %+v, %v", got, err)
	}
}

func ids(ts []Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
