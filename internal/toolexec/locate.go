package toolexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Spec 描述一個外部工具的尋找方式（OPS-R8）。
type Spec struct {
	// Name 是工具名稱，也是在 PATH 中尋找的檔名與 /readyz 回報的名稱。
	Name string
	// EnvVar 是指定路徑的環境變數。
	EnvVar string
	// ConfigKey 是設定檔 tools 區段的 key。
	ConfigKey string
	// ManagedVenv 是受管理的 venv 的 symlink 名稱（位於 Resolver.ManagedDir 底下）；空字串代表這個工具不是受管理的。
	ManagedVenv string
}

// 已知的工具。
var (
	// yt-dlp 由 Notesteme 管理（OPS-R6）：<ManagedDir>/yt-dlp-venv 是指向目前版本的 symlink。
	YtDlp  = Spec{Name: "yt-dlp", EnvVar: "NOTESTEME_YTDLP", ConfigKey: "yt-dlp", ManagedVenv: "yt-dlp-venv"}
	FFmpeg = Spec{Name: "ffmpeg", EnvVar: "NOTESTEME_FFMPEG", ConfigKey: "ffmpeg"}
	// FFprobe 與 FFmpeg 分開設定：兩者不一定在同一個目錄。
	FFprobe = Spec{Name: "ffprobe", EnvVar: "NOTESTEME_FFPROBE", ConfigKey: "ffprobe"}
	// Deno 是 yt-dlp 解析部分 YouTube 內容所需的 JS runtime（ADR-0009）。
	Deno = Spec{Name: "deno", EnvVar: "NOTESTEME_DENO", ConfigKey: "deno"}
)

// Source 是工具路徑的來源（OPS-R8，也是 GET /api/system 的 source 欄位）。
type Source string

const (
	SourceConfigFile Source = "config_file"
	SourceEnv        Source = "env"
	// SourceManaged 是 Notesteme 自己管理的 venv（OPS-R6）。
	SourceManaged Source = "managed"
	SourcePath    Source = "path"
)

// Location 是一次尋找的結果：來源與路徑，以及這個路徑不能執行的原因。
type Location struct {
	// Path 是找到（或被指定）的絕對路徑；在 PATH 中找不到時為空字串。
	Path   string
	Source Source
	// Err 非 nil 表示工具不能使用（Path 仍然回報，讓使用者看到是哪個路徑壞了）。
	Err error
}

// Available 回報工具是否可以執行。
func (l Location) Available() bool { return l.Err == nil }

// Resolver 依 OPS-R8 的順序尋找工具：設定檔的路徑 → 環境變數 → 受管理的 venv → PATH。
// 只看注入的 Vars（含 PATH），不讀程序自己的環境。
type Resolver struct {
	// Vars 是環境變數。
	Vars map[string]string
	// Config 是設定檔 tools 區段的內容，key 為 Spec.ConfigKey。
	Config map[string]string
	// ManagedDir 是受管理工具的目錄（<data>/tools）；空字串代表不使用受管理的 venv。
	ManagedDir string
}

// Locate 依 OPS-R8 的順序尋找工具。較前面的來源有指定時只認它：路徑不能執行就回報錯誤，不退回後面的來源。
// 受管理的 venv 以 symlink 存在為準；它壞了（例如目標被刪掉）一樣回報錯誤，而不是悄悄改用 PATH 上的版本。
func (r Resolver) Locate(s Spec) Location {
	if p := r.Config[s.ConfigKey]; p != "" {
		loc := Location{Source: SourceConfigFile}
		loc.Path, loc.Err = absPath(p)
		if loc.Err == nil {
			if err := checkExecutable(loc.Path); err != nil {
				loc.Err = fmt.Errorf("設定檔 tools.%s=%s：%w", s.ConfigKey, p, err)
			}
		}
		return loc
	}
	if p := r.Vars[s.EnvVar]; p != "" {
		loc := Location{Source: SourceEnv}
		loc.Path, loc.Err = absPath(p)
		if loc.Err == nil {
			if err := checkExecutable(loc.Path); err != nil {
				loc.Err = fmt.Errorf("%s=%s：%w", s.EnvVar, p, err)
			}
		}
		return loc
	}
	if r.ManagedDir != "" && s.ManagedVenv != "" {
		link := filepath.Join(r.ManagedDir, s.ManagedVenv)
		if _, err := os.Lstat(link); err == nil {
			loc := Location{Source: SourceManaged, Path: filepath.Join(link, "bin", s.Name)}
			if err := checkExecutable(loc.Path); err != nil {
				loc.Err = fmt.Errorf("受管理的 %s（%s）：%w", s.Name, link, err)
			}
			return loc
		}
	}
	p, err := LookPath(s.Name, r.Vars["PATH"])
	return Location{Path: p, Source: SourcePath, Err: err}
}

// Resolve 回傳工具的絕對路徑；不能使用時回傳錯誤（見 Locate）。
func (r Resolver) Resolve(s Spec) (string, error) {
	loc := r.Locate(s)
	if loc.Err != nil {
		return "", loc.Err
	}
	return loc.Path, nil
}

// LookPath 在 pathVar（PATH 的值，用 os.PathListSeparator 分隔）中尋找 name，回傳絕對路徑。
// 不使用程序自己的 PATH；相對的 PATH 項目與空項目會被略過，避免在目前目錄執行工具。
func LookPath(name, pathVar string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("不合法的工具名稱 %q", name)
	}
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if checkExecutable(p) == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("在 PATH 中找不到 %s", name)
}

func absPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	return filepath.Abs(p)
}

// checkExecutable 確認 p 是（跟隨 symlink 後的）一般檔案且有執行權限。
func checkExecutable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return errors.New("不是可執行的檔案")
	}
	return nil
}
