package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// layout 在暫存目錄建立：
//
//	<tmp>/media/talks/keynote.mp4
//	<tmp>/media2/secret.txt          （名稱與 media 相鄰）
//	<tmp>/outside/secret.txt
//	<tmp>/media/link-out -> <tmp>/outside
//	<tmp>/media/link-in  -> <tmp>/media/talks
//	<tmp>/alias          -> <tmp>/media （根目錄本身是 symlink）
func layout(t *testing.T) (tmp string) {
	t.Helper()
	// 暫存目錄本身可能含 symlink（例如 macOS 的 /var）；先解析，讓期望值與 EvalSymlinks 的結果一致。
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"media/talks", "media2", "outside"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"media/talks/keynote.mp4", "media2/secret.txt", "outside/secret.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"media/link-out": filepath.Join(tmp, "outside"),
		"media/link-in":  filepath.Join(tmp, "media", "talks"),
		"alias":          filepath.Join(tmp, "media"),
	} {
		if err := os.Symlink(target, filepath.Join(tmp, link)); err != nil {
			t.Skipf("無法建立 symlink：%v", err)
		}
	}
	return tmp
}

func TestCheck(t *testing.T) {
	tmp := layout(t)
	p := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }

	tests := []struct {
		name  string
		roots []string
		path  string
		ok    bool
	}{
		{"根目錄底下的檔案", []string{p("media")}, p("media/talks/keynote.mp4"), true},
		{"根目錄本身", []string{p("media")}, p("media"), true},
		{"根目錄尾端有斜線", []string{p("media") + "/"}, p("media/talks/keynote.mp4"), true},
		{"根目錄底下不存在的檔案", []string{p("media")}, p("media/talks/new.mp4"), true},
		{"不存在的子目錄底下的檔案", []string{p("media")}, p("media/nope/deeper/new.mp4"), true},
		{"指向根目錄內的 symlink", []string{p("media")}, p("media/link-in/keynote.mp4"), true},
		{"根目錄本身是 symlink", []string{p("alias")}, p("media/talks/keynote.mp4"), true},
		{"透過 symlink 的根目錄存取", []string{p("alias")}, p("alias/talks/keynote.mp4"), true},
		{"多個根目錄，第二個符合", []string{p("media2"), p("media")}, p("media/talks/keynote.mp4"), true},
		{"根目錄外的檔案", []string{p("media")}, p("outside/secret.txt"), false},
		{"相鄰名稱不算在內", []string{p("media")}, p("media2/secret.txt"), false},
		{"相鄰名稱不存在的檔案", []string{p("media")}, p("media2/new.txt"), false},
		{".. 跳出根目錄", []string{p("media")}, p("media/../outside/secret.txt"), false},
		{".. 跳出根目錄（檔案不存在）", []string{p("media")}, p("media/../outside/new.txt"), false},
		{".. 在不存在的目錄之後跳出", []string{p("media")}, p("media/nope/../../outside/new.txt"), false},
		{"symlink 指到根目錄外", []string{p("media")}, p("media/link-out/secret.txt"), false},
		{"symlink 指到根目錄外（檔案不存在）", []string{p("media")}, p("media/link-out/new.txt"), false},
		{"symlink 本身指到根目錄外", []string{p("media")}, p("media/link-out"), false},
		{"沒有根目錄", nil, p("media/talks/keynote.mp4"), false},
		{"空路徑", []string{p("media")}, "", false},
		{"相對路徑", []string{p("media")}, "media/talks/keynote.mp4", false},
		{"檔案系統根目錄", []string{p("media")}, "/", false},
		{"尚未建立的根目錄底下", []string{p("missing")}, p("missing/a.mp4"), true},
		{"尚未建立的根目錄，相鄰名稱", []string{p("missing")}, p("missing2/a.mp4"), false},
		{"尚未建立的根目錄，用 .. 跳出", []string{p("missing")}, p("missing/../outside/secret.txt"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := New(tc.roots).Check(tc.path)
			if tc.ok && err != nil {
				t.Fatalf("Check(%q) = %v，應該通過", tc.path, err)
			}
			if !tc.ok && !errors.Is(err, ErrOutsideRoots) {
				t.Fatalf("Check(%q) = %v，應該是 ErrOutsideRoots", tc.path, err)
			}
		})
	}
}

// 根目錄的路徑上有無法解析的部分（這裡是 ENOTDIR 以外的錯誤：symlink 迴圈）時略過該根目錄，不放行任何路徑。
func TestCheckUnresolvableRootNeverAllows(t *testing.T) {
	tmp := layout(t)
	loop := filepath.Join(tmp, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("無法建立 symlink：%v", err)
	}
	if err := New([]string{loop}).Check(filepath.Join(loop, "a.mp4")); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("無法解析的根目錄不應放行任何路徑，得到 %v", err)
	}
}

// 相對路徑與空字串的根目錄會依工作目錄解讀，不能當成根目錄。
func TestNewIgnoresRelativeRoots(t *testing.T) {
	for _, root := range []string{"", ".", "media"} {
		if err := New([]string{root}).Check("/etc/passwd"); !errors.Is(err, ErrOutsideRoots) {
			t.Errorf("根目錄 %q：應拒絕，得到 %v", root, err)
		}
	}
}

func TestUnrestricted(t *testing.T) {
	g := Unrestricted()
	for _, p := range []string{"/etc/passwd", "/media/../etc/passwd", "relative/x"} {
		if err := g.Check(p); err != nil {
			t.Errorf("Unrestricted().Check(%q) = %v", p, err)
		}
	}
}

// nil 的 Guard 一律拒絕（fail closed）：忘了注入時不能變成不設防。
func TestNilGuardDenies(t *testing.T) {
	var g *Guard
	if err := g.Check("/etc/passwd"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("nil Guard 應拒絕，得到 %v", err)
	}
}

// 根目錄是 / 時，所有存在的絕對路徑都在其中（管理者明確的選擇）。
func TestRootSlash(t *testing.T) {
	if err := New([]string{"/"}).Check("/etc/passwd"); err != nil {
		t.Fatalf("根目錄為 / 時應通過：%v", err)
	}
}
