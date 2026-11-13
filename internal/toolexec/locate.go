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
}

// 已知的工具。
var (
	YtDlp  = Spec{Name: "yt-dlp", EnvVar: "NOTESTEME_YTDLP", ConfigKey: "yt-dlp"}
	FFmpeg = Spec{Name: "ffmpeg", EnvVar: "NOTESTEME_FFMPEG", ConfigKey: "ffmpeg"}
	// FFprobe 與 FFmpeg 分開設定：兩者不一定在同一個目錄。
	FFprobe = Spec{Name: "ffprobe", EnvVar: "NOTESTEME_FFPROBE", ConfigKey: "ffprobe"}
	// Deno 是 yt-dlp 解析部分 YouTube 內容所需的 JS runtime（ADR-0009）。
	Deno = Spec{Name: "deno", EnvVar: "NOTESTEME_DENO", ConfigKey: "deno"}
)

// Resolver 依 OPS-R8 的順序尋找工具：設定檔的路徑 → 環境變數 → PATH。
// 只看注入的 Vars（含 PATH），不讀程序自己的環境。
type Resolver struct {
	// Vars 是環境變數。
	Vars map[string]string
	// Config 是設定檔 tools 區段的內容，key 為 Spec.ConfigKey。
	Config map[string]string
}

// Resolve 回傳工具的絕對路徑。較前面的來源有設定時只認它：路徑不能執行就回報錯誤，不退回後面的來源。
func (r Resolver) Resolve(s Spec) (string, error) {
	if p := r.Config[s.ConfigKey]; p != "" {
		if err := checkExecutable(p); err != nil {
			return "", fmt.Errorf("設定檔 tools.%s=%s：%w", s.ConfigKey, p, err)
		}
		return absPath(p)
	}
	if p := r.Vars[s.EnvVar]; p != "" {
		if err := checkExecutable(p); err != nil {
			return "", fmt.Errorf("%s=%s：%w", s.EnvVar, p, err)
		}
		return absPath(p)
	}
	return LookPath(s.Name, r.Vars["PATH"])
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
