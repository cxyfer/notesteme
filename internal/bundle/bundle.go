// Package bundle 解析與驗證 Notesteme bundle v0（`.notesteme.zip`，ADR-0005、BND 規格）。
//
// bundle 是不可信的輸入：Read 依 BND-R4～R7 的順序檢查項目名稱與種類、項目數、實際解壓的大小與壓縮比、
// manifest 的 schema，以及 zip 內容與 manifest 的雙向一致與 sha256，全部通過才回傳 Bundle；
// 內容格式（字幕、JSON、JPEG、UTF-8）由 ValidateContents 另外檢查，讓呼叫端（Hub）可以在中間插入自己的檢查。
// 錯誤一律是 *Error，訊息是 BND-R10 的原文。
//
// 這個套件只依賴標準函式庫與 internal/subtitle，Client（BND 匯入）與 Hub（HREP 分享）共用。
package bundle

import (
	"fmt"
	"strings"
	"unicode"
)

// 錯誤代碼（BND-R10）。
const (
	CodeInvalid            = "bundle_invalid"
	CodeUnsupportedVersion = "bundle_unsupported_version"
)

// ManifestName 是 manifest 的項目名稱；ArtifactsDir 是產出檔案的目錄。
const (
	ManifestName = "manifest.json"
	ArtifactsDir = "artifacts/"
)

// ManifestMaxBytes 是 manifest.json 解壓後的固定上限（BND-R5）。
const ManifestMaxBytes = 4 << 20

// ratioThreshold 以上（解壓超過 1 MiB）才檢查壓縮比（BND-R5、N4）。
const ratioThreshold = 1 << 20

// Limits 是解壓的上限（BND-R5）；值 <= 0 時用預設值。
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxRatio      int
}

// DefaultLimits 是 BND 規格的預設上限（N4）。
func DefaultLimits() Limits {
	return Limits{MaxFiles: 2000, MaxFileBytes: 64 << 20, MaxTotalBytes: 256 << 20, MaxRatio: 100}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxRatio <= 0 {
		l.MaxRatio = d.MaxRatio
	}
	return l
}

// Options 是 Read 的選項。
type Options struct {
	Limits Limits
	// InstanceIDOptional 為 true 時 created_by.instance_id 可以省略：Client 送到 Hub 的 bundle 不帶它
	// （ADR-0032 第 2 點、HREP 待決問題 R1）。實例之間搬移（BND 匯入）維持必填。
	InstanceIDOptional bool
}

// Error 是 bundle 不通過驗證的原因（BND-R10）。
type Error struct {
	Code    string
	Message string
	// Entry 是出錯的項目名稱（已清理控制字元）；沒有時是空字串。
	Entry string
	// Path 是 manifest 欄位的點號路徑（BND-R6）；不是 manifest 的錯誤時是空字串。
	Path string
}

func (e *Error) Error() string { return e.Message }

func invalid(entry, format string, args ...any) *Error {
	return &Error{Code: CodeInvalid, Message: fmt.Sprintf(format, args...), Entry: entry}
}

// maxShownEntry 是訊息中項目名稱最多顯示的字元數（BND-R10）。
const maxShownEntry = 200

// ShowEntry 把項目名稱轉成可以放進訊息的文字：控制字元與不合法的 UTF-8 換成 �，最多 200 個字元。
func ShowEntry(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if n == maxShownEntry {
			break
		}
		if r == unicode.ReplacementChar || unicode.IsControl(r) {
			r = unicode.ReplacementChar
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// FormatSize 把位元組數寫成給人看的大小（整除 GiB、MiB、KiB 時用該單位），例如 64 MiB、1 KiB。
func FormatSize(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// Bundle 是通過結構驗證的 bundle：manifest 與每個檔案的內容（已確認與 manifest 的 sha256、size 相符）。
type Bundle struct {
	Manifest Manifest
	// ManifestJSON 是 manifest.json 的原始內容。
	ManifestJSON []byte
	files        map[string][]byte
	order        []string
}

// File 回傳 manifest.json 以外的一個檔案內容。
func (b *Bundle) File(path string) ([]byte, bool) {
	d, ok := b.files[path]
	return d, ok
}

// Paths 依 zip 中的順序回傳 manifest.json 以外的檔案路徑。
func (b *Bundle) Paths() []string { return append([]string(nil), b.order...) }
