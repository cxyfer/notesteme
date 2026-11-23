//go:build unix

package ops

import "syscall"

// DiskUsagePercent 回傳 dir 所在檔案系統的用量百分比（同 df：已用 ÷（已用＋一般使用者可用），無條件進位）。
func DiskUsagePercent(dir string) (int, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return usagePercent(uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail)), nil //nolint:unconvert
}
