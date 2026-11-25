package nfrbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/benchdata"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// ytRunner 是 in-process server 的 yt-dlp 替身（不連網路）：--version 回傳版本，-J 回傳一部有人工英文字幕的
// 公開影片，--skip-download 寫出 vtt（cues 句）。ffmpeg 等其他工具一律回傳失敗。
type ytRunner struct {
	vtt []byte
	// durationS 是 -J 回報的片長。
	durationS int
}

var videoID = regexp.MustCompile(`v=([A-Za-z0-9_-]{11})`)

func newYTRunner(cues int) *ytRunner {
	cs := benchdata.Cues(cues, "en", 42)
	var sb strings.Builder
	_ = subtitle.RenderVTT(&sb, cs)
	return &ytRunner{vtt: []byte(sb.String()), durationS: int(cs[len(cs)-1].EndMS/1000) + 1}
}

func (r *ytRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	switch {
	case slices.Contains(c.Args, "--version"):
		return toolexec.Result{Stdout: []byte("2026.11.01\n")}, nil
	case slices.Contains(c.Args, "-J"):
		id := "benchxxxxxx"
		if m := videoID.FindStringSubmatch(strings.Join(c.Args, " ")); m != nil {
			id = m[1]
		}
		return toolexec.Result{Stdout: []byte(ytInfo(id, r.durationS))}, nil
	case slices.Contains(c.Args, "--skip-download"):
		i := slices.Index(c.Args, "-o")
		if i < 0 || i+1 >= len(c.Args) {
			return toolexec.Result{ExitCode: 2, Stderr: []byte("no -o")}, nil
		}
		out := strings.Replace(c.Args[i+1], "%(ext)s", "en.vtt", 1)
		return toolexec.Result{}, os.WriteFile(out, r.vtt, 0o600)
	}
	return toolexec.Result{ExitCode: 2, Stderr: []byte("unexpected: " + strings.Join(c.Args, " "))}, nil
}

func ytInfo(id string, durationS int) string {
	return fmt.Sprintf(`{"id":%q,"title":"Bench video %s","duration":%d,"language":"en","availability":"public",`+
		`"live_status":"not_live","channel_id":"UCbenchxxxxxxxxxxxxxxxxx","channel":"Bench","upload_date":"20261101",`+
		`"subtitles":{"en":[{"ext":"vtt","url":"https://example.invalid/en.vtt"}]},"automatic_captions":{}}`, id, id, durationS)
}

// writeFakeYtDlpScript 寫出給真正的 binary 使用的 yt-dlp 替身（shell script，不連網路），vtt 放在 bin 旁邊。
// toolexec 只傳 allowlist 內的環境變數，所以路徑直接寫進 script。
func writeFakeYtDlpScript(dir string, cues int) error {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	r := newYTRunner(cues)
	vtt := filepath.Join(dir, "fixture.en.vtt")
	if err := os.WriteFile(vtt, r.vtt, 0o644); err != nil {
		return err
	}
	info := ytInfo("__ID__", r.durationS)
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    --version) echo 2026.11.01; exit 0 ;;
    -J) J=1 ;;
    --skip-download) S=1 ;;
  esac
done
if [ -n "$J" ]; then
  id=$(printf '%s' "$*" | /usr/bin/sed -n 's/.*v=\([A-Za-z0-9_-]\{11\}\).*/\1/p')
  printf '%s\n' '` + info + `' | /usr/bin/sed "s/__ID__/$id/g"
  exit 0
fi
if [ -n "$S" ]; then
  prev=""
  for a in "$@"; do
    if [ "$prev" = "-o" ]; then out="$a"; fi
    prev="$a"
  done
  out=$(printf '%s' "$out" | /usr/bin/sed 's/%(ext)s/en.vtt/')
  /bin/cp '` + vtt + `' "$out"
  exit 0
fi
echo "unexpected: $*" >&2
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte("#!/bin/sh\necho 'ffmpeg version 7.0'\n"), 0o755)
}

// writeNoNetConfig 寫出讓 binary 不連外的設定檔：SponsorBlock 指向不存在的 loopback 埠（SPB-R5 存成空的產出）。
func writeNoNetConfig(dir string) error {
	return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("sponsorblock:\n  base_url: http://127.0.0.1:9\n"), 0o600)
}
