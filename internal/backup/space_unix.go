//go:build unix

package backup

import "syscall"

// freeSpace 回傳 dir 所在檔案系統，一般使用者可用的剩餘位元組數（statfs 的 Bavail）。
func freeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bsize 在不同平台的型別不同（int32／int64／uint32），一律轉成 uint64。
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert
}
