//go:build !unix

package ops

import "errors"

// DiskUsagePercent 在非 unix 平台沒有 statfs 可用：回報無法得知，磁碟用量告警這次不判斷。
func DiskUsagePercent(string) (int, error) {
	return 0, errors.New("這個平台無法查詢磁碟用量")
}
