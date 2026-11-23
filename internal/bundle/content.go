package bundle

import (
	"bytes"
	"encoding/json"
	"image/jpeg"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 內容格式（BND-R7 的後半）：字幕以 cues JSON 的驗證器檢查，JSON 必須能解析，圖片必須能以 JPEG 解碼，筆記必須是 UTF-8。

// CuesFormat 是字幕產出的格式（ADR-0005）。
const CuesFormat = "notesteme-cues+json"

// maxImagePixels 限制 JPEG 的像素數，避免小檔案解碼成巨大的圖（decompression bomb）。
const maxImagePixels = 64 << 20

func errFormat(path, what string) *Error {
	s := ShowEntry(path)
	return invalid(s, "bundle 中的檔案「%s」不是有效的%s，已拒絕匯入。", s, what)
}

// ValidateContents 依產出的種類檢查每個檔案的內容；沒有被產出引用的檔案（畫面、筆記的圖片）必須是 JPEG。
func (b *Bundle) ValidateContents() error {
	used := map[string]bool{}
	for _, a := range b.Manifest.Artifacts {
		used[a.Path] = true
		d := b.files[a.Path]
		switch a.Kind {
		case "transcript":
			if a.Format != CuesFormat {
				return errFormat(a.Path, " notesteme-cues+json 字幕")
			}
			if _, err := subtitle.DecodeCuesJSON(bytes.NewReader(d)); err != nil || !json.Valid(d) {
				return errFormat(a.Path, " notesteme-cues+json 字幕")
			}
		case "note":
			if !utf8.Valid(d) {
				return errFormat(a.Path, " UTF-8 筆記")
			}
		default:
			if !json.Valid(d) {
				return errFormat(a.Path, " JSON")
			}
		}
	}
	for _, p := range b.order {
		if used[p] {
			continue
		}
		if !validJPEG(b.files[p]) {
			return errFormat(p, " JPEG 圖片")
		}
	}
	return nil
}

func validJPEG(d []byte) bool {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(d))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return false
	}
	_, err = jpeg.Decode(bytes.NewReader(d))
	return err == nil
}
