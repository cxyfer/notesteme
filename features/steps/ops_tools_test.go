package steps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// yt-dlp 已安裝：<data>/tools/yt-dlp/<版本>/ 是假 venv，yt-dlp-venv 是指向它的相對 symlink；換版本時 symlink 跟著換。
func TestYtDlpInstalledBuildsTheManagedLayout(t *testing.T) {
	s := newTestScenario(t)
	tools := ytdlp.ToolsDir(s.World.DataDir())
	for _, v := range []string{"2026.11.01", "2026.11.02"} {
		if err := s.opsYtDlpInstalled(v); err != nil {
			t.Fatal(err)
		}
		target, err := os.Readlink(ytdlp.LinkPath(tools))
		if err != nil || target != filepath.Join("yt-dlp", v) {
			t.Fatalf("symlink 指向 %q（%v），預期 yt-dlp/%s", target, err, v)
		}
		for _, f := range []string{"bin/python", "bin/yt-dlp", "pyvenv.cfg"} {
			if _, err := os.Stat(filepath.Join(ytdlp.LinkPath(tools), f)); err != nil {
				t.Errorf("%s：%v", f, err)
			}
		}
	}
}

// 登記可安裝的版本：通過的版本沒有失敗的影片，失敗的只有檢查影片的探測失敗；檢查影片本身存在，探測才會成功。
func TestYtDlpReleasesAreRegisteredInTheFakeState(t *testing.T) {
	s := newTestScenario(t)
	if err := s.opsYtDlpReleasePasses("2026.11.10"); err != nil {
		t.Fatal(err)
	}
	if err := s.opsYtDlpReleaseFails("2026.11.11"); err != nil {
		t.Fatal(err)
	}
	st, err := faketool.ReadState(s.World.Root)
	if err != nil {
		t.Fatal(err)
	}
	if rel := st.Releases["2026.11.10"]; rel == nil || len(rel.ProbeFailsFor) != 0 {
		t.Errorf("通過的版本：%+v", rel)
	}
	if rel := st.Releases["2026.11.11"]; rel == nil || strings.Join(rel.ProbeFailsFor, ",") != ytdlp.CheckVideoID {
		t.Errorf("失敗的版本：%+v", rel)
	}
	if v := st.Videos[ytdlp.CheckVideoID]; v == nil || v.Title == "" {
		t.Errorf("檢查影片沒有登記：%+v", v)
	}
}
