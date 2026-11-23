package ops

// usagePercent 依 statfs 的區塊數計算用量百分比（同 df）：已用 ÷（已用＋一般使用者可用），無條件進位。
// 保留給 root 的區塊不算可用，所以一般使用者看到的「滿了」就是 100%。
func usagePercent(blocks, free, avail uint64) int {
	if blocks < free {
		return 0
	}
	used := blocks - free
	total := used + avail
	if total == 0 {
		return 0
	}
	return int((used*100 + total - 1) / total)
}
