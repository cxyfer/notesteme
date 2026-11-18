package harness

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/netguard"
)

func TestNewCreatesLayoutAndDefaults(t *testing.T) {
	w := NewT(t)
	for _, d := range []string{"data", "data/inbox", "config", "bin", "fake", "home"} {
		if fi, err := os.Stat(filepath.Join(w.Root, d)); err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", d, err)
		}
	}
	if len(w.Root) > 30 {
		t.Fatalf("根目錄路徑太長: %s", w.Root)
	}
	want := map[string]string{
		"NOTESTEME_CONFIG":     filepath.Join(w.Root, "config", "notesteme.yaml"),
		"NOTESTEME_DATA":       filepath.Join(w.Root, "data"),
		"NOTESTEME_PASSWORD":   TestPassword,
		"NOTESTEME_MASTER_KEY": TestMasterKey,
		"PATH":                 filepath.Join(w.Root, "bin"),
		"HOME":                 filepath.Join(w.Root, "home"),
	}
	for k, v := range want {
		if w.Env[k] != v {
			t.Errorf("Env[%s] = %q, want %q", k, w.Env[k], v)
		}
	}
	if !w.Clock.Now().Equal(DefaultNow) {
		t.Fatalf("clock = %v", w.Clock.Now())
	}
	// 不污染真實的程序環境。
	if os.Getenv("NOTESTEME_DATA") != "" {
		t.Fatal("不應呼叫 os.Setenv")
	}
	for _, n := range []string{"yt-dlp", "ffmpeg", "ffprobe"} {
		if _, err := os.Lstat(filepath.Join(w.BinDir(), n)); err != nil {
			t.Fatalf("缺少假工具 %s: %v", n, err)
		}
	}
	if st, err := faketool.ReadState(w.Root); err != nil || st.FixturesDir == "" {
		t.Fatalf("state = %+v, err = %v", st, err)
	}
	if fi, err := os.Stat(w.FixturesDir); err != nil || !fi.IsDir() {
		t.Fatalf("FixturesDir = %q: %v", w.FixturesDir, err)
	}
}

func TestWorldsDoNotOverlap(t *testing.T) {
	a, b := NewT(t), NewT(t)
	if a.Root == b.Root || strings.HasPrefix(a.Root, b.Root+string(filepath.Separator)) ||
		strings.HasPrefix(b.Root, a.Root+string(filepath.Separator)) {
		t.Fatalf("目錄重疊: %s %s", a.Root, b.Root)
	}
	if a.Env["NOTESTEME_DATA"] == b.Env["NOTESTEME_DATA"] {
		t.Fatal("DATA 不應相同")
	}
	// 修改其中一個 World 不影響另一個。
	a.Env["X"] = "1"
	a.Clock.Advance(3600e9)
	a.Net.Handle("example.com", http.NotFoundHandler())
	a.Video("abc123xxxxx").Title = "A"
	if _, ok := b.Env["X"]; ok || !b.Clock.Now().Equal(DefaultNow) {
		t.Fatal("Env 或 Clock 被共用")
	}
	if _, err := b.Net.LookupNetIP(context.Background(), "ip", "example.com"); err == nil {
		t.Fatal("Net 被共用")
	}
	if len(b.Tools.Videos) != 0 {
		t.Fatal("Tools 被共用")
	}
}

func TestExpandUnexpand(t *testing.T) {
	w := NewT(t)
	d, c := w.DataDir(), w.ConfigDir()
	cases := []struct{ in, want string }{
		{"/data", d},
		{"/data/", d + "/"},
		{"/data/x/y.txt", d + "/x/y.txt"},
		{`"/data"`, `"` + d + `"`},
		{"path: /data/inbox/a.mp4", "path: " + d + "/inbox/a.mp4"},
		{"/config/notesteme.yaml", c + "/notesteme.yaml"},
		{"--data /data --config /config", "--data " + d + " --config " + c},
		{"/data /data", d + " " + d},
		{"/database", "/database"},
		{"/data-old/x", "/data-old/x"},
		{"/data.db", "/data.db"},
		{"/home/u/data/x", "/home/u/data/x"},
		{"/var/config", "/var/config"},
		{"/configs", "/configs"},
		{"/media", w.MediaDir()},
		{"/media/talks/keynote.mp4", w.MediaDir() + "/talks/keynote.mp4"},
		{"/media/../etc/passwd", w.MediaDir() + "/../etc/passwd"},
		{"media_roots: [/media]", "media_roots: [" + w.MediaDir() + "]"},
		{"/media-old/x", "/media-old/x"},
		{"/mediaplayer", "/mediaplayer"},
		{"/var/media", "/var/media"},
		{"no paths here", "no paths here"},
		{"", ""},
	}
	for _, tc := range cases {
		got := w.Expand(tc.in)
		if got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// 反向改寫：只有真的被改寫過的輸入才需要還原成原樣。
		if tc.in != tc.want {
			if back := w.Unexpand(got); back != tc.in {
				t.Errorf("Unexpand(%q) = %q, want %q", got, back, tc.in)
			}
		}
	}
	if got := w.Unexpand("error: cannot open " + d + "/x.db: busy"); got != "error: cannot open /data/x.db: busy" {
		t.Errorf("Unexpand = %q", got)
	}
	if got := w.Unexpand(d + "base"); got != d+"base" {
		t.Errorf("Unexpand 不應改寫 %q", got)
	}
}

func TestSubst(t *testing.T) {
	w := NewT(t)
	w.Refs["token:helper"] = "nst_abc"
	if got := w.Subst(`curl -H "Authorization: Bearer {token:helper}" {token:other}`); got !=
		`curl -H "Authorization: Bearer nst_abc" {token:other}` {
		t.Fatalf("Subst = %q", got)
	}
}

func TestCheckReportsViolations(t *testing.T) {
	fakenet.DrainTrapViolations()
	w := NewT(t)
	if err := w.Check(); err != nil {
		t.Fatalf("沒有違規時 Check 應為 nil: %v", err)
	}

	// fakenet 的違規：連到沒有登記的目標。
	if err := w.Net.Resolves("internal.test", "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Net.Dial(context.Background(), netguard.Target{
		Network: "tcp", Host: "internal.test", IP: netip.MustParseAddr("10.0.0.5"), Port: 80,
	})
	err := w.Check()
	if err == nil || !strings.Contains(err.Error(), "10.0.0.5") {
		t.Fatalf("Check = %v", err)
	}

	// trap 的違規。
	w2 := NewT(t)
	restore := fakenet.InstallTrap()
	_, _ = http.Get("http://example.com/") //nolint:noctx
	restore()
	err = w2.Check()
	if err == nil || !strings.Contains(err.Error(), "http://example.com") {
		t.Fatalf("Check = %v", err)
	}
	// Check 取走後違規已清空。
	if err := w2.Check(); err != nil {
		t.Fatalf("第二次 Check 應為 nil: %v", err)
	}
}

func TestWorldHostsFakeOrigin(t *testing.T) {
	w := NewT(t)
	w.Net.Handle("example.com", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = rw.Write([]byte("ok"))
	}))
	if _, err := w.Net.LookupNetIP(context.Background(), "ip", "example.com"); err != nil {
		t.Fatal(err)
	}
	w.Sources.Returns("groq", "yt:axxxxxxxxxx", "groq-en.json")
	if w.Sources.Calls("groq") != 0 {
		t.Fatal("calls 應為 0")
	}
}

func TestVideoAndSyncTools(t *testing.T) {
	w := NewT(t)
	v := w.Video("abc123xxxxx")
	v.Title = "T"
	v.Tracks = append(v.Tracks, faketool.Track{Kind: faketool.KindManual, Lang: "en"})
	if w.Video("abc123xxxxx") != v {
		t.Fatal("Video 應回傳同一個物件")
	}
	if err := w.SyncTools(); err != nil {
		t.Fatal(err)
	}
	st, err := faketool.ReadState(w.Root)
	if err != nil || st.Videos["abc123xxxxx"] == nil || st.Videos["abc123xxxxx"].Title != "T" {
		t.Fatalf("state = %+v err = %v", st, err)
	}
	calls, err := w.ToolCalls()
	if err != nil || len(calls) != 0 {
		t.Fatalf("calls = %v err = %v", calls, err)
	}
}

func TestCloseRemovesRootAndIsIdempotent(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root := w.Root
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("根目錄應已清除: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("重複 Close: %v", err)
	}
}

func TestGateArmAndOpen(t *testing.T) {
	w := NewT(t)
	// 沒有啟用時，解除與計數都是空操作。
	if err := w.OpenGate(); err != nil {
		t.Fatal(err)
	}
	if n, err := w.GateWaiting(); err != nil || n != 0 {
		t.Fatalf("GateWaiting = %d, %v", n, err)
	}

	if err := w.ArmGate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.GatePath()); err != nil {
		t.Fatalf("gate 檔應存在：%v", err)
	}
	st, err := faketool.ReadState(w.Root)
	if err != nil || st.Gate != w.GatePath() {
		t.Fatalf("狀態檔的 Gate = %q, %v", st.Gate, err)
	}

	// 兩個存活的程序停在 gate 前（用測試程序自己與它的父程序代表）；再次 ArmGate 不會清掉它們的標記。
	// 另外一個標記屬於已結束的程序（被 kill 而來不及移除標記），不能算數。
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{os.Getpid(), os.Getppid(), dead.Process.Pid} {
		if err := os.WriteFile(fmt.Sprintf("%s.waiting.%d", w.GatePath(), pid), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.ArmGate(); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.GateWaiting(); n != 2 {
		t.Fatalf("GateWaiting = %d，預期 2（已結束的程序不算）", n)
	}
	if err := w.WaitGateWaiting(2, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := w.WaitGateWaiting(3, 30*time.Millisecond); err == nil {
		t.Fatal("只有 2 個程序就位，等 3 個應逾時")
	}
	if err := w.WaitGateCount(2, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := w.WaitGateCount(1, 30*time.Millisecond); err == nil {
		t.Fatal("有 2 個程序存活，等恰好 1 個應逾時")
	}

	// 解除：gate 檔與（可能是被 kill 的程序留下的）標記都清掉；可再次啟用。
	if err := w.OpenGate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.GatePath()); !os.IsNotExist(err) {
		t.Fatalf("gate 檔應已移除：%v", err)
	}
	if n, _ := w.GateWaiting(); n != 0 {
		t.Fatalf("標記應已清除：%d", n)
	}
	if err := w.OpenGate(); err != nil {
		t.Fatalf("重複解除不應失敗：%v", err)
	}
	if err := w.ArmGate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.GatePath()); err != nil {
		t.Fatalf("解除後可再次啟用：%v", err)
	}
}
