package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// state.json 裡的 id 會被拿去組路徑與切換 symlink：只能含 [A-Za-z0-9._-]，而且不是 . 或 ..。
func TestLoadStateRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"../evil", "a/b", `a\b`, ".", "..", "a b", "x\x00y", "/abs", "..∕x"} {
		tools := t.TempDir()
		if err := os.MkdirAll(VersionsDir(tools), 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"previous":"2026.11.01","previous_id":` + quoteJSON(id) + `}`
		if err := os.WriteFile(statePath(tools), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadState(tools)
		if !errors.Is(err, ErrStateCorrupt) {
			t.Errorf("previous_id %q：err = %v，預期 ErrStateCorrupt", id, err)
		}
	}
	for _, id := range []string{"2026.11.01", "latest-20261115T100000Z", "2026.11.10-2", "a_b.c-d"} {
		tools := t.TempDir()
		if err := saveState(tools, State{Previous: "x", PreviousID: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadState(tools); err != nil {
			t.Errorf("previous_id %q 是合法的：%v", id, err)
		}
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteString(string("0123456789abcdef"[r>>4]))
			b.WriteString(string("0123456789abcdef"[r&0xf]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// 狀態檔壞掉（無法解析或 id 不合法）：錯誤是 ErrStateCorrupt，訊息指出 state.json 的位置與可以刪除重來。
func TestCorruptStateIsReportedWithItsPath(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	if err := os.WriteFile(statePath(e.tools), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for label, call := range map[string]func() error{
		"state":    func() error { _, err := e.m.State(); return err },
		"update":   func() error { _, err := e.m.Update(context.Background(), UpdateRequest{}); return err },
		"rollback": func() error { _, err := e.m.Rollback(context.Background()); return err },
	} {
		err := call()
		if !errors.Is(err, ErrStateCorrupt) {
			t.Errorf("%s：err = %v，預期 ErrStateCorrupt", label, err)
			continue
		}
		if !strings.Contains(err.Error(), statePath(e.tools)) || !strings.Contains(err.Error(), "刪除") {
			t.Errorf("%s：訊息要指出 state.json 的位置並提示可以刪除：%v", label, err)
		}
	}
	if n := e.runner.installCalls(); n != 0 {
		t.Errorf("狀態檔壞掉時不該安裝任何東西（%d 次）", n)
	}
}

// symlink 指向奇怪的名稱時，currentID 不採用（它會被寫進 state.json 當 previous_id）。
func TestCurrentIDIgnoresUnsafeLinkTargets(t *testing.T) {
	e := newEnv(t, newFakeRunner())
	if err := os.MkdirAll(e.tools, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"yt-dlp/..", "yt-dlp/.", "yt-dlp/a b"} {
		_ = os.Remove(LinkPath(e.tools))
		if err := os.Symlink(target, LinkPath(e.tools)); err != nil {
			t.Fatal(err)
		}
		if id := e.m.currentID(); id != "" {
			t.Errorf("symlink → %q：currentID = %q，預期空字串", target, id)
		}
	}
	_ = os.Remove(LinkPath(e.tools))
	if err := os.Symlink("yt-dlp/2026.11.01", LinkPath(e.tools)); err != nil {
		t.Fatal(err)
	}
	if id := e.m.currentID(); id != "2026.11.01" {
		t.Errorf("currentID = %q", id)
	}
}

// chmodOnVersion 在第 n 次 `yt-dlp --version` 時把 yt-dlp/ 目錄改成唯讀，讓之後的 saveState 失敗
// （Rollback 在切換 symlink 之後會再問一次版本，剛好卡在 saveState 之前）。
type chmodOnVersion struct {
	*fakeRunner
	dir  string
	n    int
	seen int
}

func (c *chmodOnVersion) Run(ctx context.Context, cmd toolexec.Cmd) (toolexec.Result, error) {
	if filepath.Base(cmd.Path) == "yt-dlp" && len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
		c.seen++
		if c.seen == c.n {
			if err := os.Chmod(c.dir, 0o500); err != nil {
				return toolexec.Result{}, err
			}
		}
	}
	return c.fakeRunner.Run(ctx, cmd)
}

// Rollback 在狀態檔寫入失敗時，要把 symlink 換回原本的版本，不能留下「symlink 與狀態不一致」。
func TestRollbackRestoresLinkWhenSavingStateFails(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	mustUpdate(t, e, UpdateRequest{})
	before := e.linkTarget()

	dir := VersionsDir(e.tools)
	e.m.Runner = &chmodOnVersion{fakeRunner: e.runner, dir: dir, n: 2}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := e.m.Rollback(context.Background()); err == nil {
		t.Fatal("狀態檔寫不進去，Rollback 應該失敗")
	}
	if got := e.linkTarget(); got != before {
		t.Errorf("symlink = %q，預期還原成 %q", got, before)
	}
}

// 回退到映像內建的版本（移除 symlink）時，寫狀態失敗也要把 symlink 建回來。
func TestRollbackToPathRestoresLinkWhenSavingStateFails(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	e := newEnv(t, r)
	builtin := filepath.Join(filepath.Dir(e.tools), "opt", "yt-dlp")
	if err := os.MkdirAll(filepath.Join(builtin, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{filepath.Join(builtin, "bin", "yt-dlp"): "#!/bin/sh\n", filepath.Join(builtin, "version"): "2026.10.01"} {
		if err := os.WriteFile(p, []byte(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.m.Resolver.Vars["PATH"] += string(os.PathListSeparator) + filepath.Join(builtin, "bin")
	mustUpdate(t, e, UpdateRequest{})
	before := e.linkTarget()

	dir := VersionsDir(e.tools)
	e.m.Runner = &chmodOnVersion{fakeRunner: r, dir: dir, n: 2}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := e.m.Rollback(context.Background()); err == nil {
		t.Fatal("應該失敗")
	}
	if got := e.linkTarget(); got != before {
		t.Errorf("symlink = %q，預期還原成 %q", got, before)
	}
}

// 上一版 venv 遺失 bin/yt-dlp 時，Rollback 要還原 symlink、保留原 state.json 並回錯，不能留下壞掉的目標。
func TestRollbackRestoresLinkWhenPreviousManagedBinaryMissing(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	mustUpdate(t, e, UpdateRequest{})
	before := e.linkTarget()
	beforeState := e.state()

	if err := os.Remove(filepath.Join(VersionsDir(e.tools), "2026.11.01", "bin", "yt-dlp")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Rollback(context.Background()); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v，預期 ErrNoPrevious", err)
	}
	if got := e.linkTarget(); got != before {
		t.Errorf("symlink = %q，預期還原成 %q", got, before)
	}
	if got := e.state(); got != beforeState {
		t.Errorf("state = %+v，預期維持 %+v", got, beforeState)
	}
}

// 上一版是 PATH 後備且已不存在時，Rollback 要把 symlink 建回原本的受管理版本、保留 state.json 並回錯。
func TestRollbackRestoresLinkWhenPathFallbackMissing(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	e := newEnv(t, r)
	builtin := filepath.Join(filepath.Dir(e.tools), "opt", "yt-dlp")
	if err := os.MkdirAll(filepath.Join(builtin, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	ytBin := filepath.Join(builtin, "bin", "yt-dlp")
	for p, c := range map[string]string{ytBin: "#!/bin/sh\n", filepath.Join(builtin, "version"): "2026.10.01"} {
		if err := os.WriteFile(p, []byte(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.m.Resolver.Vars["PATH"] += string(os.PathListSeparator) + filepath.Join(builtin, "bin")
	mustUpdate(t, e, UpdateRequest{})
	before := e.linkTarget()
	beforeState := e.state()

	if err := os.Remove(ytBin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Rollback(context.Background()); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v，預期 ErrNoPrevious", err)
	}
	if got := e.linkTarget(); got != before {
		t.Errorf("symlink = %q，預期還原成 %q", got, before)
	}
	if got := e.state(); got != beforeState {
		t.Errorf("state = %+v，預期維持 %+v", got, beforeState)
	}
}

// Windows 不支援受管理的 yt-dlp（venv 的布局與 symlink 都不同）：明確回錯誤，不去跑 pip。
func TestManagedYtDlpIsUnsupportedOnWindows(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.m.GOOS = "windows"
	const want = "受管理的 yt-dlp 在 Windows 上不支援，請用設定檔或 " + "NOTESTEME_YTDLP" + " 指定路徑。"
	for label, call := range map[string]func() error{
		"update":   func() error { _, err := e.m.Update(context.Background(), UpdateRequest{}); return err },
		"pin":      func() error { _, err := e.m.Update(context.Background(), UpdateRequest{Pin: "2026.11.10"}); return err },
		"rollback": func() error { _, err := e.m.Rollback(context.Background()); return err },
	} {
		err := call()
		if !errors.Is(err, ErrUnsupportedPlatform) {
			t.Errorf("%s：err = %v，預期 ErrUnsupportedPlatform", label, err)
			continue
		}
		if err.Error() != want {
			t.Errorf("%s：訊息 = %q，預期 %q", label, err.Error(), want)
		}
	}
	if n := len(e.runner.argvs()); n != 0 {
		t.Errorf("不該執行任何指令（%d 次）", n)
	}
	// 訊息裡的環境變數要和 toolexec 實際讀的相同。
	if !strings.Contains(want, toolexec.YtDlp.EnvVar) {
		t.Errorf("訊息提到的環境變數與 toolexec.YtDlp.EnvVar（%s）不同", toolexec.YtDlp.EnvVar)
	}
	// 其他平台照常。
	e.m.GOOS = "linux"
	if _, err := e.m.Update(context.Background(), UpdateRequest{}); err != nil {
		t.Errorf("linux：%v", err)
	}
}

// 路徑由設定檔指定時，優先回報「自行指定」而不是平台不支援（使用者已經照訊息做了）。
func TestWindowsWithConfiguredPathIsExternallyManaged(t *testing.T) {
	e := newEnv(t, newFakeRunner())
	e.m.GOOS = "windows"
	e.m.Resolver.Config = map[string]string{"yt-dlp": "/x/yt-dlp"}
	if _, err := e.m.Update(context.Background(), UpdateRequest{}); !errors.Is(err, ErrExternallyManaged) {
		t.Errorf("err = %v", err)
	}
}

// 回退時附上簡短原因（reason 欄位），message 維持規格的固定文字，不帶任何後綴。
func TestRolledBackResultCarriesAShortReason(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	r.probeFails["2026.11.10"] = true
	e := newEnv(t, r)
	e.install("2026.11.01", "2026.11.01")
	res := mustUpdate(t, e, UpdateRequest{})
	if !res.RolledBack {
		t.Fatalf("前置：應該回退：%+v", res)
	}
	if res.Message != "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。" {
		t.Errorf("message 不可改動：%q", res.Message)
	}
	if !strings.Contains(res.Reason, "Unable to extract player response") {
		t.Errorf("reason = %q，預期帶有失敗原因", res.Reason)
	}
	if len([]rune(res.Reason)) > 300 || strings.ContainsAny(res.Reason, "\r\n") {
		t.Errorf("reason 應該是簡短的單行文字：%q", res.Reason)
	}
}

// reason 在寫進回應之前要先遮蔽 cookie 內容。
func TestReasonIsRedacted(t *testing.T) {
	got := shortReason(errors.New("探測失敗：WARNING: skipping cookie file entry due to bad line: 'SID=secret-value'\nERROR: boom"))
	if strings.Contains(got, "secret-value") {
		t.Errorf("reason 洩漏了 cookie：%q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("其他行要保留：%q", got)
	}
}

// pip 加上 --isolated：忽略使用者與系統的 pip.conf 與 PIP_ 環境變數，不接受自訂的 package index。
func TestPipArgsAreIsolated(t *testing.T) {
	for _, v := range []string{"", "2026.11.10"} {
		found := false
		for _, a := range PipArgs(v) {
			found = found || a == "--isolated"
		}
		if !found {
			t.Errorf("PipArgs(%q) 缺少 --isolated：%v", v, PipArgs(v))
		}
	}
}

// stderrRunner 讓 pip 以指定的 stderr 失敗。
type stderrRunner struct {
	*fakeRunner
	stderr string
}

func (s stderrRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if filepath.Base(c.Path) == "python" {
		return toolexec.Result{ExitCode: 1, Stderr: []byte(s.stderr)}, nil
	}
	return s.fakeRunner.Run(ctx, c)
}

// 錯誤訊息裡的 pip stderr 已經遮蔽含 cookie 內容的行（其他行保留）。
func TestInstallErrorRedactsCookieLines(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.m.Runner = stderrRunner{fakeRunner: e.runner, stderr: "ERROR: boom\nWARNING: skipping cookie file entry: 'SID=sekrit'\n"}
	_, err := e.m.Update(context.Background(), UpdateRequest{})
	if !errors.Is(err, ErrInstall) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "sekrit") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("訊息 = %q", err)
	}
}

// 安裝失敗的錯誤保留 pip 的 stderr 尾段（供 log 與本機 CLI），API 層另外決定回給 client 的內容。
func TestInstallErrorKeepsPipDetail(t *testing.T) {
	r := newFakeRunner("2026.11.10")
	r.pipExit = 1
	e := newEnv(t, r)
	_, err := e.m.Update(context.Background(), UpdateRequest{})
	if !errors.Is(err, ErrInstall) || !strings.Contains(err.Error(), "pip exploded") {
		t.Fatalf("err = %v", err)
	}
}

// 上一版 venv 回報的版本與記錄不符（內容被改過）：Rollback 要還原 symlink、保留 state.json 並回錯。
func TestRollbackRejectsManagedPreviousWithWrongVersion(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	e.install("2026.11.01", "2026.11.01")
	mustUpdate(t, e, UpdateRequest{})
	before := e.linkTarget()
	beforeState := e.state()

	if err := os.WriteFile(filepath.Join(VersionsDir(e.tools), "2026.11.01", "version"), []byte("2026.09.09"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.m.Rollback(context.Background())
	if !errors.Is(err, ErrNoPrevious) || !strings.Contains(err.Error(), "2026.09.09") || !strings.Contains(err.Error(), "2026.11.01") {
		t.Fatalf("err = %v，預期 ErrNoPrevious 並指出兩個版本", err)
	}
	if got := e.linkTarget(); got != before {
		t.Errorf("symlink = %q，預期還原成 %q", got, before)
	}
	if got := e.state(); got != beforeState {
		t.Errorf("state = %+v，預期維持 %+v", got, beforeState)
	}
}

// 上一版是映像內建的版本、而映像已經更新：照樣回退（否則退不出受管理的版本），訊息指出實際與記錄的版本。
func TestRollbackToUpgradedBuiltinReportsBothVersions(t *testing.T) {
	e := newEnv(t, newFakeRunner("2026.11.10"))
	builtin := filepath.Join(filepath.Dir(e.tools), "opt", "yt-dlp")
	if err := os.MkdirAll(filepath.Join(builtin, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(version string) {
		for p, c := range map[string]string{filepath.Join(builtin, "bin", "yt-dlp"): "#!/bin/sh\n", filepath.Join(builtin, "version"): version} {
			if err := os.WriteFile(p, []byte(c), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("2026.10.01")
	e.m.Resolver.Vars["PATH"] += string(os.PathListSeparator) + filepath.Join(builtin, "bin")
	mustUpdate(t, e, UpdateRequest{})
	write("2026.11.05") // 映像升級，內建版本跟著改變

	res, err := e.m.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "2026.11.05" || !strings.Contains(res.Message, "2026.11.05") || !strings.Contains(res.Message, "2026.10.01") {
		t.Errorf("結果 = %+v，訊息要指出實際（2026.11.05）與記錄（2026.10.01）的版本", res)
	}
	if _, err := os.Lstat(LinkPath(e.tools)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("回到映像內建的版本時 symlink 應該移除：%v", err)
	}
}
