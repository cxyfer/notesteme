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

	if _, err := w.Dial(context.Background(), "tcp", "example.com:9999"); err == nil || !strings.Contains(err.Error(), "9999") {
		t.Errorf("沒有對應 listener 時應該失敗，得到 %v", err)
	}
	if _, err := w.Dial(context.Background(), "tcp", "no-port"); err == nil {
		t.Error("位址不正確時應該失敗")
	}
}
