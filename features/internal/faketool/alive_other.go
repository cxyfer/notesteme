//go:build !unix

package faketool

// pidAlive 在非 Unix 平台無法檢查程序是否存活，一律視為存活（只影響驗收測試對被 kill 程序的判斷）。
func pidAlive(pid int) bool { return pid > 0 }
