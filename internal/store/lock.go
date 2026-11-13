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

// ErrLocked 表示資料目錄已被另一個程序持有（CLI-R1）。訊息是 serve 持有時的版本，
// 持有者是獨立模式的 CLI 時，呼叫端要讀 ReadLockInfo 依 Holder 另外說明（見 app.lockedError）。
var ErrLocked = errors.New("資料目錄已經由另一個 notesteme serve 使用中。")

// LockInfo 是寫進鎖檔的內容，client 模式的 CLI 依此連到 serve。
type LockInfo struct {
	// Holder 是持有者的種類："serve" 或 "cli"。
	Holder string `json:"holder"`
	PID    int    `json:"pid"`
	// Listen 是 serve 實際監聽的位址；cli 持有時為空。
	Listen string `json:"listen,omitempty"`
	// Socket 是 serve 的本機 Unix socket 路徑（client 模式的 CLI 連到這裡）；cli 持有時為空。
	Socket string `json:"socket,omitempty"`
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
	// 取得鎖之後立刻清空：上一個持有者（例如被中斷的 serve）留下的內容不再有效，
	// 其他程序讀到空內容就知道「持有者還沒寫入連線資訊」，而不是連到舊的 socket。
	if err := f.Truncate(0); err != nil {
		unflock(f)
		_ = f.Close()
		return nil, fmt.Errorf("清空鎖檔失敗：%w", err)
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
