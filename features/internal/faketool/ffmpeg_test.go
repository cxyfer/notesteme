package faketool

import (
	"strings"
	"testing"
)

func TestFFmpegBuildconf(t *testing.T) {
	// 預設含 libass；-hide_banner 可有可無。
	s := newScenario(t, State{})
	for _, args := range [][]string{{"-buildconf"}, {"-hide_banner", "-buildconf"}} {
		code, out, _ := s.exec("ffmpeg", args...)
		if code != 0 || !strings.Contains(out, "--enable-libass") || !strings.Contains(out, "--enable-gpl") {
			t.Errorf("%v：code=%d out=%q", args, code, out)
		}
	}
	if calls, _ := ReadCalls(s.root); len(calls) != 2 {
		t.Errorf("每次呼叫都應記錄：%v", calls)
	}

	// FFmpegNoLibass 時不含 libass，其餘設定仍在。
	s = newScenario(t, State{FFmpegNoLibass: true})
	code, out, _ := s.exec("ffmpeg", "-hide_banner", "-buildconf")
	if code != 0 || strings.Contains(out, "libass") || !strings.Contains(out, "--enable-gpl") {
		t.Errorf("沒有 libass：code=%d out=%q", code, out)
	}

	// 其他參數組合仍然是用法錯誤。
	for _, args := range [][]string{{"-buildconf", "-x"}, {"-x", "-buildconf"}, {"-hide_banner"}} {
		if code, _, _ := s.exec("ffmpeg", args...); code != 2 {
			t.Errorf("%v：code=%d，預期 2", args, code)
		}
	}
}
