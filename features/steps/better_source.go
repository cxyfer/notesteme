package steps

import (
	"fmt"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// platformAddsCaptions 讓平台在 at（RFC 3339）才補上一條字幕軌（steps.md 4.17，SRC-10）：
// 假 yt-dlp 依 fake clock（harness 寫入的 now 檔）決定是否列出、能否下載。
func (s *Scenario) platformAddsCaptions(kind, lang, ref, at string) error {
	switch kind {
	case faketool.KindManual, faketool.KindAuto:
	default:
		return fmt.Errorf("字幕軌的種類 %q 不正確，應是 manual 或 auto", kind)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		return fmt.Errorf("時間 %q 不是 RFC 3339：%w", at, err)
	}
	if !strings.HasPrefix(ref, "yt:") && !strings.HasPrefix(ref, "bili:") {
		return fmt.Errorf("%q 不是平台影音的代號（yt: 或 bili:）", ref)
	}
	if err := s.editVideo(ref, func(v *faketool.Video) error {
		v.Later = append(v.Later, faketool.Track{Kind: kind, Lang: lang, AvailableAt: at})
		return nil
	}); err != nil {
		return err
	}
	return s.World.SyncNow()
}
