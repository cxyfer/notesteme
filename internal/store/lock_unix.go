//go:build unix

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errWouldBlock = errors.New("would block")

// unflock 在關閉檔案前釋放 f 的鎖；unix 上關閉檔案就會釋放，不需要額外動作。
func unflock(*os.File) {}

// flock 以 flock(2) 對 f 取得排他鎖（LOCK_NB）。
// 用 flock 而不是 fcntl：Linux 上同一程序內兩個 open file description 的 flock 也會互斥。
func flock(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return errWouldBlock
		default:
			return err
		}
	}
}
