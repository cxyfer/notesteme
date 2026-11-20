package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/store"
)

const wantBackupName = "notesteme-backup-20261112T100000Z.tar.gz"

// readBackup 讀出備份檔，確認它是完整、可還原的備份。
func readBackup(t *testing.T, path string) *backup.Contents {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := backup.Extract(f, t.TempDir(), backup.Limits{})
	if err != nil {
		t.Fatalf("備份 %s 不是有效的備份：%v", path, err)
	}
	return c
}

// 獨立模式：寫到 --out，檔名取自 clock，latest 是相對 symlink，內容是有效的備份。
func TestBackupStandalone(t *testing.T) {
	env, _ := testEnv(t)
	out := filepath.Join(shortTempDir(t), "nested", "backups")
	res, stdout, stderr := run(t, env, "backup", "--out", out)
	if res.Code != 0 || res.Mode != ModeStandalone {
		t.Fatalf("結果 = %+v，stderr：%s", res, stderr)
	}
	file := filepath.Join(out, wantBackupName)
	if !strings.Contains(stdout, file) {
		t.Errorf("stdout 沒有說明檔案位置：%q", stdout)
	}
	if target, err := os.Readlink(filepath.Join(out, backup.LatestName)); err != nil || target != wantBackupName {
		t.Errorf("latest 指向 %q, %v", target, err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("備份檔：%v, %v", fi, err)
	}
	c := readBackup(t, filepath.Join(out, backup.LatestName))
	if latest, _ := store.LatestSchemaVersion(nil); c.Manifest.SchemaVersion != latest {
		t.Errorf("manifest = %+v", c.Manifest)
	}
	// 獨立模式結束後鎖已釋放。
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("備份結束後 serve 無法啟動：%v", err)
	}
	srv.Kill()
}

// 沒有 --out：放在 <資料目錄>/backups；serve 執行中走 client 模式也可以備份；--json 輸出路徑。
func TestBackupClientModeDefaultOut(t *testing.T) {
	env, data := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()

	res, stdout, stderr := run(t, env, "backup", "--json")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v，stderr：%s", res, stderr)
	}
	var out struct {
		File   string `json:"file"`
		Latest string `json:"latest"`
		Bytes  int64  `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("--json 的輸出 %q：%v", stdout, err)
	}
	want := filepath.Join(data, "backups", wantBackupName)
	if out.File != want || out.Latest != filepath.Join(data, "backups", backup.LatestName) || out.Bytes <= 0 {
		t.Errorf("輸出 = %+v，預期檔案 %s", out, want)
	}
	readBackup(t, want)
}

func TestBackupUsageErrors(t *testing.T) {
	env, data := testEnv(t)
	for _, args := range [][]string{{"backup", "extra"}, {"backup", "--bogus"}} {
		if res, _, _ := run(t, env, args...); res.Code != 2 {
			t.Errorf("%v：結束碼 = %d，預期 2", args, res.Code)
		}
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("參數錯誤不該碰資料目錄")
	}
}

// fakeServer 是 --server 的對象：只實作 POST /api/backup，由 handler 決定回應。
func fakeServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

func remoteBackup(t *testing.T, url, out string) (Result, string) {
	t.Helper()
	env, _ := testEnv(t)
	res, _, stderr := run(t, env, "--server", url, "--token", "tk", "backup", "--out", out)
	return res, stderr
}

// server 建議的檔名不能夾帶路徑：不寫任何檔案。
func TestBackupRejectsUnsafeServerFileName(t *testing.T) {
	for _, name := range []string{"../../etc/evil", "x.tar.gz", "/abs/notesteme-backup-20261112T100000Z.tar.gz", ""} {
		url := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
			if name != "" {
				w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			}
			_, _ = w.Write([]byte("data"))
		})
		out := t.TempDir()
		res, stderr := remoteBackup(t, url, out)
		if res.Code != 1 || !strings.Contains(stderr, "備份檔名不正確") {
			t.Errorf("檔名 %q：結果 %+v，stderr：%s", name, res, stderr)
		}
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("檔名 %q：不該寫任何檔案：%v", name, entries)
		}
	}
}

// 串流到一半中斷：失敗、不留下部分檔案，latest 也不更新。
func TestBackupInterruptedStream(t *testing.T) {
	url := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="`+wantBackupName+`"`)
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	out := t.TempDir()
	res, stderr := remoteBackup(t, url, out)
	if res.Code != 1 {
		t.Fatalf("結果 = %+v，stderr：%s", res, stderr)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("中斷後不該留下檔案：%v", entries)
	}
}

// 錯誤回應（例如 token 沒有權限）：照 API 的錯誤處理，結束碼 1，不寫檔。
func TestBackupErrorResponse(t *testing.T) {
	url := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"forbidden","message":"只有 owner 可以備份。"}`))
	})
	out := t.TempDir()
	res, stderr := remoteBackup(t, url, out)
	if res.Code != 1 || res.ErrorCode != "forbidden" || !strings.Contains(stderr, "只有 owner 可以備份") {
		t.Errorf("結果 = %+v，stderr：%s", res, stderr)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("不該寫檔：%v", entries)
	}
}

// 備份（standalone）之後換資料目錄還原：端到端，包含完成訊息的提醒。
func TestRestoreFromBackup(t *testing.T) {
	env, data := testEnv(t)
	out := t.TempDir()
	if res, _, stderr := run(t, env, "backup", "--out", out); res.Code != 0 {
		t.Fatalf("backup：%+v %s", res, stderr)
	}
	file := filepath.Join(out, backup.LatestName)

	res, stdout, stderr := run(t, env, "restore", file)
	if res.Code != 0 || res.Mode != "" {
		t.Fatalf("restore：%+v\nstdout：%s\nstderr：%s", res, stdout, stderr)
	}
	for _, want := range []string{"已還原備份", "pre-restore-20261112T100000Z.db", "master key", "SEC-R5"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 不含 %q：%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "backups", "pre-restore-20261112T100000Z.db")); err != nil {
		t.Errorf("沒有 pre-restore：%v", err)
	}

	res, stdout, _ = run(t, env, "restore", "--json", file)
	var j struct {
		SchemaVersion int64  `json:"schema_version"`
		PreRestore    string `json:"pre_restore"`
	}
	if res.Code != 0 || json.Unmarshal([]byte(stdout), &j) != nil || j.SchemaVersion == 0 || j.PreRestore == "" {
		t.Errorf("--json：%+v %q", res, stdout)
	}
}

// serve 執行中：結束碼 1、說明要先停止 serve，不判斷模式。
func TestRestoreWhileServing(t *testing.T) {
	env, _ := testEnv(t)
	srv, err := app.StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	res, _, stderr := run(t, env, "restore", "/does/not/matter.tar.gz")
	if res.Code != 1 || res.Mode != "" || !strings.Contains(stderr, "請先停止 notesteme serve，再執行 restore。") {
		t.Errorf("結果 = %+v，stderr：%s", res, stderr)
	}
}

func TestRestoreUsageErrors(t *testing.T) {
	env, data := testEnv(t)
	for _, args := range [][]string{
		{"restore"}, {"restore", "a", "b"}, {"restore", "--bogus", "a"},
		{"restore", "--server", "http://127.0.0.1:1", "--token", "t", "a"},
	} {
		if res, _, _ := run(t, env, args...); res.Code != 2 {
			t.Errorf("%v：結束碼 = %d，預期 2", args, res.Code)
		}
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("參數錯誤不該碰資料目錄")
	}
}

func TestBackupName(t *testing.T) {
	resp := func(cd string) *http.Response {
		return &http.Response{Header: http.Header{"Content-Disposition": {cd}}}
	}
	if got, err := backupName(resp(`attachment; filename="` + wantBackupName + `"`)); err != nil || got != wantBackupName {
		t.Errorf("backupName = %q, %v", got, err)
	}
	for _, bad := range []string{"", "attachment", `attachment; filename="a/b.tar.gz"`, `inline; filename="../` + wantBackupName + `"`} {
		if _, err := backupName(resp(bad)); err == nil {
			t.Errorf("%q 應該被拒絕", bad)
		}
	}
}
