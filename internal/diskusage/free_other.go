//go:build !unix

package diskusage

// freeBytes 在非 unix 平台沒有實作：可用空間是 null。
func freeBytes(string) (int64, bool) { return 0, false }
