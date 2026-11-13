package source

import (
	"slices"

	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// implemented 是產品已經實作的字幕來源 ID（steps.md 第 3 節）。
// 新增來源的實作時，同一個 commit 把它加進來；驗收測試依它判斷
// 「腳本化某個來源」的前提能不能成立，還沒實作的來源讓場景停在 Given 並標為 pending。
var implemented = []string{
	transcript.SourceYouTubeManual,
	transcript.SourceYouTubeAuto,
	supadata.ID,
}

// Implemented 回報字幕來源 id 是否已經實作。
func Implemented(id string) bool { return slices.Contains(implemented, id) }

// ImplementedSources 回傳已經實作的字幕來源 ID。
func ImplementedSources() []string { return slices.Clone(implemented) }
