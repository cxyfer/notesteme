package cli

import (
	"os"
	"strings"
	"testing"
)

// backup、restore 由各自的 parseXxx 處理；實作前訊息與其他尚未實作的子命令相同，也不碰資料目錄。
func TestOpsSubcommandsSkeleton(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"backup", []string{"backup", "--out", "/x"}},
		{"restore", []string{"restore", "/x/notesteme-backup-latest.tar.gz"}},
	} {
		env, data := testEnv(t)
		res, _, stderr := run(t, env, c.args...)
		if res.Code != 1 || res.ErrorCode != "not_implemented" || res.Mode != "" {
			t.Errorf("%s：結果 = %+v", c.name, res)
		}
		if want := "notesteme " + c.name + " 尚未實作。"; !strings.Contains(stderr, want) {
			t.Errorf("%s：stderr = %q，預期包含 %q", c.name, stderr, want)
		}
		if _, err := os.Stat(data); err == nil {
			t.Errorf("%s：不應該碰資料目錄", c.name)
		}
	}
}
