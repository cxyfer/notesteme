package cli

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/compat"
)

// CLI-R9：CLI 每個請求帶自己的版本；serve 自己不擋（例如較舊的 serve）、只回報版本時，CLI 也會依回應標頭擋下，
// 指出要升級哪一邊；沒有回報版本的 serve 視為相容。
func TestCLIChecksServeVersionHeader(t *testing.T) {
	for _, c := range []struct {
		serve    string
		wantCode int
		wantOut  string
	}{
		{"0.3.0", 1, "serve 版本太舊（serve 0.3.0，CLI 0.5.0），請升級 serve，或換成 0.3 版的 CLI。"},
		{"0.7.0", 1, "CLI 版本太舊（CLI 0.5.0，serve 0.7.0），請升級 CLI。"},
		{"0.4.9", 0, ""},
		{"", 0, ""},
	} {
		var sent string
		_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
			sent = r.Header.Get(compat.Header)
			if c.serve != "" {
				w.Header().Set(compat.Header, c.serve)
			}
			writeJSON(w, 200, `{"items":[]}`)
		})
		env := app.Env{Vars: map[string]string{}, Test: &app.TestControls{Version: "0.5.0"}}
		data := filepath.Join(shortTempDir(t), "nonexistent")
		res, out, errOut := run(t, env, "items", "ls", "--server", url, "--token", "tok", "--data", data)
		if sent != "0.5.0" {
			t.Errorf("serve %q：CLI 送出的版本 = %q", c.serve, sent)
		}
		if res.Code != c.wantCode || !strings.Contains(out+errOut, c.wantOut) {
			t.Errorf("serve %q：結束碼 %d，輸出 %q %q", c.serve, res.Code, out, errOut)
		}
	}
}
