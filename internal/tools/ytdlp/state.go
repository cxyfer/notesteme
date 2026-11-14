package ytdlp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// LoadState 讀取狀態；檔案不存在時回傳空的 State。
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
		return State{}, fmt.Errorf("yt-dlp 的狀態檔 %s 無法解析：%w", statePath(tools), err)
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
