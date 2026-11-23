package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/storetest"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
)

func openTemp(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(context.Background(), dir, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T, clk clock.Clock) hubstore.Store {
		s, err := Open(context.Background(), t.TempDir(), clk)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// DB 檔一律 0600（NFR-SEC-5），資料目錄不存在時建立。
func TestPrivateFilesAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hub")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := openTemp(t, dir)
	fi, err := os.Stat(filepath.Join(dir, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("DB 權限 = %o", fi.Mode().Perm())
	}
	v1, _ := s.SchemaVersion(context.Background())
	_ = s.Close()
	// 重新開啟不會重複套用 migration。
	s2 := openTemp(t, dir)
	if v2, err := s2.SchemaVersion(context.Background()); err != nil || v2 != v1 {
		t.Errorf("重新開啟後版本 = %d, %v，預期 %d", v2, err, v1)
	}
}

// 資料庫比程式新（降版執行）時拒絕啟動。
func TestSchemaTooNew(t *testing.T) {
	dir := t.TempDir()
	s := openTemp(t, dir)
	_ = s.Close()
	db, err := sqlitedb.Open(filepath.Join(dir, DBFileName), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (999, 1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err = Open(context.Background(), dir, clock.Real{})
	var tn *SchemaTooNewError
	if !errors.As(err, &tn) || tn.DB != 999 {
		t.Fatalf("err = %v，預期 SchemaTooNewError", err)
	}
}

// identities 的唯一性：同一個 (provider, subject) 只能屬於一個帳號，每個帳號每個供應商最多一個身分（HACC-R4、R6）。
func TestIdentityConstraints(t *testing.T) {
	s := openTemp(t, t.TempDir())
	exec := func(q string, args ...any) error { _, err := s.w.Exec(q, args...); return err }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(exec(`INSERT INTO users (id, display_name, created_at) VALUES ('u1', 'a', 1), ('u2', 'b', 1)`))
	ins := `INSERT INTO identities (id, user_id, provider, subject, linked_at, last_login_at) VALUES (?, ?, ?, ?, 1, 1)`
	must(exec(ins, "i1", "u1", "github", "42"))
	if exec(ins, "i2", "u2", "github", "42") == nil {
		t.Error("同一個 (provider, subject) 不能屬於兩個帳號")
	}
	if exec(ins, "i3", "u1", "github", "43") == nil {
		t.Error("同一個帳號不能有兩個 github 身分")
	}
	if exec(ins, "i4", "u1", "gitlab", "1") == nil {
		t.Error("provider 只能是 google、github、linuxdo")
	}
	if exec(ins, "i5", "nobody", "google", "x") == nil {
		t.Error("user_id 要參照存在的帳號（foreign_keys）")
	}
	if n, err := s.CountUsers(context.Background()); err != nil || n != 2 {
		t.Errorf("CountUsers = %d, %v", n, err)
	}
	var _ *sql.DB = s.r
}
