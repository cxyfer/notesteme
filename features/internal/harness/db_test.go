package harness

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// 啟動並正常關閉一次 server，留下已 migrate 的資料庫；之後直接以唯讀連線查詢。
func TestDBReadersWorkAfterGracefulShutdown(t *testing.T) {
	w := NewT(t)
	srv, err := app.StartServer(context.Background(), w.AppEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 沒有任何資料：查不到步驟，沒有被領取的任務。
	if _, found, err := w.DBStageStatus("item-1", "metadata"); err != nil || found {
		t.Fatalf("DBStageStatus = found %v, %v", found, err)
	}
	if got, err := w.DBLeasedTasks(); err != nil || len(got) != 0 {
		t.Fatalf("DBLeasedTasks = %v, %v", got, err)
	}

	// 以另一個連線寫入資料（server 已關閉，foreign key 預設不檢查）。
	u := url.URL{Scheme: "file", Path: filepath.Join(w.DataDir(), "notesteme.db")}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO stage_runs (id, item_id, stage, status, config_snapshot_id, created_at) VALUES ('r1', 'item-1', 'metadata', 'failed', 's', 1)`,
		`INSERT INTO stage_runs (id, item_id, stage, status, config_snapshot_id, created_at) VALUES ('r2', 'item-1', 'metadata', 'pending', 's', 2)`,
		`INSERT INTO tasks (id, stage_run_id, kind, executor, resource_class, state, priority, created_at) VALUES ('t1', 'r2', 'k', 'e', 'download', 'leased', 0, 1)`,
		`INSERT INTO tasks (id, stage_run_id, kind, executor, resource_class, state, priority, created_at) VALUES ('t2', 'r2', 'k', 'e', 'download', 'leased', 0, 1)`,
		`INSERT INTO tasks (id, stage_run_id, kind, executor, resource_class, state, priority, created_at) VALUES ('t3', 'r2', 'k', 'e', 'asr', 'pending', 0, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// 取最近一次執行的狀態。
	if got, found, err := w.DBStageStatus("item-1", "metadata"); err != nil || !found || got != "pending" {
		t.Fatalf("DBStageStatus = %q, %v, %v；預期 pending", got, found, err)
	}
	got, err := w.DBLeasedTasks()
	if err != nil || len(got) != 1 || got["download"] != 2 {
		t.Fatalf("DBLeasedTasks = %v, %v；預期只有 download 有 2 個", got, err)
	}
}

// 唯讀連線不能寫入。
func TestDBQueryIsReadOnly(t *testing.T) {
	w := NewT(t)
	srv, err := app.StartServer(context.Background(), w.AppEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, `DELETE FROM tasks`)
		return err
	})
	if err == nil {
		t.Fatal("唯讀連線不應該能寫入")
	}
}

// 資料庫不存在時回傳錯誤，而且不會建立它。
func TestDBQueryDoesNotCreateDatabase(t *testing.T) {
	w := NewT(t)
	if _, _, err := w.DBStageStatus("x", "metadata"); err == nil {
		t.Fatal("資料庫不存在應該失敗")
	}
	if _, err := w.DBLeasedTasks(); err == nil {
		t.Fatal("資料庫不存在應該失敗")
	}
	if _, err := os.Stat(filepath.Join(w.DataDir(), "notesteme.db")); !os.IsNotExist(err) {
		t.Fatalf("唯讀查詢不應建立資料庫檔：%v", err)
	}
}
