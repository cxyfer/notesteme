package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pathguard"
)

// GRD-R7：path 不在根目錄內回 403 path_outside_roots；通過檢查後延遲驗證，檔案不存在仍回 201（ING-05）。
func TestCreateItemPathGuard(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(tmp, "media")
	if err := os.MkdirAll(filepath.Join(media, "talks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(media, "link-to-root")); err != nil {
		t.Skipf("無法建立 symlink：%v", err)
	}
	r := newItemsRigAPI(t, func(d *Deps) { d.Paths = pathguard.New([]string{media}) })

	const msg = "只能新增媒體資料夾中的檔案。"
	for _, c := range []struct {
		name, path string
		status     int
	}{
		{"根目錄之外", "/etc/passwd", 403},
		{"..", media + "/../etc/passwd", 403},
		{"symlink 指到 /", media + "/link-to-root/etc/passwd", 403},
		{"相對路徑", "talks/keynote.mp4", 403},
		{"根目錄之內", media + "/talks/keynote.mp4", 201},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := r.owner("POST", "/api/items", `{"path":"`+c.path+`"}`)
			if rec.Code != c.status {
				t.Fatalf("status = %d，要 %d：%s", rec.Code, c.status, rec.Body)
			}
			if c.status == 403 {
				if e := decode[Error](t, rec); e.Code != "path_outside_roots" || e.Message != msg {
					t.Errorf("error = %+v", e)
				}
			}
		})
	}
}

// 沒有注入 Paths 時一律拒絕（fail closed），不能變成不設防。
func TestCreateItemPathWithoutGuard(t *testing.T) {
	r := newItemsRig(t)
	if rec := r.owner("POST", "/api/items", `{"path":"/etc/passwd"}`); rec.Code != 403 {
		t.Fatalf("沒有 Paths 時應回 403：%d %s", rec.Code, rec.Body)
	}
}

// 獨立模式（Unrestricted）不檢查路徑：/etc/passwd 通過檢查（201）。
func TestCreateItemPathUnrestricted(t *testing.T) {
	r := newItemsRigAPI(t, func(d *Deps) { d.Paths = pathguard.Unrestricted() })
	if rec := r.owner("POST", "/api/items", `{"path":"/etc/passwd"}`); rec.Code != 201 {
		t.Fatalf("Unrestricted 應通過檢查（201）：%d %s", rec.Code, rec.Body)
	}
}
