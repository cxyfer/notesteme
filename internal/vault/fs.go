package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// EscapeError 是 vault 內指向 vault 以外的連結（VAULT-R4）：這個檔案不寫入，publish 以 permanent 失敗。
type EscapeError struct {
	// Path 是相對於 vault 根目錄、遇到連結的路徑。
	Path string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("vault 內的 %s 是指向 vault 以外的連結，已拒絕寫入。", e.Path)
}

// CheckInside 確認 rel（相對於 root、以 / 分隔）一路上沒有離開 root 的連結（VAULT-R4）：逐段 Lstat，遇到 symlink 時
// 經由 root 解析（os.Root 只跟隨留在 root 之內的連結），解析失敗就是逃逸（含懸空的連結）。路徑的其餘部分不存在時停止。
// 檢查之後寫入之前被換成連結的情況，由 os.Root 的寫入本身拒絕（見 IsEscape）。
func CheckInside(root *os.Root, rel string) error {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("vault 內的路徑 %q 不正確", rel)
	}
	segs := strings.Split(rel, "/")
	for i := range segs {
		if segs[i] == "" || segs[i] == "." || segs[i] == ".." {
			return fmt.Errorf("vault 內的路徑 %q 不正確", rel)
		}
		prefix := strings.Join(segs[:i+1], "/")
		st, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			if IsEscape(err) {
				return &EscapeError{Path: prefix}
			}
			return err
		}
		if st.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		if _, err := root.Stat(prefix); err != nil {
			return &EscapeError{Path: prefix}
		}
	}
	return nil
}

// IsEscape 回報 err 是不是 os.Root 拒絕離開根目錄的錯誤（標準庫沒有匯出這個錯誤值，只能比對訊息）。
func IsEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}
