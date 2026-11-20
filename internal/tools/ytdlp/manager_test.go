package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// fakeRunner 以 Go 程式碼模擬 python、pip 與 yt-dlp，不執行任何真實的指令（也不連網）。
// 每個 venv 的版本記在 <venv>/version。
type fakeRunner struct {
	mu    sync.Mutex
	calls []toolexec.Cmd

	// releases 是 pip 可以安裝的版本；latest 是 -U 裝的版本。
	releases map[string]bool
	latest   string
	// probeFails 列出 -J 會失敗的版本（--version 照常成功）。
	probeFails map[string]bool
	// pipExit 非 0 時 pip 以這個結束碼失敗。
	pipExit int
	// block 非 nil 時，pip 會先通知 reached 再等 block 關閉。
	block   chan struct{}
	reached chan struct{}
}

func newFakeRunner(releases ...string) *fakeRunner {
	f := &fakeRunner{releases: map[string]bool{}, probeFails: map[string]bool{}}
	for _, r := range releases {
		f.releases[r] = true
		f.latest = max(f.latest, r)
	}
	return f
}

func (f *fakeRunner) argvs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		out = append(out, append([]string{filepath.Base(c.Path)}, c.Args...))
	}
	return out
}

// installCalls 回傳 pip 與 venv 的呼叫（有沒有試著安裝任何東西）。
func (f *fakeRunner) installCalls() int {
	n := 0
	for _, a := range f.argvs() {
		if len(a) > 2 && a[1] == "-m" {
			n++
		}
	}
	return n
}

func venvOfPath(p string) string { return filepath.Dir(filepath.Dir(p)) }

func (f *fakeRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	fail := func(code int, msg string) (toolexec.Result, error) {
		return toolexec.Result{ExitCode: code, Stderr: []byte(msg)}, nil
	}
	switch filepath.Base(c.Path) {
	case "python3": // -m venv <dir>
		if len(c.Args) != 3 || c.Args[0] != "-m" || c.Args[1] != "venv" {
			return fail(2, "bad venv args")
		}
		if err := os.MkdirAll(filepath.Join(c.Args[2], "bin"), 0o755); err != nil {
			return toolexec.Result{}, err
		}
		return toolexec.Result{}, nil
	case "python": // -m pip install …
		if f.block != nil {
			close(f.reached)
			<-f.block
		}
		if f.pipExit != 0 {
			return fail(f.pipExit, "ERROR: pip exploded")
		}
		spec := c.Args[len(c.Args)-1]
		version := f.latest
		if _, v, ok := strings.Cut(spec, "=="); ok {
			version = v
		}
		if !f.releases[version] {
			return fail(1, "ERROR: No matching distribution found for "+spec)
		}
		dir := venvOfPath(c.Path)
		if err := os.WriteFile(filepath.Join(dir, "bin", "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			return toolexec.Result{}, err
		}
		return toolexec.Result{}, os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644)
	case "yt-dlp":
		b, err := os.ReadFile(filepath.Join(venvOfPath(c.Path), "version"))
		if err != nil {
			return fail(1, "no version")
		}
		version := string(b)
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			return toolexec.Result{Stdout: []byte(version + "\n")}, nil
		}
		if f.probeFails[version] {
			return fail(1, "ERROR: [youtube] "+CheckVideoID+": Unable to extract player response")
		}
		return toolexec.Result{Stdout: []byte(`{"id":"` + CheckVideoID + `","title":"Me at the zoo","duration":19}`)}, nil
	}
	return fail(2, "unknown tool "+c.Path)
}

// testEnv 是一個 Manager 加上它用到的目錄。
type testEnv struct {
	t      *testing.T
	tools  string
	runner *fakeRunner
	m      *Manager
	clk    *clock.Fake
}

func newEnv(t *testing.T, runner *fakeRunner) *testEnv {
	t.Helper()
	root := t.TempDir()
	pathDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pathDir, "python3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(root, "data", "tools")
	clk := clock.NewFake(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	m := New(tools, runner, toolexec.Resolver{Vars: map[string]string{"PATH": pathDir}}, clk, nil)
	return &testEnv{t: t, tools: tools, runner: runner, m: m, clk: clk}
}

// install 建立受管理的 venv <tools>/yt-dlp/<id>（回報 version）並讓 symlink 指向它。
func (e *testEnv) install(id, version string) {
	e.t.Helper()
	dir := filepath.Join(VersionsDir(e.tools), id)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := switchLink(e.tools, id); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) linkTarget() string {
	e.t.Helper()
	target, err := os.Readlink(LinkPath(e.tools))
	if err != nil {
		e.t.Fatalf("symlink：%v", err)
	}
	return target
}

func (e *testEnv) dirs() []string {
	e.t.Helper()
	entries, err := os.ReadDir(VersionsDir(e.tools))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		if en.IsDir() {
			out = append(out, en.Name())
		}
	}
	return out
}

func (e *testEnv) state() State {
	e.t.Helper()
	st, err := LoadState(e.tools)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func mustUpdate(t *testing.T, e *testEnv, req UpdateRequest) *Result {
	t.Helper()
	res, err := e.m.Update(context.Background(), req)
	if err != nil {
		t.Fatalf("Update(%+v)：%v", req, err)
	}
	return res
}

func TestUpdateKeepsAHealthyRelease(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")

	res := mustUpdate(t, e, UpdateRequest{})
	if res.Version != "2026.11.10" || res.RolledBack || res.Previous != "2026.11.01" || res.Pinned != "" {
		t.Fatalf("結果 = %+v", res)
	}
	// symlink 換到新的 venv（相對路徑），上一版保留供 rollback。
	if target := e.linkTarget(); !strings.HasPrefix(target, "yt-dlp"+string(filepath.Separator)+"latest-20261115T100000Z") {
		t.Errorf("symlink 指向 %q", target)
	}
	if got := e.dirs(); len(got) != 2 {
		t.Errorf("應保留目前與上一版兩個 venv：%v", got)
	}
	st := e.state()
	if st.Previous != "2026.11.01" || st.PreviousID != "2026.11.01" || st.Pinned != "" {
		t.Errorf("state = %+v", st)
	}
	// 切換用的暫存 symlink 不留下。
	if _, err := os.Lstat(LinkPath(e.tools) + ".new"); err == nil {
		t.Error("暫存 symlink 沒有清掉")
	}
	// 通過檢查之後，新的 yt-dlp 就是 Locate 找到的那個。
	loc := e.m.Resolver.Locate(toolexec.YtDlp)
	if loc.Source != toolexec.SourceManaged || e.m.version(context.Background(), loc) != "2026.11.10" {
		t.Errorf("Locate = %+v", loc)
	}
}

// 自我檢查失敗：維持原本的版本，symlink 沒動，新 venv 刪掉，狀態檔沒有改。
func TestUpdateRollsBackWhenTheSelfCheckFails(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	r.probeFails["2026.11.10"] = true
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")
	before := e.linkTarget()

	res := mustUpdate(t, e, UpdateRequest{})
	if !res.RolledBack || res.Version != "2026.11.01" || res.Message != "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。" {
		t.Fatalf("結果 = %+v", res)
	}
	if e.linkTarget() != before {
		t.Errorf("symlink 被改動：%q → %q", before, e.linkTarget())
	}
	if got := e.dirs(); len(got) != 1 || got[0] != "2026.11.01" {
		t.Errorf("新的 venv 應該被刪除：%v", got)
	}
	if _, err := os.Stat(statePath(e.tools)); err == nil {
		t.Error("失敗的更新不應該寫入狀態")
	}
}

// 要求的版本與實際裝到的不同，也算自我檢查失敗。
func TestUpdateRollsBackWhenTheInstalledVersionDiffers(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")
	// pip 被要求 ==2026.11.10 卻裝了別的版本：模擬 latest 與 releases 的落差。
	r.releases["2026.11.12"] = true
	r.latest = "2026.11.12"
	badRunner := &versionSwapRunner{fakeRunner: r, from: "2026.11.10", to: "2026.11.12"}
	e.m.Runner = badRunner

	res := mustUpdate(t, e, UpdateRequest{Pin: "2026.11.10"})
	if !res.RolledBack || res.Version != "2026.11.01" || res.Pinned != "" {
		t.Fatalf("結果 = %+v", res)
	}
	if e.state().Pinned != "" {
		t.Error("沒有裝成功，不應該固定")
	}
}

// versionSwapRunner 把 pip 要求的版本換成另一個，模擬裝錯版本。
type versionSwapRunner struct {
	*fakeRunner
	from, to string
}

func (v *versionSwapRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if filepath.Base(c.Path) == "python" {
		c.Args = append([]string(nil), c.Args...)
		c.Args[len(c.Args)-1] = strings.Replace(c.Args[len(c.Args)-1], v.from, v.to, 1)
	}
	return v.fakeRunner.Run(ctx, c)
}

func TestPinnedUpdatesAreSkipped(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")

	res := mustUpdate(t, e, UpdateRequest{Pin: "2026.11.01"})
	if res.Pinned != "2026.11.01" || res.Version != "2026.11.01" || res.RolledBack || res.Skipped {
		t.Fatalf("固定的結果 = %+v", res)
	}
	if n := e.runner.installCalls(); n != 0 {
		t.Errorf("已經是這個版本，不該安裝任何東西（%d 次）", n)
	}

	res = mustUpdate(t, e, UpdateRequest{})
	if !res.Skipped || res.Message != "yt-dlp 已固定在 2026.11.01，略過更新。" || res.Version != "2026.11.01" {
		t.Fatalf("略過的結果 = %+v", res)
	}
	if n := e.runner.installCalls(); n != 0 {
		t.Errorf("固定之後的更新不該安裝任何東西（%d 次）", n)
	}
	if e.state().Pinned != "2026.11.01" {
		t.Error("固定的版本應該留在狀態檔")
	}

	// 解除固定之後可以更新。
	if res := mustUpdate(t, e, UpdateRequest{Unpin: true}); res.Pinned != "" {
		t.Fatalf("解除固定：%+v", res)
	}
	if res := mustUpdate(t, e, UpdateRequest{}); res.Version != "2026.11.10" || res.Skipped {
		t.Fatalf("解除之後的更新：%+v", res)
	}
}

// 固定在別的版本：裝那個版本（pip 的版本釘在 ==）並固定；沒通過檢查就不固定。
func TestPinInstallsTheRequestedVersion(t *testing.T) {
	r := newFakeRunner("2026.10.20", "2026.11.10")
	r.probeFails["2026.11.10"] = true
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")

	res := mustUpdate(t, e, UpdateRequest{Pin: "2026.10.20"})
	if res.Version != "2026.10.20" || res.Pinned != "2026.10.20" || res.RolledBack || res.Previous != "2026.11.01" {
		t.Fatalf("結果 = %+v", res)
	}
	var pip []string
	for _, a := range r.argvs() {
		if len(a) > 2 && a[1] == "-m" && a[2] == "pip" {
			pip = a
		}
	}
	if strings.Join(pip, " ") != "python -m pip install --isolated --disable-pip-version-check --no-input yt-dlp[default]==2026.10.20" {
		t.Errorf("pip 的參數 = %q", pip)
	}
	if got := e.linkTarget(); !strings.HasSuffix(got, "2026.10.20") {
		t.Errorf("venv 目錄用版本命名：%q", got)
	}

	// 固定在會失敗的版本：回退，也不會固定（原本的固定維持）。
	res = mustUpdate(t, e, UpdateRequest{Pin: "2026.11.10"})
	if !res.RolledBack || res.Version != "2026.10.20" || res.Pinned != "2026.10.20" {
		t.Fatalf("失敗的固定 = %+v", res)
	}
}

func TestUpdateRejectsInvalidVersionsBeforeRunningAnything(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	for _, v := range []string{
		"latest", "2026.11", "2026.1.1", "26.11.01", "2026.11.10.", "2026.11.10.x", " 2026.11.10", "2026.11.10 ",
		"2026.11.10;rm -rf /", "2026.11.10 --index-url=https://evil.example", "--index-url=https://evil.example",
		"2026.11.10\n", "../2026.11.10", "2026.11.10==1",
	} {
		_, err := e.m.Update(context.Background(), UpdateRequest{Pin: v})
		if !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("Pin %q：err = %v，預期 ErrInvalidVersion", v, err)
		}
	}
	if n := len(e.runner.argvs()); n != 0 {
		t.Errorf("版本不合法時不該執行任何指令（%d 次）：%v", n, e.runner.argvs())
	}
	if _, err := e.m.Update(context.Background(), UpdateRequest{Pin: "2026.11.10", Unpin: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("pin 與 unpin 同時指定：%v", err)
	}
}

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"2026.11.10", "2026.11.10.1", "2026.11.10.232612"} {
		if !ValidVersion(v) {
			t.Errorf("%q 應該合法", v)
		}
	}
	if ValidVersion("") {
		t.Error("空字串不是合法的版本")
	}
}

func TestPipArgsAreAFixedAllowlist(t *testing.T) {
	if got := strings.Join(PipArgs("2026.11.10"), " "); got != "-m pip install --isolated --disable-pip-version-check --no-input yt-dlp[default]==2026.11.10" {
		t.Errorf("指定版本：%q", got)
	}
	if got := strings.Join(PipArgs(""), " "); got != "-m pip install --isolated --disable-pip-version-check --no-input -U yt-dlp[default]" {
		t.Errorf("最新版：%q", got)
	}
	for _, a := range append(PipArgs(""), PipArgs("2026.11.10")...) {
		if strings.Contains(a, "index") || strings.HasPrefix(a, "http") {
			t.Errorf("不接受自訂的 package index：%q", a)
		}
	}
}

// 設定檔或環境變數指定了 yt-dlp：不更新（也不執行任何指令）。
func TestUpdateRefusesExternallyManagedTools(t *testing.T) {
	for name, mutate := range map[string]func(*Manager, string){
		"設定檔":  func(m *Manager, p string) { m.Resolver.Config = map[string]string{"yt-dlp": p} },
		"環境變數": func(m *Manager, p string) { m.Resolver.Vars["NOTESTEME_YTDLP"] = p },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, newFakeRunner("2026.11.10"))
			mutate(e.m, "/nonexistent/yt-dlp") // 不能執行也一樣：指定了就不是我們管的
			for label, call := range map[string]func() error{
				"update":   func() error { _, err := e.m.Update(context.Background(), UpdateRequest{}); return err },
				"pin":      func() error { _, err := e.m.Update(context.Background(), UpdateRequest{Pin: "2026.11.10"}); return err },
				"rollback": func() error { _, err := e.m.Rollback(context.Background()); return err },
			} {
				if err := call(); !errors.Is(err, ErrExternallyManaged) {
					t.Errorf("%s：err = %v，預期 ErrExternallyManaged", label, err)
				}
			}
			if n := len(e.runner.argvs()); n != 0 {
				t.Errorf("不該執行任何指令（%d 次）", n)
			}
			if _, err := os.Stat(VersionsDir(e.tools)); err == nil {
				t.Error("不該建立任何目錄")
			}
		})
	}
}

func TestUpdateReportsInstallFailuresWithoutSwitching(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	r.pipExit = 1
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")
	before := e.linkTarget()

	_, err := e.m.Update(context.Background(), UpdateRequest{})
	if !errors.Is(err, ErrInstall) || !strings.Contains(err.Error(), "pip exploded") {
		t.Fatalf("err = %v", err)
	}
	if e.linkTarget() != before || len(e.dirs()) != 1 {
		t.Errorf("安裝失敗不該留下東西：link=%q dirs=%v", e.linkTarget(), e.dirs())
	}

	// 版本不存在（pip 找不到）同樣是安裝失敗。
	r.pipExit = 0
	if _, err := e.m.Update(context.Background(), UpdateRequest{Pin: "2020.01.01"}); !errors.Is(err, ErrInstall) {
		t.Errorf("未知的版本：%v", err)
	}
	if e.state().Pinned != "" {
		t.Error("沒裝成功不該固定")
	}
}

func TestUpdateWithoutPythonFails(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.m.Resolver.Vars["PATH"] = t.TempDir()
	if _, err := e.m.Update(context.Background(), UpdateRequest{}); !errors.Is(err, ErrInstall) {
		t.Fatalf("err = %v", err)
	}
}

// 沒有受管理的版本時（用 PATH 上的 yt-dlp），第一次更新建立受管理的 venv；上一版不是受管理的，
// rollback 移除 symlink 就退回 PATH。
func TestFirstManagedUpdateAndRollbackToPath(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	e := newEnv(t, r)
	// PATH 上的 yt-dlp：由另一個 venv 目錄模擬（fakeRunner 以 <dir>/bin/yt-dlp 的上上層找 version）。
	builtin := filepath.Join(filepath.Dir(e.tools), "opt", "yt-dlp")
	if err := os.MkdirAll(filepath.Join(builtin, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtin, "bin", "yt-dlp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtin, "version"), []byte("2026.10.01"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.m.Resolver.Vars["PATH"] += string(os.PathListSeparator) + filepath.Join(builtin, "bin")

	res := mustUpdate(t, e, UpdateRequest{})
	if res.Version != "2026.11.10" || res.Previous != "2026.10.01" {
		t.Fatalf("結果 = %+v", res)
	}
	if st := e.state(); st.PreviousID != "" || st.Previous != "2026.10.01" {
		t.Errorf("state = %+v", st)
	}

	back, err := e.m.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if back.Version != "2026.10.01" || back.Message != "yt-dlp 已回到 2026.10.01。" {
		t.Errorf("rollback = %+v", back)
	}
	if _, err := os.Lstat(LinkPath(e.tools)); err == nil {
		t.Error("rollback 到映像內建的版本應移除 symlink")
	}
	if loc := e.m.Resolver.Locate(toolexec.YtDlp); loc.Source != toolexec.SourcePath {
		t.Errorf("Locate = %+v", loc)
	}
	// 再 rollback 一次回到剛才的新版本（上一版被換成了新版本）。
	if again, err := e.m.Rollback(context.Background()); err != nil || again.Version != "2026.11.10" {
		t.Errorf("再 rollback：%+v %v", again, err)
	}
}

func TestRollbackSwapsCurrentAndPrevious(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	mustUpdate(t, e, UpdateRequest{Pin: "2026.11.10"})
	if e.state().Pinned != "2026.11.10" {
		t.Fatal("前置：應固定在 2026.11.10")
	}

	res, err := e.m.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "2026.11.01" || res.Previous != "2026.11.10" || res.Pinned != "" {
		t.Fatalf("rollback = %+v（固定的版本與回到的版本不同，應解除固定）", res)
	}
	if got := e.linkTarget(); !strings.HasSuffix(got, "2026.11.01") {
		t.Errorf("symlink = %q", got)
	}
	if got := e.dirs(); len(got) != 2 {
		t.Errorf("兩個版本都要留著：%v", got)
	}
}

func TestRollbackWithoutPreviousFails(t *testing.T) {
	e := newEnv(t, newFakeRunner())
	e.install("2026.11.01", "2026.11.01")
	if _, err := e.m.Rollback(context.Background()); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v", err)
	}
	// 狀態說有上一版、目錄卻不見了。
	if err := saveState(e.tools, State{Previous: "2026.10.01", PreviousID: "2026.10.01"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Rollback(context.Background()); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("目錄不見：err = %v", err)
	}
}

// 最新版就是目前的版本：不切換，也不留下新的 venv。
func TestUpdateToTheCurrentVersionChangesNothing(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.01"))
	e.install("2026.11.01", "2026.11.01")
	res := mustUpdate(t, e, UpdateRequest{})
	if res.Version != "2026.11.01" || res.RolledBack || !strings.Contains(res.Message, "已經是最新版本") {
		t.Fatalf("結果 = %+v", res)
	}
	if got := e.dirs(); len(got) != 1 || e.linkTarget() != filepath.Join("yt-dlp", "2026.11.01") {
		t.Errorf("dirs=%v link=%q", got, e.linkTarget())
	}
}

// 更新進行中再呼叫：回 ErrBusy，不會同時安裝兩份。
func TestUpdateIsExclusive(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	r.block, r.reached = make(chan struct{}), make(chan struct{})
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")

	done := make(chan error, 1)
	go func() { _, err := e.m.Update(context.Background(), UpdateRequest{}); done <- err }()
	<-r.reached
	if _, err := e.m.Update(context.Background(), UpdateRequest{}); !errors.Is(err, ErrBusy) {
		t.Errorf("更新進行中：err = %v", err)
	}
	if _, err := e.m.Rollback(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("更新進行中的 rollback：err = %v", err)
	}
	close(r.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// client 斷線（ctx 取消）：不切換，新 venv 清掉。
func TestUpdateCancelledLeavesNothingBehind(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")
	ctx, cancel := context.WithCancel(context.Background())
	e.m.Runner = cancelOnPip{fakeRunner: r, cancel: cancel}

	if _, err := e.m.Update(ctx, UpdateRequest{}); err == nil {
		t.Fatal("被取消應回傳錯誤")
	}
	if got := e.dirs(); len(got) != 1 || e.linkTarget() != filepath.Join("yt-dlp", "2026.11.01") {
		t.Errorf("dirs=%v link=%q", got, e.linkTarget())
	}
}

type cancelOnPip struct {
	*fakeRunner
	cancel context.CancelFunc
}

func (c cancelOnPip) Run(ctx context.Context, cmd toolexec.Cmd) (toolexec.Result, error) {
	res, err := c.fakeRunner.Run(ctx, cmd)
	if filepath.Base(cmd.Path) == "python" {
		c.cancel()
		return res, ctx.Err()
	}
	return res, err
}

func TestStateFileIsPrivateAndAtomic(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	mustUpdate(t, e, UpdateRequest{Pin: "2026.11.10"})

	fi, err := os.Stat(statePath(e.tools))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state.json 權限 = %v，預期 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(VersionsDir(e.tools))
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".tmp") {
			t.Errorf("暫存檔沒有清掉：%s", en.Name())
		}
	}
	// 壞掉的狀態檔不會被默默當成空的。
	if err := os.WriteFile(statePath(e.tools), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Update(context.Background(), UpdateRequest{}); err == nil {
		t.Error("狀態檔壞掉應該回報錯誤")
	}
}

func TestNewIDAvoidsExistingDirectories(t *testing.T) {
	e := newEnv(t, newFakeRunner())
	if err := os.MkdirAll(filepath.Join(VersionsDir(e.tools), "2026.11.10"), 0o755); err != nil {
		t.Fatal(err)
	}
	if id := e.m.newID("2026.11.10"); id != "2026.11.10-2" {
		t.Errorf("id = %q", id)
	}
	if id := e.m.newID(""); id != "latest-20261115T100000Z" {
		t.Errorf("id = %q", id)
	}
}

// 受管理的 venv 之外的東西（state.json、不是目錄的檔案）不會被清掉；沒有用的舊 venv 會。
func TestPruneKeepsOnlyCurrentAndPrevious(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10", "2026.11.11"))
	e.install("2026.10.01", "2026.10.01")
	mustUpdate(t, e, UpdateRequest{Pin: "2026.11.10"})
	mustUpdate(t, e, UpdateRequest{Unpin: true})
	mustUpdate(t, e, UpdateRequest{})
	got := e.dirs()
	if len(got) != 2 {
		t.Fatalf("只該留下目前與上一版：%v", got)
	}
	if _, err := os.Stat(statePath(e.tools)); err != nil {
		t.Errorf("state.json 不該被清掉：%v", err)
	}
	for _, d := range got {
		if d == "2026.10.01" {
			t.Errorf("最舊的 venv 應該被清掉：%v", got)
		}
	}
}
