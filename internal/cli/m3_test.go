package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// M3 的子命令（M3-S0）：送出規格的路由；server 回 501 時以結束碼 1 結束，訊息說明尚未實作。
func TestM3CommandsCallTheirRoutes(t *testing.T) {
	f, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 501, `{"code":"not_implemented","message":"這個功能尚未實作。"}`)
	})
	out := filepath.Join(t.TempDir(), "f.jpg")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"frames", "ls", "yt:abc123xxxxx"}, "GET /api/items/yt:abc123xxxxx/frames"},
		{[]string{"frames", "ls", "yt:abc123xxxxx", "--all"}, "GET /api/items/yt:abc123xxxxx/frames?selected_only=false"},
		{[]string{"frames", "get", "yt:abc123xxxxx", "3", "-o", out}, "GET /api/items/yt:abc123xxxxx/frames/3?size=full"},
		{[]string{"note", "show", "yt:abc123xxxxx"}, "GET /api/items/yt:abc123xxxxx/note?format=md"},
		{[]string{"note", "show", "yt:abc123xxxxx", "--lang", "en", "--format", "json"}, "GET /api/items/yt:abc123xxxxx/note?lang=en"},
	}
	for _, c := range cases {
		before := len(f.requests)
		res, _, errOut := run(t, app.Env{}, append(c.args, "--server", url, "--token", "t")...)
		if res.Code != exitError || !strings.Contains(errOut, "尚未實作") {
			t.Errorf("%v：code %d，stderr %q", c.args, res.Code, errOut)
		}
		if len(f.requests) != before+1 || f.requests[before] != c.want {
			t.Errorf("%v：請求 %v，預期 %s", c.args, f.requests[before:], c.want)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("失敗時不該寫出檔案")
	}
}

func TestM3CommandsUsage(t *testing.T) {
	for _, args := range [][]string{
		{"frames"}, {"frames", "rm", "x"}, {"frames", "ls"}, {"frames", "get", "x", "0", "-o", "f"},
		{"frames", "get", "x", "1"}, {"note"}, {"note", "show"}, {"note", "show", "x", "--format", "html"},
	} {
		if res, _, _ := run(t, app.Env{}, args...); res.Code != exitUsage {
			t.Errorf("%v：code %d，預期 %d", args, res.Code, exitUsage)
		}
	}
}
