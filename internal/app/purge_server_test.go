package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// purgeServer 啟動手動 worker 的 server；cfgYAML 非空時寫成設定檔。
type purgeServer struct {
	*Server
	t   *testing.T
	clk *clock.Fake
	dir string
}

func startPurgeServer(t *testing.T, cfgYAML string) *purgeServer {
	t.Helper()
	env := testEnv(t)
	env.Test.ManualWorker = true
	if cfgYAML != "" {
		if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte(cfgYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := StartServer(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Kill)
	return &purgeServer{Server: srv, t: t, clk: env.Clock.(*clock.Fake), dir: env.Vars["NOTESTEME_DATA"]}
}

// trashedItem 新增一部影音、放幾個檔案，並用 SQL 直接移到垃圾桶（移到垃圾桶屬於 DEL-B1）。
func (s *purgeServer) trashedItem(ext string) store.Item {
	s.t.Helper()
	ctx := context.Background()
	it, _, err := s.store.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "https://www.youtube.com/watch?v=" + ext})
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.store.Write().UpdateItemMeta(ctx, it.ID, store.ItemMeta{Title: "PostgreSQL in 100 Seconds"}); err != nil {
		s.t.Fatal(err)
	}
	for _, sub := range []string{"artifacts", "media", "uploads"} {
		if err := os.MkdirAll(filepath.Join(s.dir, sub, it.ID), 0o700); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, sub, it.ID, "f"), []byte("x"), 0o600); err != nil {
			s.t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		s.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET deleted_at = ? WHERE id = ?`, s.clk.Now().UnixMilli(), it.ID); err != nil {
		s.t.Fatal(err)
	}
	return it
}

func (s *purgeServer) itemExists(id string) bool {
	s.t.Helper()
	_, err := s.store.Read().ItemByID(context.Background(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.t.Fatal(err)
	}
	return err == nil
}

func (s *purgeServer) hasFiles(id string) bool {
	for _, sub := range []string{"artifacts", "media", "uploads"} {
		if _, err := os.Lstat(filepath.Join(s.dir, sub, id)); err == nil {
			return true
		}
	}
	return false
}

// 設定檔的保留天數接到定期清理：worker 每輪執行 housekeeping，滿 7 天（不是 30 天）的影音被永久刪除。
// 時間只靠 fake clock 推進：到期之前不刪，剛好到期才刪。
func TestServerPurgesExpiredTrashUsingConfiguredRetention(t *testing.T) {
	ctx := context.Background()
	s := startPurgeServer(t, "library:\n  trash_retention_days: 7\n")
	it := s.trashedItem("abc123xxxxx")

	if err := s.RunUntilIdle(ctx); err != nil { // 第一次執行：沒有到期的，排定 24 小時後
		t.Fatal(err)
	}
	if v, ok, _ := s.store.GetSetting(ctx, settingTrashCleanupNextAt); !ok || v != "2026-11-13T10:00:00Z" {
		t.Fatalf("trash_cleanup_next_at = %q, %v", v, ok)
	}
	s.clk.Advance(6 * 24 * time.Hour) // 第 6 天
	if err := s.RunUntilIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.itemExists(it.ID) || !s.hasFiles(it.ID) {
		t.Fatal("還沒滿 7 天，不該永久刪除")
	}
	s.clk.Advance(24 * time.Hour) // 剛好 7 天
	if err := s.RunUntilIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if s.itemExists(it.ID) || s.hasFiles(it.ID) {
		t.Errorf("滿 7 天應該永久刪除：影音存在 = %v，檔案存在 = %v", s.itemExists(it.ID), s.hasFiles(it.ID))
	}
}

// 預設保留 30 天：第 29 天還在，第 30 天刪除。
func TestServerPurgesExpiredTrashAfter30DaysByDefault(t *testing.T) {
	ctx := context.Background()
	s := startPurgeServer(t, "")
	it := s.trashedItem("abc123xxxxx")
	for _, step := range []struct {
		advance time.Duration
		want    bool // 之後影音是否還在
	}{{0, true}, {29 * 24 * time.Hour, true}, {24 * time.Hour, false}} {
		s.clk.Advance(step.advance)
		if err := s.RunUntilIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if got := s.itemExists(it.ID); got != step.want {
			t.Fatalf("推進 %v 之後影音存在 = %v，預期 %v", step.advance, got, step.want)
		}
	}
}

// 保留天數超出範圍時拒絕啟動，訊息指出欄位；失敗後資料目錄的鎖已釋放，修正設定之後可以重新啟動。
func TestStartServerRejectsInvalidTrashRetention(t *testing.T) {
	for _, v := range []string{"0", "3651", "-5", "many"} {
		env := testEnv(t)
		if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte("library:\n  trash_retention_days: "+v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := StartServer(context.Background(), env)
		if err == nil || !strings.Contains(err.Error(), "library.trash_retention_days") {
			t.Errorf("值 %s：err = %v，預期拒絕啟動並指出 library.trash_retention_days", v, err)
		}
		if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte("library:\n  trash_retention_days: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		srv, err := StartServer(context.Background(), env)
		if err != nil {
			t.Fatalf("修正之後應該可以啟動：%v", err)
		}
		srv.Kill()
	}
}

func TestTrashRetentionHelper(t *testing.T) {
	s := startPurgeServer(t, "library:\n  trash_retention_days: 3650\n")
	if got := trashRetention(s.Config()); got != 3650*24*time.Hour {
		t.Errorf("trashRetention = %v", got)
	}
	s2 := startPurgeServer(t, "")
	if got := trashRetention(s2.Config()); got != 30*24*time.Hour {
		t.Errorf("預設 trashRetention = %v，預期 720h", got)
	}
}

// 成本帳本的測試 hook：寫入、讀取；永久刪除後紀錄仍在，item_id 為空、標題另存。
func TestSeedCostAndCostEntriesSurvivePurge(t *testing.T) {
	ctx := context.Background()
	s := startPurgeServer(t, "")
	it := s.trashedItem("abc123xxxxx")

	if err := s.SeedCost(ctx, it.ID, 0.42, "actual"); err != nil {
		t.Fatal(err)
	}
	s.clk.Advance(time.Minute)
	if err := s.SeedCost(ctx, it.ID, 1, "reserved"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CostEntries(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("CostEntries = %+v, %v", got, err)
	}
	if got[0].ItemID != it.ID || got[0].USD != 0.42 || got[0].State != "actual" || got[1].State != "reserved" || got[1].USD != 1 {
		t.Errorf("紀錄 = %+v", got)
	}

	if err := s.pipeline.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.CostEntries(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("永久刪除之後 CostEntries = %+v, %v", got, err)
	}
	for _, e := range got {
		if e.ItemID != "" || e.ItemTitle != "PostgreSQL in 100 Seconds" {
			t.Errorf("永久刪除之後的紀錄 = %+v，預期 item_id 為空、標題保留", e)
		}
	}
}

func TestSeedCostRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	s := startPurgeServer(t, "")
	it := s.trashedItem("abc123xxxxx")
	if err := s.SeedCost(ctx, it.ID, 1, "bogus"); !errors.Is(err, store.ErrInvalidCostState) {
		t.Errorf("不認得的狀態：err = %v", err)
	}
	if err := s.SeedCost(ctx, "no-such-item", 1, "actual"); err == nil {
		t.Error("不存在的影音應該失敗（外鍵）")
	}
	if got, _ := s.CostEntries(ctx); len(got) != 0 {
		t.Errorf("失敗不該留下紀錄：%+v", got)
	}
}
