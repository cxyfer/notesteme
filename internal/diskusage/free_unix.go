//go:build unix

package diskusage

import "golang.org/x/sys/unix"

// freeBytes 是 dir 所在檔案系統中一般使用者可用的空間。
func freeBytes(dir string) (int64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:gosec // 檔案系統的大小不會超過 int64
}
