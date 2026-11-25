//go:build !(faultinject && unix)

package faultpoint

// Enabled 回報這個建置是否含故障注入（一般建置為 false）。
const Enabled = false

// Hit 在一般建置中什麼都不做，呼叫會被內聯掉。
func Hit(point, scope string) {}
