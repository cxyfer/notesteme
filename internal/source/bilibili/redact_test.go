package bilibili

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// yt-dlp 讀 cookie 檔時的警告會帶出 cookie 的值：進入錯誤的 Detail（也就是 log）之前要整行遮蔽。
func TestStderrCookieWarningIsRedactedFromDetail(t *testing.T) {
	stderr := "WARNING: skipping cookie file entry due to invalid expires at 'x': '.bilibili.com\tTRUE\t/\tTRUE\tx\tSESSDATA\tsecret-cookie-value'\n" +
		"ERROR: HTTP Error 500: Internal Server Error\n"
	err := classify(toolexec.Result{ExitCode: 1, Stderr: []byte(stderr)})
	e, ok := err.(*errclass.Error)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(e.Detail, "secret-cookie-value") || strings.Contains(err.Error(), "secret-cookie-value") {
		t.Errorf("Detail 含 cookie 的值：%q／%v", e.Detail, err)
	}
	if !strings.Contains(e.Detail, "500") {
		t.Errorf("其他行應保留：%q", e.Detail)
	}
}
