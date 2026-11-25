package faketool

import (
	"strings"
	"testing"
)

// TestOutageFailsEveryVideoOperation：State.Outage 讓所有影音網址的操作都失敗（PIPE-R17），--version 照常成功。
func TestOutageFailsEveryVideoOperation(t *testing.T) {
	st := sampleState(t)
	st.Outage = &Failure{Stderr: "ERROR: Temporary failure in name resolution", ExitCode: 1}
	s := newScenario(t, st)
	if code, out, _ := s.exec("yt-dlp", "--version"); code != 0 || strings.TrimSpace(out) != DefaultYtDlpVersion {
		t.Fatalf("--version：code=%d out=%q", code, out)
	}
	for _, args := range [][]string{
		{"-J", "--no-playlist", "https://www.youtube.com/watch?v=abc123xxxxx"},
		{"--skip-download", "--write-subs", "--sub-langs", "en", "--sub-format", "vtt", "-o", "subs/%(id)s.%(ext)s", "https://youtu.be/abc123xxxxx"},
	} {
		code, _, errs := s.exec("yt-dlp", args...)
		if code != 1 || !strings.Contains(errs, "Temporary failure in name resolution") {
			t.Errorf("%v：code=%d stderr=%q", args, code, errs)
		}
	}
}
