package cookies

import (
	"errors"
	"strings"
	"testing"
)

// Netscape 格式的每個欄位都不能含控制字元（與標頭格式一致）：
// 否則內容會被寫進 cookie 檔，造成欄位錯位或注入多餘的行。
func TestNormalizeNetscapeRejectsControlCharacters(t *testing.T) {
	for _, ctrl := range []struct{ name, ch string }{
		{"CR", "\r"}, {"NUL", "\x00"}, {"ESC", "\x1b"}, {"SOH", "\x01"},
	} {
		for _, f := range []string{"domain", "path", "name", "value"} {
			t.Run(ctrl.name+"/"+f, func(t *testing.T) {
				fields := map[string]string{"domain": ".youtube.com", "path": "/", "name": "SID", "value": "abc-secret"}
				fields[f] = fields[f][:1] + ctrl.ch + fields[f][1:]
				if f == "domain" {
					fields[f] = "x" + ctrl.ch + ".youtube.com" // 仍然是平台網域的子網域
				}
				line := strings.Join([]string{fields["domain"], "TRUE", fields["path"], "TRUE", "0", fields["name"], fields["value"]}, "\t")
				got, n, err := Normalize("youtube", "# Netscape HTTP Cookie File\n"+line+"\n")
				if !errors.Is(err, ErrInvalidFormat) || got != "" || n != 0 {
					t.Fatalf("Normalize = %q, %d, %v，預期 ErrInvalidFormat", got, n, err)
				}
				if strings.Contains(err.Error(), "abc-secret") {
					t.Errorf("錯誤訊息含 cookie 的值：%v", err)
				}
			})
		}
	}
}
