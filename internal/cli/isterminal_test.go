package cli

import (
	"io"
	"os"
	"strings"
	"testing"
)

// 預設的 isTerminal 要真的判斷終端機：/dev/null 是字元裝置，但不是終端機；
// 排程或容器裡 stdin 常接 /dev/null，不能因此詢問確認而讀到 EOF。
func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	closed, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()

	cases := []struct {
		name string
		r    io.Reader
	}{
		{"/dev/null", null},
		{"pipe", pr},
		{"已關閉的檔案", closed},
		{"nil *os.File", (*os.File)(nil)},
		{"strings.Reader", strings.NewReader("y\n")},
		{"nil", nil},
	}
	for _, c := range cases {
		if isTerminal(c.r) {
			t.Errorf("%s 不是終端機，isTerminal 不該回傳 true", c.name)
		}
	}
}
