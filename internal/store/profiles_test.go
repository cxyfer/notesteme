package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const emptyConfigSHA = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"

// migration 種入內建的 default：版本 1、config 是 {}、sha256 對 {} 計算。
func TestBuiltinDefaultProfileIsSeeded(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	pr, err := s.Read().Profile(ctx, "default")
	if err != nil || !pr.Builtin || pr.CurrentVersion != 1 {
		t.Fatalf("default = %+v, %v", pr, err)
	}
	pv, err := s.Read().ProfileVersion(ctx, "default", 1)
	if err != nil || pv.ConfigJSON != "{}" || pv.SHA256 != emptyConfigSHA {
		t.Fatalf("default v1 = %+v, %v", pv, err)
	}
	if _, err := s.Read().Profile(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的處理方案 = %v，預期 ErrNotFound", err)
	}
}

// 每次儲存都新增一版（內容相同也一樣），版本遞增且舊版本不變；已存在的名稱是 ErrConflict。
func TestProfileVersionsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	v1, err := w.CreateProfile(ctx, "standard", `{"a":1}`, shaA)
	if err != nil || v1.Version != 1 {
		t.Fatalf("CreateProfile = %+v, %v", v1, err)
	}
	if _, err := w.CreateProfile(ctx, "standard", `{}`, shaB); !errors.Is(err, ErrConflict) {
		t.Fatalf("重複建立 = %v，預期 ErrConflict", err)
	}
	clk.Advance(time.Second)
	v2, err := w.AddProfileVersion(ctx, "standard", `{"a":1}`, shaA) // 內容相同也建立新版本
	if err != nil || v2.Version != 2 {
		t.Fatalf("AddProfileVersion = %+v, %v", v2, err)
	}
	if _, err := w.AddProfileVersion(ctx, "missing", `{}`, shaA); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的處理方案新增版本 = %v，預期 ErrNotFound", err)
	}

	pr, err := s.Read().Profile(ctx, "standard")
	if err != nil || pr.CurrentVersion != 2 || pr.Builtin {
		t.Fatalf("Profile = %+v, %v", pr, err)
	}
	if !pr.UpdatedAt.After(pr.CreatedAt) {
		t.Errorf("updated_at %v 應晚於 created_at %v", pr.UpdatedAt, pr.CreatedAt)
	}
	vs, err := s.Read().ProfileVersions(ctx, "standard")
	if err != nil || len(vs) != 2 || vs[0].Version != 1 || vs[1].Version != 2 {
		t.Fatalf("ProfileVersions = %+v, %v", vs, err)
	}
	all, err := s.Read().Profiles(ctx)
	if err != nil || len(all) != 2 || all[0].Name != "default" || all[1].Name != "standard" {
		t.Fatalf("Profiles = %+v, %v（預期依名稱排序）", all, err)
	}
	cur, err := s.Read().CurrentProfileConfigs(ctx)
	if err != nil || cur["standard"] != `{"a":1}` || cur["default"] != "{}" || len(cur) != 2 {
		t.Fatalf("CurrentProfileConfigs = %v, %v", cur, err)
	}
}

// 刪除：版本一併刪除（外鍵 CASCADE）；內建的 default 刪不掉。
func TestDeleteProfileCascadesAndKeepsBuiltin(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	if _, err := w.CreateProfile(ctx, "tmp", `{}`, shaA); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddProfileVersion(ctx, "tmp", `{}`, shaA); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.DeleteProfile(ctx, "tmp"); err != nil || !ok {
		t.Fatalf("DeleteProfile = %v, %v", ok, err)
	}
	if vs, err := s.Read().ProfileVersions(ctx, "tmp"); err != nil || len(vs) != 0 {
		t.Errorf("刪除後版本 = %+v, %v，預期 0 筆", vs, err)
	}
	if ok, err := w.DeleteProfile(ctx, "tmp"); err != nil || ok {
		t.Errorf("再刪一次 = %v, %v，預期 false", ok, err)
	}
	if ok, err := w.DeleteProfile(ctx, "default"); err != nil || ok {
		t.Errorf("刪除內建的 default = %v, %v，預期 false", ok, err)
	}
	if _, err := s.Read().Profile(ctx, "default"); err != nil {
		t.Errorf("default 應還在：%v", err)
	}
}

// 建立與新增版本在同一個交易中：交易回復時兩者都不留下。
func TestCreateProfileRollsBackWithTx(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, err := r.CreateProfile(ctx, "gone", `{}`, shaA); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := s.Read().Profile(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rollback 後處理方案 = %v，預期 ErrNotFound", err)
	}
}

// 影音記錄處理方案與版本；M1 的影音沒有記錄（0）。
func TestItemProfileVersion(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	it, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "a", URL: "u", ProfileID: "default", ProfileVersion: 1})
	if err != nil || it.ProfileID != "default" || it.ProfileVersion != 1 {
		t.Fatalf("item = %+v, %v", it, err)
	}
	old, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "b", URL: "u"})
	if err != nil || old.ProfileID != "" || old.ProfileVersion != 0 {
		t.Fatalf("沒有處理方案的影音 = %+v, %v", old, err)
	}
	if err := w.SetItemProfile(ctx, old.ID, "standard", 3); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Read().ItemByID(ctx, old.ID); got.ProfileID != "standard" || got.ProfileVersion != 3 {
		t.Errorf("SetItemProfile 之後 = %+v", got)
	}
	if err := w.SetItemProfile(ctx, "missing", "standard", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的影音 = %v，預期 ErrNotFound", err)
	}
}

// 頻道覆寫：預設 {}；寫入整份取代；UpsertChannel 不會蓋掉它；ChannelsWithOverrides 只列有覆寫的。
func TestChannelOverrides(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	id, err := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC1", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC2"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.Read().ChannelByID(ctx, id)
	if err != nil || string(ch.Overrides) != "{}" {
		t.Fatalf("ChannelByID = %+v, %v", ch, err)
	}
	if err := w.SetChannelOverrides(ctx, id, `{"language":{"targets":["ja"]}}`); err != nil {
		t.Fatal(err)
	}
	// metadata 再次 upsert 同一個頻道：title 更新，覆寫保留。
	if again, err := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "UC1", Title: "new"}); err != nil || again != id {
		t.Fatalf("UpsertChannel = %q, %v", again, err)
	}
	ch, _ = s.Read().ChannelByID(ctx, id)
	if string(ch.Overrides) != `{"language":{"targets":["ja"]}}` || ch.Title != "new" {
		t.Errorf("覆寫被蓋掉或 title 沒更新：%+v", ch)
	}
	byKey, err := s.Read().ChannelByKey(ctx, "youtube", "UC1")
	if err != nil || byKey.ID != id || string(byKey.Overrides) != string(ch.Overrides) {
		t.Errorf("ChannelByKey = %+v, %v", byKey, err)
	}
	list, err := s.Read().ChannelsWithOverrides(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Errorf("ChannelsWithOverrides = %+v, %v（%s 沒有覆寫）", list, err, other)
	}
	if err := w.SetChannelOverrides(ctx, "missing", `{}`); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的頻道 = %v，預期 ErrNotFound", err)
	}
	if _, err := s.Read().ChannelByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ChannelByID(missing) = %v", err)
	}
}

// EnsureSnapshot 保留原簽名（layers_json 是 {}）；EnsureSnapshotLayers 記下各層，相同 sha256 回傳既有的 ID。
func TestSnapshotLayers(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	legacy, err := w.EnsureSnapshot(ctx, shaA, `{"x":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Read().SnapshotLayers(ctx, legacy); err != nil || got != "{}" {
		t.Fatalf("舊式快照的 layers = %q, %v，預期 {}", got, err)
	}
	withLayers, err := w.EnsureSnapshotLayers(ctx, shaB, `{"x":1}`, `{"profile":{"name":"p"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := w.EnsureSnapshotLayers(ctx, shaB, `{"x":1}`, `{"other":true}`); err != nil || again != withLayers {
		t.Errorf("相同 sha256 = %q, %v，預期 %q", again, err, withLayers)
	}
	if got, err := s.Read().SnapshotLayers(ctx, withLayers); err != nil || got != `{"profile":{"name":"p"}}` {
		t.Errorf("layers = %q, %v", got, err)
	}
	if _, err := s.Read().SnapshotLayers(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SnapshotLayers(missing) = %v", err)
	}
}

// 改綁只動同一份快照、沒開始的 pending 下游步驟：metadata、已開始的、其他快照的都不動。
func TestRebindPendingRuns(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	it, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "a", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	from, _ := w.EnsureSnapshot(ctx, shaA, `{}`)
	to, _ := w.EnsureSnapshotLayers(ctx, shaB, `{}`, `{"k":1}`)
	other, _ := w.EnsureSnapshot(ctx, "c"+shaA[1:], `{}`)
	add := func(stage, snap string) StageRun {
		run, err := w.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: stage, Status: StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	meta := add("metadata", from)
	pending := add("transcript", from)
	started := add("normalize", from)
	elsewhere := add("segment", other)
	done := add("correct", from)
	if err := w.SetStageState(ctx, started.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetStageState(ctx, done.ID, StageState{Status: StatusDone}); err != nil {
		t.Fatal(err)
	}

	ids, err := w.RebindPendingRuns(ctx, it.ID, from, to)
	if err != nil || !slices.Equal(ids, []string{pending.ID}) {
		t.Fatalf("RebindPendingRuns = %v, %v，預期只有 %s", ids, err, pending.ID)
	}
	snapOf := func(id string) string {
		run, err := s.Read().StageRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return run.ConfigSnapshotID
	}
	for id, want := range map[string]string{meta.ID: from, pending.ID: to, started.ID: from, elsewhere.ID: other, done.ID: from} {
		if got := snapOf(id); got != want {
			t.Errorf("stage run %s 綁定 %s，預期 %s", id, got, want)
		}
	}
	// 重跑一次沒有東西可改。
	if ids, err := w.RebindPendingRuns(ctx, it.ID, from, to); err != nil || len(ids) != 0 {
		t.Errorf("第二次改綁 = %v, %v", ids, err)
	}
}

// 00010 升級：舊資料取得預設值、default 被種入；降級：欄位與表都移除、原有資料保留；可以再次升級。
func TestMigration00010_DownUp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 9, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO channels (id, platform, external_id, updated_at) VALUES ('ch', 'youtube', 'UC1', 1)`,
		`INSERT INTO config_snapshots (id, sha256, config_json, created_at) VALUES ('snap', 'sha', '{}', 1)`,
		`INSERT INTO items (id, platform, external_id, url, profile_id, created_at) VALUES ('old', 'youtube', 'x', 'u', 'legacy', 1)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	s, err = openWith(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ch, err := s.Read().ChannelByID(ctx, "ch")
	if err != nil || string(ch.Overrides) != "{}" {
		t.Fatalf("升級後的舊頻道 = %+v, %v", ch, err)
	}
	if got, err := s.Read().SnapshotLayers(ctx, "snap"); err != nil || got != "{}" {
		t.Fatalf("升級後的舊快照 layers = %q, %v", got, err)
	}
	it, err := s.Read().ItemByID(ctx, "old")
	if err != nil || it.ProfileID != "legacy" || it.ProfileVersion != 0 {
		t.Fatalf("升級後的舊影音 = %+v, %v", it, err)
	}
	if pr, err := s.Read().Profile(ctx, "default"); err != nil || !pr.Builtin {
		t.Fatalf("升級後應有內建的 default：%+v, %v", pr, err)
	}
	for table, col := range map[string]string{"channels": "overrides_json", "items": "profile_version", "config_snapshots": "layers_json"} {
		if !slices.Contains(columnsOf(t, s, table), col) {
			t.Errorf("%s 缺少欄位 %s", table, col)
		}
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, s.w, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	// 降到 9（不假設 00010 是最新的一支）。
	if _, err := provider.DownTo(ctx, 9); err != nil {
		t.Fatalf("降級：%v", err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 9 {
		t.Fatalf("降級後的版本 = %d, %v；預期 9", v, err)
	}
	for table, col := range map[string]string{"channels": "overrides_json", "items": "profile_version", "config_snapshots": "layers_json"} {
		if slices.Contains(columnsOf(t, s, table), col) {
			t.Errorf("降級後 %s 仍有欄位 %s", table, col)
		}
	}
	if objectExists(t, s, "table", "profiles") || objectExists(t, s, "table", "profile_versions") {
		t.Error("降級後不該留下 profiles 或 profile_versions")
	}
	var n int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id = 'old'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("降級後原有影音 = %d, %v", n, err)
	}
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE id = 'ch'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("降級後原有頻道 = %d, %v", n, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("再次升級：%v", err)
	}
	if pr, err := s.Read().Profile(ctx, "default"); err != nil || pr.CurrentVersion != 1 {
		t.Errorf("再次升級後的 default = %+v, %v", pr, err)
	}
}
