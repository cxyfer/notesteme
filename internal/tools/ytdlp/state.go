package ytdlp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// State 是 <tools>/yt-dlp/state.json 的內容：固定的版本與上一版（供 rollback）。
// 存在檔案而不是資料庫：工具的管理不應該依賴資料庫的 schema。
type State struct {
	// Pinned 非空時，手動與排程的更新都略過（OPS-R7）。
	Pinned string `json:"pinned,omitempty"`
	// Previous 是上一版的版本字串，只供顯示。
	Previous string `json:"previous,omitempty"`
	// PreviousID 是上一版的 venv 目錄名稱（<tools>/yt-dlp/<id>）；空字串代表上一版不是受管理的（例如映像內建的），
	// rollback 時改為移除 symlink，讓尋找順序退回 PATH。
	PreviousID string `json:"previous_id,omitempty"`
}

func statePath(tools string) string {
	return filepath.Join(VersionsDir(tools), stateFileName)
}

// ErrStateCorrupt 表示 state.json 無法解析，或內容不合法（例如 id 含路徑分隔字元）。
// 檔案只是記錄固定的版本與上一版，刪除後重來不會損失資料。
var ErrStateCorrupt = errors.New("yt-dlp 的狀態檔損毀")

// idRe 是 venv 目錄名稱（id）可以使用的字元：目錄名稱會被拿去組路徑與切換 symlink。
var idRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidID 回報 id 是不是合法的 venv 目錄名稱：只含 [A-Za-z0-9._-]，而且不是 . 或 ..。
func ValidID(id string) bool { return idRe.MatchString(id) && id != "." && id != ".." }

// corruptError 組出 ErrStateCorrupt，訊息指出檔案位置與處理方式。
func corruptError(tools string, cause error) error {
	return fmt.Errorf("%w：%s 無法使用（%v）；可以刪除這個檔案後重試，只會失去固定的版本與上一版的記錄。", ErrStateCorrupt, statePath(tools), cause)
}

// LoadState 讀取狀態；檔案不存在時回傳空的 State。無法解析或內容不合法時回傳 ErrStateCorrupt。
func LoadState(tools string) (State, error) {
	var st State
	b, err := os.ReadFile(statePath(tools))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, corruptError(tools, err)
	}
	// id 之後會組成 <tools>/yt-dlp/<id> 並建立 symlink，檔案內容不可信任：../ 之類的值會讓 symlink 指到別處。
	if st.PreviousID != "" && !ValidID(st.PreviousID) {
		return State{}, corruptError(tools, errors.New("previous_id 含有不合法的字元"))
	}
	if st.Pinned != "" && !ValidVersion(st.Pinned) {
		return State{}, corruptError(tools, errors.New("pinned 不是合法的版本"))
	}
	return st, nil
}

// saveState 以原子方式寫入狀態（先寫暫存檔再 rename），權限 0600。
func saveState(tools string, st State) error {
	dir := VersionsDir(tools)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp") // CreateTemp 的權限是 0600
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), statePath(tools)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
