package steps

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

// 腳本化來源的前提由 production 宣告的已實作清單決定（source.Implemented）。
func TestScriptableSource(t *testing.T) {
	if err := scriptableSource("supadata"); err != nil {
		t.Errorf("supadata 已實作，應該可以腳本化：%v", err)
	}
	for _, id := range []string{"groq", "bilibili-ai", "whisper-1", "podcast-transcript", "youtube-auto"} {
		if err := scriptableSource(id); !errors.Is(err, godog.ErrPending) {
			t.Errorf("%s 應該讓場景 pending：%v", id, err)
		}
	}
}

func TestSizePattern(t *testing.T) {
	for _, ok := range []string{"25MB", "100 KB", "2GB"} {
		if !sizePattern.MatchString(ok) {
			t.Errorf("%q 應該有效", ok)
		}
	}
	for _, bad := range []string{"25", "MB", "1.5MB", "25mb"} {
		if sizePattern.MatchString(bad) {
			t.Errorf("%q 應該無效", bad)
		}
	}
}
