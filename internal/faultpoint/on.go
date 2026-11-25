//go:build faultinject && unix

package faultpoint

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Enabled 回報這個建置是否含故障注入（faultinject 建置為 true）。
const Enabled = true

// 環境變數。只有 faultinject 建置會讀，production 程式一律經過 app.Env（ADR-0015），這裡是唯一的例外。
const (
	EnvFault = "NOTESTEME_FAULT"
	EnvTrace = "NOTESTEME_FAULT_TRACE"
)

var (
	once   sync.Once
	target string
	nth    int
	trace  *os.File

	mu     sync.Mutex
	counts = map[string]int{}
)

func load() {
	if v := os.Getenv(EnvFault); v != "" {
		i := strings.LastIndexByte(v, ':')
		n, err := strconv.Atoi(v[i+1:])
		if i <= 0 || err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "faultpoint: %s=%q 的格式應該是 <名稱>:<n>（n ≥ 1）\n", EnvFault, v)
			os.Exit(3)
		}
		target, nth = v[:i], n
	}
	if p := os.Getenv(EnvTrace); p != "" {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "faultpoint: 開啟 %s 失敗：%v\n", p, err)
			os.Exit(3)
		}
		trace = f
	}
}

// Hit 記錄一次命中；命中 NOTESTEME_FAULT 指定的點第 n 次時，對自己送 SIGKILL，不會返回。
func Hit(point, scope string) {
	once.Do(load)
	if target == "" && trace == nil {
		return
	}
	name := Name(point, scope)
	mu.Lock()
	defer mu.Unlock()
	counts[name]++
	n := counts[name]
	if trace != nil {
		// 每行一次 write（O_APPEND），被殺時最多少掉這一行；kill -9 不影響已寫入 page cache 的內容。
		_, _ = trace.WriteString(name + "\n")
	}
	if name != target || n != nth {
		return
	}
	if trace != nil {
		_, _ = trace.WriteString("KILL " + name + " " + strconv.Itoa(n) + "\n")
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {} // SIGKILL 送達之前不讓呼叫端繼續往下做
}
