package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`notesteme items ls --json`, []string{"notesteme", "items", "ls", "--json"}},
		{`  a   b  `, []string{"a", "b"}},
		{`notesteme export x --out "/tmp/my dir/a.srt"`, []string{"notesteme", "export", "x", "--out", "/tmp/my dir/a.srt"}},
		{`a 'b "c" d' e`, []string{"a", `b "c" d`, "e"}},
		{`a "" b`, []string{"a", "", "b"}},
		{`a b\ c`, []string{"a", "b c"}},
		{`a "x\"y"`, []string{"a", `x"y`}},
		{`--flag="a b"`, []string{"--flag=a b"}},
		// 雙引號內只有 \" 與 \\ 是跳脫，其他反斜線原樣保留。
		{`a "C:\x\y z"`, []string{"a", `C:\x\y z`}},
		{`a "x\\y"`, []string{"a", `x\y`}},
		{`a 'C:\x'`, []string{"a", `C:\x`}},
		{`a "p\qr\"s"`, []string{"a", `p\qr"s`}},
		{``, nil},
	}
	for _, c := range cases {
		got, err := SplitArgs(c.line)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitArgs(%q) = %q, %v；預期 %q", c.line, got, err, c.want)
		}
	}
	for _, bad := range []string{`a "b`, `a 'b`, `a\`} {
		if _, err := SplitArgs(bad); err == nil {
			t.Errorf("SplitArgs(%q) 應該失敗", bad)
		}
	}
}

// Dial 依設定位址的 port 對到 server 實際綁定的位址；沒有對應時失敗，不連到其他地方。
func TestWorldDial(t *testing.T) {
	w := NewT(t)
	ln, err := w.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
		close(accepted)
	}()

	c, err := w.Dial(context.Background(), "tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Dial：%v", err)
	}
	c.Close()
	<-accepted

	// loopback 與 localhost 都對到本機的 server。
	for _, addr := range []string{"localhost:8080", "[::1]:8080"} {
		c, err := w.Dial(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("Dial(%s)：%v", addr, err)
		}
		c.Close()
	}
	if _, err := w.Dial(context.Background(), "tcp", "127.0.0.1:9999"); err == nil || !strings.Contains(err.Error(), "9999") {
		t.Errorf("沒有對應 listener 時應該失敗，得到 %v", err)
	}
	// 其他 host 就算 port 相同也不連線（不會接到本機的 server）。
	for _, addr := range []string{"example.com:8080", "10.0.0.5:8080", "0.0.0.0:8080", "[2001:db8::1]:8080"} {
		if _, err := w.Dial(context.Background(), "tcp", addr); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("Dial(%s) 應該因為 host 不是 loopback 而失敗，得到 %v", addr, err)
		}
	}
	if _, err := w.Dial(context.Background(), "tcp", "no-port"); err == nil {
		t.Error("位址不正確時應該失敗")
	}
}
