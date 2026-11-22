// Package fakehook 是驗收測試用的假 hook 程式 `notesteme-test-hook`（VAULT-R10、R11，M3）。
//
// 每次執行都把 argv、環境變數的名稱與值、工作目錄記錄到 Record 指定的檔案（一行一個 JSON），
// 讓步驟檢查 hook 收到的參數與環境。旗標（只看 argv 中的第一個旗標）：
//
//	--exit N         以結束碼 N 結束
//	--hang           一直等到被結束（測試逾時與 process group）
//	--print-bytes N  印出 N 個位元組的 x，再印 END-OF-HOOK-OUTPUT（測試輸出上限）
//
// 接進 faketool.Dispatch（依 argv[0] 分派）由 M3 的實作切片負責。
package fakehook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Name 是假 hook 在 PATH 中的名稱。
const Name = "notesteme-test-hook"

// Marker 是 --print-bytes 最後印出的文字。
const Marker = "END-OF-HOOK-OUTPUT"

// Run 是一次執行的紀錄。
type Run struct {
	Argv []string          `json:"argv"`
	Env  map[string]string `json:"env"`
	Dir  string            `json:"dir"`
}

// Main 執行假 hook：記錄到 record 後依旗標動作，回傳結束碼。sleep 讓測試可以替換。
func Main(record string, argv, environ []string, dir string, stdout io.Writer, sleep func(time.Duration)) int {
	env := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	b, _ := json.Marshal(Run{Argv: argv, Env: env, Dir: dir})
	if f, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
	args := argv
	if len(args) > 0 {
		args = args[1:]
	}
	if len(args) == 0 {
		return 0
	}
	num := func() int {
		if len(args) < 2 {
			return 0
		}
		n, _ := strconv.Atoi(args[1])
		return n
	}
	switch args[0] {
	case "--exit":
		return num()
	case "--hang":
		for {
			sleep(time.Hour)
		}
	case "--print-bytes":
		n := num()
		chunk := strings.Repeat("x", 4096)
		for n > 0 {
			k := min(n, len(chunk))
			fmt.Fprint(stdout, chunk[:k])
			n -= k
		}
		fmt.Fprintln(stdout, Marker)
	}
	return 0
}

// Runs 讀取 record 中的全部紀錄。
func Runs(record string) ([]Run, error) {
	b, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Run
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var r Run
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
