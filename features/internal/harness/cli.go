package harness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// SplitArgs 把一行指令切成參數：以空白分隔，支援單引號、雙引號與反斜線跳脫（引號內的空白保留）。
// 引號沒有結束時回傳錯誤。
func SplitArgs(line string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inArg   bool
		quote   rune
		escaped bool
	)
	flush := func() {
		if inArg {
			args = append(args, cur.String())
			cur.Reset()
			inArg = false
		}
	}
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inArg = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inArg = r, true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("引號沒有結束：%s", line)
	}
	if escaped {
		return nil, fmt.Errorf("結尾的反斜線沒有對象：%s", line)
	}
	flush()
	return args, nil
}

// Dial 是 CLI 的 app.Env.Dial（--server 的 client 模式）：TCP 位址依設定的 port 對到 server 實際綁定的位址
// （listener 一律綁 127.0.0.1:0，見 Listen）；unix 照原樣連線。
// 沒有對應的 listener 時回傳錯誤，不連到其他地方。
func (w *World) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	if network == "unix" {
		return d.DialContext(ctx, network, addr)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("位址 %q 不正確：%w", addr, err)
	}
	var actual string
	for _, l := range w.Listens() {
		// 後來的啟動覆蓋先前的（重啟之後同一個設定位址對到新的 listener）。
		if _, p, err := net.SplitHostPort(l.Configured); err == nil && p == port {
			actual = l.Actual
		}
	}
	if actual == "" {
		return nil, errors.New("沒有 listener 的設定位址使用 port " + port + "，測試的 CLI 只能連到 server 的 loopback 位址")
	}
	return d.DialContext(ctx, network, actual)
}
