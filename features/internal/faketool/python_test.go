package faketool

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// execAt 以子程序執行 path（argv[0] 就是 path，模擬 production 用絕對路徑執行 venv 內的工具）。
func (s *scenario) execAt(path string, args ...string) (code int, stdout, stderr string) {
	s.t.Helper()
	var out, errb bytes.Buffer
	cmd := &exec.Cmd{
		Path: path, Args: append([]string{path}, args...), Dir: s.work,
		Env: []string{"PATH=" + s.bin}, Stdout: &out, Stderr: &errb,
	}
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		s.t.Fatalf("exec %s: %v", path, err)
	}
	return code, out.String(), errb.String()
}

func pipInstall(extra ...string) []string {
	return append([]string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input"}, extra...)
}

// 從 PATH 上的 python3 建立 venv，再用 venv 內的 python 裝 yt-dlp；venv 在場景根目錄的深處，
// 假工具要能從那裡找回場景根目錄（不能假設根目錄就是 bin/ 的上一層）。
func TestPythonVenvAndPipInstall(t *testing.T) {
	st := State{Releases: map[string]*Release{"2026.11.10": {}}}
	s := newScenario(t, st)
	venv := filepath.Join(s.root, "data", "tools", "yt-dlp", "2026.11.10")

	if code, _, errs := s.exec(Python3, "-m", "venv", venv); code != 0 {
		t.Fatalf("venv：code=%d stderr=%q", code, errs)
	}
	py := filepath.Join(venv, "bin", "python")
	if fi, err := os.Lstat(py); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bin/python：%v %v", fi, err)
	}
	if _, err := os.Lstat(filepath.Join(venv, "bin", "yt-dlp")); err == nil {
		t.Fatal("剛建立的 venv 還沒有 yt-dlp，要由 pip 裝上去")
	}

	if code, _, errs := s.execAt(py, pipInstall("yt-dlp[default]==2026.11.10")...); code != 0 {
		t.Fatalf("pip install：code=%d stderr=%q", code, errs)
	}
	code, out, errs := s.execAt(filepath.Join(venv, "bin", "yt-dlp"), "--version")
	if code != 0 || strings.TrimSpace(out) != "2026.11.10" {
		t.Fatalf("venv 內的 yt-dlp --version：code=%d out=%q stderr=%q", code, out, errs)
	}
	// PATH 上的 yt-dlp 不受影響，仍是 State 的版本。
	if _, out, _ := s.exec(YtDlp, "--version"); strings.TrimSpace(out) != DefaultYtDlpVersion {
		t.Errorf("PATH 上的 yt-dlp：%q", out)
	}
	// 呼叫都記在 calls.jsonl。
	calls, err := ReadCalls(s.root)
	if err != nil {
		t.Fatal(err)
	}
	var pythons int
	for _, c := range calls {
		if c.Tool == Python3 || c.Tool == VenvPython {
			pythons++
		}
	}
	if pythons != 2 {
		t.Errorf("python 的呼叫紀錄 %d 筆，預期 2：%+v", pythons, calls)
	}
}

func TestPythonPipUpgradeInstallsLatestRelease(t *testing.T) {
	s := newScenario(t, State{Releases: map[string]*Release{"2026.10.01": {}, "2026.11.10": {}, "2026.11.02": {}}})
	venv := filepath.Join(s.root, "v")
	if code, _, errs := s.exec(Python3, "-m", "venv", venv); code != 0 {
		t.Fatalf("venv：%d %q", code, errs)
	}
	if code, _, errs := s.execAt(filepath.Join(venv, "bin", "python"), pipInstall("-U", "yt-dlp[default]")...); code != 0 {
		t.Fatalf("pip install -U：%d %q", code, errs)
	}
	if _, out, _ := s.execAt(filepath.Join(venv, "bin", "yt-dlp"), "--version"); strings.TrimSpace(out) != "2026.11.10" {
		t.Errorf("-U 應裝最新的版本：%q", out)
	}
}

func TestPythonPipRejectsUnknownReleaseAndArguments(t *testing.T) {
	s := newScenario(t, State{Releases: map[string]*Release{"2026.11.10": {}}})
	venv := filepath.Join(s.root, "v")
	if code, _, _ := s.exec(Python3, "-m", "venv", venv); code != 0 {
		t.Fatal("venv")
	}
	py := filepath.Join(venv, "bin", "python")

	// 沒有登記的版本：裝不起來（結束碼 1），也不會留下 yt-dlp。
	code, _, errs := s.execAt(py, pipInstall("yt-dlp[default]==2026.01.01")...)
	if code != 1 || !strings.Contains(errs, "No matching distribution") {
		t.Errorf("未登記的版本：code=%d stderr=%q", code, errs)
	}
	if _, err := os.Lstat(filepath.Join(venv, "bin", "yt-dlp")); err == nil {
		t.Error("安裝失敗不應留下 yt-dlp")
	}
	// 不在白名單的旗標、套件與寫法一律是參數錯誤（結束碼 2）。
	for _, args := range [][]string{
		pipInstall("--index-url", "https://evil.example/simple", "yt-dlp[default]==2026.11.10"),
		pipInstall("--extra-index-url=https://evil.example", "yt-dlp[default]"),
		pipInstall("yt-dlp==2026.11.10"),
		pipInstall("yt-dlp[default]==2026.11.10; rm -rf /"),
		pipInstall("requests"),
		pipInstall("yt-dlp[default]", "other"),
		pipInstall(),
		{"-m", "pip", "uninstall", "yt-dlp"},
		{"-m", "pip"},
		{"-c", "print(1)"},
		{"-m", "http.server"},
	} {
		if code, _, errs := s.execAt(py, args...); code != 2 {
			t.Errorf("%q：code=%d stderr=%q，預期 2", args, code, errs)
		}
	}
	// PATH 上的 python3 不是 venv，不能 pip install。
	if code, _, _ := s.exec(Python3, pipInstall("yt-dlp[default]")...); code != 2 {
		t.Errorf("venv 外的 pip：code=%d", code)
	}
	// venv 的參數要正好是一個目錄。
	for _, args := range [][]string{{"-m", "venv"}, {"-m", "venv", "a", "b"}, {"-m", "venv", "--clear"}} {
		if code, _, _ := s.exec(Python3, args...); code != 2 {
			t.Errorf("%q：code=%d，預期 2", args, code)
		}
	}
}

// 只有這個版本對指定的影片探測失敗；--version 與其他影片照常。
func TestVenvYtDlpProbeFailsOnlyForRegisteredVideos(t *testing.T) {
	st := State{
		Videos:   map[string]*Video{"checkvideo1": {Title: "check"}, "otherxxxxxx": {Title: "other"}},
		Releases: map[string]*Release{"2026.11.10": {ProbeFailsFor: []string{"checkvideo1"}}, "2026.11.11": {}},
	}
	s := newScenario(t, st)
	bad, good := filepath.Join(s.root, "bad"), filepath.Join(s.root, "good")
	if err := InstallVenv(bad, "2026.11.10"); err != nil {
		t.Fatal(err)
	}
	if err := InstallVenv(good, "2026.11.11"); err != nil {
		t.Fatal(err)
	}
	probe := func(venv, id string) int {
		code, _, _ := s.execAt(filepath.Join(venv, "bin", "yt-dlp"), "-J", "--", "https://www.youtube.com/watch?v="+id)
		return code
	}
	if code, out, _ := s.execAt(filepath.Join(bad, "bin", "yt-dlp"), "--version"); code != 0 || strings.TrimSpace(out) != "2026.11.10" {
		t.Errorf("--version 應照常成功：%d %q", code, out)
	}
	if probe(bad, "checkvideo1") == 0 {
		t.Error("登記為失敗的版本，對檢查影片的探測應失敗")
	}
	if probe(bad, "otherxxxxxx") != 0 {
		t.Error("其他影片的探測不應失敗")
	}
	if probe(good, "checkvideo1") != 0 {
		t.Error("沒有登記失敗的版本，探測應成功")
	}
}

func TestFindRootWalksUpToScenario(t *testing.T) {
	s := newScenario(t, State{})
	if got := findRoot(s.bin); got != s.root {
		t.Errorf("bin/：%q，預期 %q", got, s.root)
	}
	deep := filepath.Join(s.root, "data", "tools", "yt-dlp-venv", "bin")
	if got := findRoot(deep); got != s.root {
		t.Errorf("深處的 venv：%q，預期 %q", got, s.root)
	}
	// 找不到場景時回傳上一層，讓 requireScenario 以原本的訊息拒絕。
	lone := filepath.Join(t.TempDir(), "x", "bin")
	if got := findRoot(lone); got != filepath.Dir(lone) {
		t.Errorf("沒有場景：%q，預期 %q", got, filepath.Dir(lone))
	}
}

func TestInstallVenvWithVersionIsReadyToUse(t *testing.T) {
	s := newScenario(t, State{})
	venv := filepath.Join(s.root, "v")
	if err := InstallVenv(venv, "2026.11.01"); err != nil {
		t.Fatal(err)
	}
	// 重複呼叫（例如換版本）會取代既有的 symlink。
	if err := InstallVenv(venv, "2026.11.02"); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := s.execAt(filepath.Join(venv, "bin", "yt-dlp"), "--version"); code != 0 || strings.TrimSpace(out) != "2026.11.02" {
		t.Errorf("code=%d out=%q", code, out)
	}
}
