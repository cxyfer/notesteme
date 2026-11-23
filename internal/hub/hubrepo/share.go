package hubrepo

import (
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/bundle"
)

// 分享的檢查（HREP-R1、R3、R4）。

// 錯誤代碼。
const (
	CodeAttestRequired = "attest_required"
	CodeBundleInvalid  = bundle.CodeInvalid
	CodeBundleTooLarge = "bundle_too_large"
	CodeNotShareable   = "not_shareable"
	CodeLicenseInvalid = "license_invalid"
	CodeRemovedContent = "removed_content"
)

// SharePlatforms 是 Hub 接受的平台（ADR-0031 第 3 點）。
var SharePlatforms = []string{"youtube", "bilibili", "podcast"}

// Kinds 是 Hub 保存的產出種類（HREP-R17）；bundle 的 sponsor_segments 不分享（HREP 待決問題 R8）。
var Kinds = []string{"transcript", "chapters", "note", "frames"}

func notShareable(msg string) *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, CodeNotShareable, msg)
}

// ErrAttestRequired 是沒有確認有權分享（HREP-R1）。
func ErrAttestRequired() *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, CodeAttestRequired, "分享前要確認你有權分享這些內容。")
}

// ErrTooLarge 是超過上傳上限（HREP-R2）。
func ErrTooLarge(limit int64) *apierr.Error {
	return apierr.New(http.StatusRequestEntityTooLarge, CodeBundleTooLarge, "bundle 超過 "+bundle.FormatSize(limit)+" 的上傳上限。")
}

// ErrRemovedContent 是已依下架申請移除的內容（HREP-R9）。
func ErrRemovedContent() *apierr.Error {
	return apierr.New(http.StatusConflict, CodeRemovedContent, "這份內容已依下架申請移除，不能再上傳。")
}

// FromBundleError 把 bundle 的驗證錯誤轉成 422（HREP-R2：訊息同 BND-R10）。
func FromBundleError(e *bundle.Error) *apierr.Error {
	out := apierr.New(http.StatusUnprocessableEntity, e.Code, e.Message)
	if e.Path != "" {
		out.Errors = append(out.Errors, fieldError(e.Path, e.Message))
	}
	return out
}

// mediaExts 是音訊與影片的副檔名（小寫）。
var mediaExts = []string{".mp4", ".m4v", ".m4a", ".mov", ".mkv", ".webm", ".mka", ".avi", ".flv", ".wmv", ".ts", ".m2ts",
	".mpg", ".mpeg", ".mp3", ".aac", ".wav", ".flac", ".ogg", ".oga", ".ogv", ".opus", ".wma", ".aiff", ".amr", ".3gp"}

// IsMedia 依副檔名與內容簽章判斷是不是音訊或影片檔（HREP-R3）。
func IsMedia(name string, d []byte) bool {
	if slices.Contains(mediaExts, strings.ToLower(path.Ext(name))) {
		return true
	}
	at := func(off int, sig string) bool { return len(d) >= off+len(sig) && string(d[off:off+len(sig)]) == sig }
	switch {
	case at(4, "ftyp"), // MP4、MOV、M4A、3GP
		at(0, "\x1a\x45\xdf\xa3"), // Matroska、WebM
		at(0, "OggS"), at(0, "fLaC"), at(0, "ID3"), at(0, "#!AMR"),
		at(0, "RIFF") && (at(8, "WAVE") || at(8, "AVI ")),
		at(0, "FORM") && at(8, "AIFF"),
		at(0, "FLV"), at(0, "\x30\x26\xb2\x75\x8e\x66\xcf\x11"), // FLV、ASF（WMV、WMA）
		at(0, "\x00\x00\x01\xba"), at(0, "\x00\x00\x01\xb3"): // MPEG-PS
		return true
	}
	// MPEG 音訊（MP3、AAC ADTS）的 frame sync；JPEG 以 FF D8 開頭，不會誤判。
	if len(d) >= 2 && d[0] == 0xff && d[1]&0xe0 == 0xe0 && d[1] != 0xff {
		return true
	}
	return false
}

// CheckShareable 依 HREP-R3 的順序檢查能不能分享：平台、公開與否、需要登入的字幕、媒體檔，最後是授權。
// provs 與 b.Manifest.Artifacts 一一對應。
func CheckShareable(b *bundle.Bundle, provs []Provenance, licenses []string) *apierr.Error {
	it := b.Manifest.Item
	if !slices.Contains(SharePlatforms, it.Platform) {
		return notShareable("本機檔案不能分享到 Hub。")
	}
	if it.Availability != "public" {
		return notShareable("非公開影音不能分享到 Hub。")
	}
	for _, p := range provs {
		if (p.CookieRequired != nil && *p.CookieRequired) || (p.CookieRequired == nil && p.Source == "bilibili-ai") {
			return notShareable("需要登入才能取得的字幕不能分享到 Hub。")
		}
	}
	for _, name := range b.Paths() {
		d, _ := b.File(name)
		if IsMedia(name, d) {
			return notShareable("bundle 中不能包含媒體檔。")
		}
	}
	for _, a := range b.Manifest.Artifacts {
		if !slices.Contains(Kinds, a.Kind) {
			return notShareable("Hub 不接受這種產出：" + a.Kind + "。")
		}
	}
	if !slices.Contains(licenses, b.Manifest.License) {
		lic := b.Manifest.License
		if lic == "" {
			lic = "（沒有指定）"
		}
		return apierr.New(http.StatusUnprocessableEntity, CodeLicenseInvalid, "不支援的授權："+bundle.ShowEntry(lic)+"。")
	}
	return nil
}

// CheckProvenanceSize 確認每個產出的來源紀錄最多 16 KiB（HREP-R4）。
func CheckProvenanceSize(b *bundle.Bundle) *apierr.Error {
	for _, a := range b.Manifest.Artifacts {
		if len(a.Provenance) > MaxProvenanceBytes {
			return apierr.New(http.StatusUnprocessableEntity, CodeBundleInvalid,
				"產出「"+a.ID+"」的來源紀錄超過 16 KiB。")
		}
	}
	return nil
}

// ImagesOf 回傳屬於這個產出的圖片：沒有被產出引用、檔名以 artifacts/<產出 ID>- 開頭的檔案（BND-R1 的畫面圖片）。
func ImagesOf(b *bundle.Bundle, a bundle.Artifact) []string {
	used := map[string]bool{}
	for _, x := range b.Manifest.Artifacts {
		used[x.Path] = true
	}
	prefix := bundle.ArtifactsDir + a.ID + "-"
	var out []string
	for _, p := range b.Paths() {
		if !used[p] && strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix)) {
			out = append(out, p)
		}
	}
	return out
}
