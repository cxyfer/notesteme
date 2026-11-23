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
		{[]string{"frames", "ls", "yt:abc123xxxxx", "--all"}, "GET /api/items/yt:abc123xxxxx/frames"}, // --all 在 client 端過濾（M3-S3）
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

// frames ls／get 成功時（M3-S3）：預設只列選中的畫面，--all 列出全部；get 寫出 API 回傳的圖片。
func TestFramesCommandsOutput(t *testing.T) {
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/frames/1") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
			return
		}
		writeJSON(w, 200, `{"frames":[{"n":1,"at":"00:00:05.000","selected":true,"caption":"Title\nslide","chapter":"Intro"},`+
			`{"n":2,"at":"00:03:10.000","selected":false,"excluded":"sponsor","caption":"","chapter":""}]}`)
	})
	res, out, _ := run(t, app.Env{}, "frames", "ls", "yt:abc123xxxxx", "--server", url, "--token", "t")
	if res.Code != exitOK || out != "*   1  00:00:05.000  Title slide  [Intro]\n" {
		t.Errorf("ls：code %d，stdout %q", res.Code, out)
	}
	res, out, _ = run(t, app.Env{}, "frames", "ls", "yt:abc123xxxxx", "--all", "--server", url, "--token", "t")
	if res.Code != exitOK || !strings.Contains(out, "x   2  00:03:10.000") {
		t.Errorf("ls --all：code %d，stdout %q", res.Code, out)
	}
	p := filepath.Join(t.TempDir(), "f.jpg")
	if res, _, errOut := run(t, app.Env{}, "frames", "get", "yt:abc123xxxxx", "1", "-o", p, "--server", url, "--token", "t"); res.Code != exitOK {
		t.Fatalf("get：code %d，%s", res.Code, errOut)
	}
	if b, err := os.ReadFile(p); err != nil || len(b) != 3 || b[0] != 0xFF {
		t.Errorf("寫出的圖片 = %v，%v", b, err)
	}
}
