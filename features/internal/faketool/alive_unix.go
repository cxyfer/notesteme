//go:build unix

package faketool

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// pidAlive 回報 pid 的程序是否還存活。
// kill(pid, 0) 不送出訊號，只檢查程序存在；對已結束但還沒被回收的殭屍程序它也會成功，
// 所以 Linux 上再看 /proc/<pid>/stat 的狀態，殭屍（Z）視為已結束。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // 沒有 /proc（非 Linux）時只能相信 kill 的結果
	}
	// 格式：pid (comm) state ...；comm 可能含空白與括號，從最後一個 ')' 之後取狀態。
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return true
	}
	return s[i+2] != 'Z'
}
