//go:build !unix

package store

import (
	"errors"
	"os"
	"sync"
)

var errWouldBlock = errors.New("would block")

// 非 unix 平台沒有 flock：退回成程序內的互斥，只能防止同一程序重複開啟。
// TODO: Windows 改用 LockFileEx。
var (
	heldMu sync.Mutex
	held   = map[string]bool{}
)

// unflock 在關閉檔案前釋放 f 的鎖。
func unflock(f *os.File) {
	heldMu.Lock()
	defer heldMu.Unlock()
	delete(held, f.Name())
}

func flock(f *os.File) error {
	heldMu.Lock()
	defer heldMu.Unlock()
	if held[f.Name()] {
		return errWouldBlock
	}
	held[f.Name()] = true
	return nil
}
