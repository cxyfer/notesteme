package output

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// FileState 是輸出檔目前的內容與 notesteme 最後一次寫入時的紀錄的關係（DEL-R9）。
type FileState string

const (
	// StateIntact 是一般檔案，目前的 sha256 與紀錄相同：notesteme 寫的、沒被改過。
	StateIntact FileState = "intact"
	// StateModified 是紀錄以外的內容，或不是一般檔案（symlink、目錄）、太大不可能是字幕：視為使用者的檔案。
	StateModified FileState = "modified"
	// StateMissing 是檔案（或它的資料夾）已經不存在。
	StateMissing FileState = "missing"
)

// Inspect 檢查 dir 下的 name 是否還是 notesteme 寫的（sha256 等於 wantSHA）。
// 只用 os.Root 在 dir 之內操作，不跟隨 symlink；name 必須是單一檔名。
func Inspect(dir, name, wantSHA string) (FileState, error) {
	return inspect(dir, name, wantSHA, false)
}

// Remove 在檔案還是 notesteme 寫的（sha256 等於 wantSHA）時刪除 dir 下的 name，並回傳刪除前的狀態；
// 其他狀態（改過、不存在）不動它。比對雜湊與刪除之間若檔案被換掉（不是同一個檔案），視為改過、不刪。
func Remove(dir, name, wantSHA string) (FileState, error) {
	return inspect(dir, name, wantSHA, true)
}

func inspect(dir, name, wantSHA string, remove bool) (FileState, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return StateMissing, nil
	}
	if err != nil {
		return "", fmt.Errorf("開啟輸出資料夾：%w", err)
	}
	defer root.Close()

	lst, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return StateMissing, nil
	case err != nil:
		return "", err
	case !lst.Mode().IsRegular() || lst.Size() > maxHashBytes:
		return StateModified, nil
	}
	f, err := root.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return StateMissing, nil
		}
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(lst, opened) {
		return StateModified, false, nil // 檢查與開啟之間被換掉了
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxHashBytes+1)); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != wantSHA {
		return StateModified, nil
	}
	if !remove {
		return StateIntact, nil
	}
	// 刪除前再確認 name 仍然是剛才雜湊的那個檔案。
	if now, err := root.Lstat(name); err != nil || !os.SameFile(now, opened) {
		if err != nil && errors.Is(err, fs.ErrNotExist) {
			return StateMissing, nil
		}
		return StateModified, err
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return StateIntact, nil
}
