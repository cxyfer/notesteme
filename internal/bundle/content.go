package bundle

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image/jpeg"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 內容格式（BND-R7 的後半）：字幕以 cues JSON 的驗證器檢查，JSON 必須能解析，筆記必須是 UTF-8。
// 圖片只做結構檢查（標頭、標記序列、尺寸），不完整解碼：完整解碼留給真正要縮圖的呼叫端（BND 待決問題 N25）。

// CuesFormat 是字幕產出的格式（ADR-0005）。
const CuesFormat = "notesteme-cues+json"

// MaxImagePixels 是單張 JPEG 的像素上限，避免小檔案宣告成巨大的圖（decompression bomb）。
const MaxImagePixels = 16 << 20

// maxJPEGScans 是一張 JPEG 最多的 SOS 數：baseline 每個 component 最多一個 scan。
const maxJPEGScans = 4

func errFormat(path, what string) *Error {
	s := ShowEntry(path)
	return invalid(s, "bundle 中的檔案「%s」不是有效的%s，已拒絕匯入。", s, what)
}

// ValidateContents 同 ValidateContentsContext，不能取消。
func (b *Bundle) ValidateContents() error { return b.ValidateContentsContext(context.Background()) }

// ValidateContentsContext 依產出的種類檢查每個檔案的內容；沒有被產出引用的檔案（畫面、筆記的圖片）必須是 JPEG，
// 而且所有圖片的像素合計不能超過 Limits.MaxTotalPixels。每張圖之間檢查 ctx。
func (b *Bundle) ValidateContentsContext(ctx context.Context) error {
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
	budget := b.limits.withDefaults().MaxTotalPixels
	var pixels int64
	for _, p := range b.order {
		if used[p] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		n, ok := JPEGPixels(b.files[p])
		if !ok {
			return errFormat(p, " JPEG 圖片")
		}
		pixels += n
		if pixels > budget {
			s := ShowEntry(p)
			return invalid(s, "bundle 中的圖片合計超過 %d 百萬像素的上限（到「%s」為止），已拒絕匯入。", budget>>20, s)
		}
	}
	return nil
}

// JPEGPixels 檢查 d 是不是結構正確的 baseline JPEG，回傳像素數；不是時 ok 為 false。
// 只讀標頭與標記序列（O(檔案大小)，不配置像素），不保證熵編碼的資料能解碼。
func JPEGPixels(d []byte) (int64, bool) {
	if !jpegStructureOK(d) {
		return 0, false
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(d))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, false
	}
	n := int64(cfg.Width) * int64(cfg.Height)
	if n > MaxImagePixels {
		return 0, false
	}
	return n, true
}

// jpegStructureOK 走過 JPEG 的標記序列：SOI 開頭、EOI 結尾（之後沒有資料）、剛好一個 SOF0／SOF1、
// SOS 在 SOF 之後且最多 maxJPEGScans 個、只允許 baseline 會用到的標記。
// progressive（SOF2）一律拒絕：完整解碼時每個 scan 都要走過整張圖，掃描數可以被灌爆（BND 待決問題 N25）。
func jpegStructureOK(d []byte) bool {
	n := len(d)
	if n < 4 || d[0] != 0xFF || d[1] != 0xD8 || d[n-2] != 0xFF || d[n-1] != 0xD9 {
		return false
	}
	sof, scans := false, 0
	i := 2
	for i < n {
		if d[i] != 0xFF {
			return false
		}
		// 標記前可以有填充的 0xFF。
		for i < n && d[i] == 0xFF {
			i++
		}
		if i >= n {
			return false
		}
		m := d[i]
		if m == 0xD9 {
			return i == n-1 && sof && scans > 0
		}
		if i+2 >= n {
			return false
		}
		l := int(binary.BigEndian.Uint16(d[i+1:]))
		end := i + 1 + l
		if l < 2 || end > n {
			return false
		}
		switch {
		case m == 0xC0 || m == 0xC1:
			if sof || scans > 0 {
				return false
			}
			sof = true
		case m == 0xC4, m == 0xDB, m == 0xDD, m == 0xFE, m >= 0xE0 && m <= 0xEF:
		case m == 0xDA:
			scans++
			if !sof || scans > maxJPEGScans {
				return false
			}
			next, ok := skipEntropy(d, end)
			if !ok {
				return false
			}
			i = next
			continue
		default:
			// SOF2（progressive）、其他 SOFn、DAC、DNL、RSTn 在 scan 外、TEM 等都不接受。
			return false
		}
		i = end
	}
	return false
}

// skipEntropy 從 scan 資料的開頭找到下一個標記（不是 FF00 也不是 RSTn 的 0xFF）的位置。
func skipEntropy(d []byte, j int) (int, bool) {
	n := len(d)
	for j < n {
		k := bytes.IndexByte(d[j:], 0xFF)
		if k < 0 {
			return 0, false
		}
		j += k
		if j+1 >= n {
			return 0, false
		}
		switch b := d[j+1]; {
		case b == 0x00, b >= 0xD0 && b <= 0xD7:
			j += 2
		case b == 0xFF:
			// 填充：下一個 0xFF 才可能是標記的開始。
			j++
		default:
			return j, true
		}
	}
	return 0, false
}
