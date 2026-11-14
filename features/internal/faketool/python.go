package faketool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 假 venv 內的檔案。
const (
	// venvConfig 是 venv 的標記檔（真實的 venv 也有 pyvenv.cfg）。
	venvConfig = "pyvenv.cfg"
	// venvVersionFile 記錄這個 venv 裡裝的 yt-dlp 版本；假 yt-dlp 的 --version 讀它。
	venvVersionFile = "fake-version"
)

// pipSpec 是假 pip 接受的套件寫法：yt-dlp[default]，可以加上 ==<版本>。
var pipSpec = regexp.MustCompile(`^yt-dlp\[default\](?:==(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?))?$`)

// 假 pip 接受的旗標（只有 production 實際會帶的；其他一律拒絕，及早發現介面不一致，也擋下不該出現的 pip 參數）。
var pipFlags = map[string]bool{"--disable-pip-version-check": true, "--no-input": true, "-U": true}

// InstallVenv 在 dir 建立假的 venv：bin/python 指向測試 binary，pyvenv.cfg 標記它是 venv。
// version 非空時也放上 bin/yt-dlp 並記下版本，等同已經裝好那個版本的 yt-dlp。
// 假 python 的 `-m venv` 與 harness 的 `Given yt-dlp … is installed` 共用它。
func InstallVenv(dir, version string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, venvConfig), []byte("home = fake\n"), 0o644); err != nil {
		return err
	}
	if err := relink(exe, filepath.Join(bin, exeName(VenvPython))); err != nil {
		return err
	}
	if version == "" {
		return nil
	}
	return installYtDlp(dir, exe, version)
}

// installYtDlp 在已存在的假 venv 內放上 yt-dlp 並記下版本。
func installYtDlp(dir, exe, version string) error {
	if err := relink(exe, filepath.Join(dir, "bin", exeName(YtDlp))); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, venvVersionFile), []byte(version+"\n"), 0o644)
}

// relink 建立指向 target 的 symlink，已存在的同名檔案會被取代。
func relink(target, link string) error {
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("faketool: symlink %s: %w", link, err)
	}
	return nil
}

// venvOf 回傳 e 所在的假 venv 目錄（argv[0] 是 <venv>/bin/<工具>，而且 <venv>/pyvenv.cfg 存在）；不在 venv 內時回傳空字串。
func venvOf(e env) string {
	bin := filepath.Dir(e.argv[0])
	if filepath.Base(bin) != "bin" {
		return ""
	}
	dir := filepath.Dir(bin)
	if _, err := os.Stat(filepath.Join(dir, venvConfig)); err != nil {
		return ""
	}
	return dir
}

// ytDlpVersion 是這次被執行的 yt-dlp 回報的版本：假 venv 內的 yt-dlp 讀 venv 記下的版本，
// 其他（PATH 上的）用 State.YtDlpVersion，沒有就用預設值。
func ytDlpVersion(e env, st State) string {
	if dir := venvOf(e); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, venvVersionFile)); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	if st.YtDlpVersion != "" {
		return st.YtDlpVersion
	}
	return DefaultYtDlpVersion
}

// runPython 實作假的 python：只支援 `-m venv <dir>` 與 venv 內的 `-m pip install …`。
func runPython(e env, args []string) int {
	logCall(e, "")
	if len(args) < 2 || args[0] != "-m" {
		return usageError(e, "only `-m venv <dir>` and `-m pip install …` are supported, got %q", strings.Join(args, " "))
	}
	switch args[1] {
	case "venv":
		return pythonVenv(e, args[2:])
	case "pip":
		return pythonPip(e, args[2:])
	}
	return usageError(e, "unsupported module %q", args[1])
}

// pythonVenv 處理 `-m venv <dir>`：建立不含 yt-dlp 的假 venv（yt-dlp 要由 pip 裝上去）。
func pythonVenv(e env, args []string) int {
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return usageError(e, "expected `-m venv <dir>`, got %q", strings.Join(args, " "))
	}
	dir := args[0]
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.cwd, dir)
	}
	if err := InstallVenv(dir, ""); err != nil {
		fmt.Fprintf(e.stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// pythonPip 處理 venv 內的 `-m pip install [旗標] yt-dlp[default][==版本]`。
// 版本要在 State.Releases 登記過；沒有指定版本時取最新的一個。
func pythonPip(e env, args []string) int {
	dir := venvOf(e)
	if dir == "" {
		return usageError(e, "`-m pip` only works inside a venv created by `-m venv`")
	}
	if len(args) == 0 || args[0] != "install" {
		return usageError(e, "only `pip install` is supported")
	}
	var spec string
	for _, a := range args[1:] {
		switch {
		case pipFlags[a]:
		case strings.HasPrefix(a, "-"):
			return usageError(e, "unsupported pip option %q", a)
		case spec != "":
			return usageError(e, "unexpected extra argument %q", a)
		default:
			spec = a
		}
	}
	m := pipSpec.FindStringSubmatch(spec)
	if m == nil {
		return usageError(e, "unsupported requirement %q (only yt-dlp[default] with an optional ==<version>)", spec)
	}
	st, err := ReadState(e.root)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	version := m[1]
	if version == "" {
		version = latestRelease(st)
	}
	if _, ok := st.Releases[version]; !ok || version == "" {
		fmt.Fprintf(e.stderr, "ERROR: No matching distribution found for %s\n", spec)
		return 1
	}
	exe, err := os.Executable()
	if err == nil {
		err = installYtDlp(dir, exe, version)
	}
	if err != nil {
		fmt.Fprintf(e.stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "Successfully installed yt-dlp-%s\n", version)
	return 0
}

// latestRelease 回傳 State.Releases 中版本字串最大的一個；日期格式的版本，字串順序就是時間順序。
func latestRelease(st State) string {
	vs := make([]string, 0, len(st.Releases))
	for v := range st.Releases {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1]
}
