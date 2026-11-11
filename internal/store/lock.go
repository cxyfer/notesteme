package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LockFileName 是 /data 下的鎖專用檔（ADR-0007、ADR-0015）。
const LockFileName = "notesteme.lock"

// ErrLocked 表示資料目錄已被另一個 serve 持有（CLI-R1）。
//
// TODO: 持有者是獨立模式的 CLI 時要有不同的訊息，之後依鎖檔的 holder 區分。
var ErrLocked = errors.New("資料目錄已經由另一個 notesteme serve 使用中。")

// LockInfo 是寫進鎖檔的內容，client 模式的 CLI 依此連到 serve。
type LockInfo struct {
	// Holder 是持有者的種類："serve" 或 "cli"。
	Holder string `json:"holder"`
	PID    int    `json:"pid"`
	// Listen 是 serve 實際監聽的位址；cli 持有時為空。
	Listen string `json:"listen,omitempty"`
}

// DirLock 是資料目錄的 flock。
type DirLock struct {
	f *os.File
}

// LockDir 對 dir/notesteme.lock 取得排他的 flock（不等待）。dir 必須已存在。
// 已被持有時回傳 ErrLocked。
func LockDir(dir string) (*DirLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, LockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("開啟鎖檔失敗：%w", err)
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("鎖定資料目錄失敗：%w", err)
	}
	return &DirLock{f: f}, nil
}

// WriteInfo 以 info 取代鎖檔內容。
func (l *DirLock) WriteInfo(info LockInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	_, err = l.f.WriteAt(append(b, '\n'), 0)
	return err
}

// Release 清空鎖檔內容並釋放 flock（正常關閉）。
func (l *DirLock) Release() error {
	if l.f == nil {
		return nil
	}
	_ = l.f.Truncate(0)
	return l.Close()
}

// Close 只關閉檔案、釋放 flock，不動內容（模擬程序中斷）。
func (l *DirLock) Close() error {
	if l.f == nil {
		return nil
	}
	unflock(l.f)
	err := l.f.Close()
	l.f = nil
	return err
}

// ReadLockInfo 讀取 dir 的鎖檔內容；沒有鎖檔或內容為空時回傳 nil。
func ReadLockInfo(dir string) (*LockInfo, error) {
	b, err := os.ReadFile(filepath.Join(dir, LockFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var info LockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, fmt.Errorf("鎖檔內容不正確：%w", err)
	}
	return &info, nil
}
