package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

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

// migration 00005 重建 group_members 與 audit_log：既有的資料與 index 都要保留，新的 CHECK 生效（M5-S5）。
func TestCommunityMigrationKeepsData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, DBFileName)
	db, err := sqlitedb.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, display_name, created_at) VALUES ('u1', 'root', 1)`,
		`INSERT INTO group_members (user_id, group_name, source, rule_provider, added_at) VALUES
			('u1', 'admins', 'config', '', 1), ('u1', 'moderators', 'manual', '', 2), ('u1', 'contributors', 'rule', 'linuxdo', 3)`,
		`INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, at) VALUES ('a1', 'u1', 'group.add', 'user', 'u1', 5)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	s := openTemp(t, dir)
	ms, err := s.GroupMemberships(ctx, "u1")
	if err != nil || len(ms) != 3 {
		t.Fatalf("migration 後的成員資格 = %+v, %v", ms, err)
	}
	es, err := s.AuditEntries(ctx, 10)
	if err != nil || len(es) != 1 || es[0].Actor != "root" || es[0].ActorUserID != "u1" {
		t.Fatalf("migration 後的稽核紀錄 = %+v, %v", es, err)
	}
	for _, idx := range []string{"group_members_group", "audit_log_at"} {
		var n int
		if err := s.r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s = %d, %v", idx, n, err)
		}
	}
	if _, err := s.w.Exec(`INSERT INTO group_members (user_id, group_name, source, added_at) VALUES ('u1', 'admins', 'cli', 1)`); err != nil {
		t.Errorf("source cli：%v", err)
	}
	if _, err := s.w.Exec(`INSERT INTO group_members (user_id, group_name, source, added_at) VALUES ('u1', 'admins', 'nope', 1)`); err == nil {
		t.Error("不認得的 source 要被 CHECK 擋下")
	}
	// Down 也要能還原（命令列的資料會被刪掉）。
	db2, err := sqlitedb.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	p2, _ := goose.NewProvider(goose.DialectSQLite3, db2, MigrationsFS())
	if _, err := p2.DownTo(ctx, 4); err != nil {
		t.Fatalf("Down：%v", err)
	}
	var n int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM group_members`).Scan(&n); err != nil || n != 3 {
		t.Errorf("Down 後的成員資格 = %d, %v", n, err)
	}
}

// migration 00006 重建 versions（移除鍵的唯一限制，安全修補 A）：被其他表參照的資料、外鍵與 index 都要保留。
func TestScopedDedupMigrationKeepsData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, DBFileName)
	db, err := sqlitedb.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	p, err := goose.NewProvider(goose.DialectSQLite3, db, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, display_name, created_at) VALUES ('u1', 'alice', 1), ('u2', 'bob', 1)`,
		`INSERT INTO videos (id, platform, external_id, key_sha256, first_shared_at) VALUES ('v1', 'youtube', 'abc', 'k', 1)`,
		`INSERT INTO blobs (sha256, size, media_type, created_at) VALUES ('s1', 1, 'application/json', 1), ('s2', 1, 'application/json', 1)`,
		`INSERT INTO versions (id, video_id, kind, variant, lang, format, sha256, size, model_verified, visibility, license, score, created_at, removal_source, removal_reason)
		 VALUES ('x1', 'v1', 'transcript', 'source', 'en', 'notesteme-cues+json', 's1', 1, 1, 'private', 'CC0-1.0', 1, 1, '', ''),
		        ('x2', 'v1', 'transcript', 'normalized', 'en', 'notesteme-cues+json', 's2', 1, 0, 'public', 'CC0-1.0', 0, 2, 'report', 'spam')`,
		`INSERT INTO version_blobs (version_id, sha256, role) VALUES ('x1', 's1', 'content'), ('x2', 's2', 'content')`,
		`INSERT INTO shares (id, user_id, video_id, license, attested_at, bundle_sha256, bundle_size, created_at) VALUES ('sh1', 'u1', 'v1', 'CC0-1.0', 1, 'b', 1, 1)`,
		`INSERT INTO share_versions (share_id, version_id, position, deduplicated) VALUES ('sh1', 'x1', 0, 0), ('sh1', 'x2', 1, 0)`,
		`INSERT INTO derivations (child_version_id, parent_version_id, relation) VALUES ('x2', 'x1', 'parent')`,
		`INSERT INTO votes (version_id, user_id, value, created_at, updated_at) VALUES ('x1', 'u2', 1, 1, 1)`,
		`INSERT INTO reports (id, version_id, reporter_id, reason, created_at) VALUES ('r1', 'x2', 'u2', 'spam', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s：%v", q, err)
		}
	}
	_ = db.Close()
	s := openTemp(t, dir)
	if v, err := s.SchemaVersion(ctx); err != nil || v != 6 {
		t.Fatalf("schema 版本 = %d, %v", v, err)
	}
	x1, err := s.Version(ctx, "x1")
	if err != nil || x1.Visibility != "private" || !x1.ModelVerified || x1.Score != 1 {
		t.Fatalf("x1 = %+v, %v", x1, err)
	}
	var src, reason string
	if err := s.r.QueryRow(`SELECT removal_source, removal_reason FROM versions WHERE id = 'x2'`).Scan(&src, &reason); err != nil || src != "report" || reason != "spam" {
		t.Fatalf("x2 的 removal = %q %q, %v", src, reason, err)
	}
	for table, want := range map[string]int{"version_blobs": 2, "share_versions": 2, "derivations": 1, "votes": 1, "reports": 1} {
		var n int
		if err := s.r.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d, %v，預期 %d", table, n, err, want)
		}
	}
	var bad int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil || bad != 0 {
		t.Errorf("foreign_key_check = %d, %v", bad, err)
	}
	var fk int
	if err := s.w.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("migration 後 foreign_keys = %d, %v，預期打開", fk, err)
	}
	for _, idx := range []string{"versions_sha", "versions_key"} {
		var n int
		if err := s.r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s = %d, %v", idx, n, err)
		}
	}
	// 同一個鍵可以有第二列；外鍵仍然生效。
	if _, err := s.w.Exec(`INSERT INTO versions (id, video_id, kind, variant, lang, format, sha256, size, model_verified, visibility, license, created_at)
		VALUES ('x3', 'v1', 'transcript', 'source', 'en', 'notesteme-cues+json', 's1', 1, 1, 'public', 'CC0-1.0', 3)`); err != nil {
		t.Errorf("同一個鍵的第二列：%v", err)
	}
	if _, err := s.w.Exec(`INSERT INTO version_blobs (version_id, sha256, role) VALUES ('nope', 's1', 'content')`); err == nil {
		t.Error("version_blobs 的外鍵要生效")
	}
}
