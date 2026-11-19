package youtube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

const cookieWarning = "WARNING: skipping cookie file entry due to invalid expires at 'x': '.youtube.com\tTRUE\t/\tTRUE\tx\tSID\tsecret-cookie-value'\n"

// yt-dlp 讀 cookie 檔時的警告會帶出 cookie 的值：進入錯誤的 Detail（也就是 log）之前要整行遮蔽。
func TestStderrCookieWarningIsRedactedFromDetail(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(cookieWarning + "ERROR: HTTP Error 500: Internal Server Error\n")}}
	_, err := newClient(t, r).Probe(context.Background(), "x")
	var e *errclass.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(e.Detail, "secret-cookie-value") || strings.Contains(err.Error(), "secret-cookie-value") {
		t.Errorf("Detail 含 cookie 的值：%q／%v", e.Detail, err)
	}
	if !strings.Contains(e.Detail, "500") {
		t.Errorf("其他行應保留：%q", e.Detail)
	}
}

func TestPlaylistGoneDetailRedactsCookieWarning(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(cookieWarning + "ERROR: [youtube:tab] x: The playlist does not exist.\n")}}
	_, err := newClient(t, r).Playlist(context.Background(), "PLxxxxxxxxxxxx")
	if err == nil || strings.Contains(err.Error(), "secret-cookie-value") {
		t.Errorf("err = %v", err)
	}
}
