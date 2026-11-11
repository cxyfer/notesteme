package subtitle

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTitleBytes 限制標題部分的長度，避免整個檔名超過檔案系統的 255 位元組。
const maxTitleBytes = 200

// ntfsForbidden 為 NTFS 不允許出現在檔名的字元。
const ntfsForbidden = `<>:"/\|?*`

// windowsReserved 為 Windows 保留的裝置名稱（不分大小寫，含副檔名時一樣保留）。
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sanitizeName 移除 NTFS 不允許的字元與控制字元，合併連續空白，去掉頭尾空白與尾端句點。
func sanitizeName(s string) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case strings.ContainsRune(ntfsForbidden, r), unicode.IsControl(r), r == utf8.RuneError:
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteRune(r)
	}
	return strings.TrimRight(sb.String(), ". ")
}

// DownloadFileName 產生下載檔名 `<標題>.<lang>.<ext>`（OUT-R4）。
// 標題中 NTFS 不允許的字元與控制字元會被移除，連續空白合成一個，
// 尾端的空白與句點會去掉；標題為空時用 untitled。
// 標題過長時截斷；結果會是 Windows 保留名稱（如 CON）時加上底線前綴。
func DownloadFileName(title, lang, ext string) string {
	t := sanitizeName(title)
	if len(t) > maxTitleBytes {
		t = t[:maxTitleBytes]
		for !utf8.ValidString(t) {
			t = t[:len(t)-1]
		}
		t = strings.TrimRight(t, ". ")
	}
	if t == "" {
		t = "untitled"
	}
	if windowsReserved[strings.ToUpper(t)] {
		t = "_" + t
	}
	name := t
	if l := sanitizeName(lang); l != "" {
		name += "." + l
	}
	if e := strings.TrimLeft(sanitizeName(ext), "."); e != "" {
		name += "." + e
	}
	return name
}

// ContentDisposition 產生 attachment 的 Content-Disposition 值（RFC 6266／5987）：
// 同時包含 ASCII 的 filename="…" 與 filename* 參數（UTF-8 百分比編碼）。
func ContentDisposition(filename string) string {
	var ascii strings.Builder
	for _, r := range filename {
		switch {
		case r < 0x20 || r == 0x7f || r > 0x7e || r == '"' || r == '\\':
			ascii.WriteByte('_')
		default:
			ascii.WriteRune(r)
		}
	}
	var enc strings.Builder
	for _, b := range []byte(filename) {
		if isAttrChar(b) {
			enc.WriteByte(b)
		} else {
			fmt.Fprintf(&enc, "%%%02X", b)
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii.String(), enc.String())
}

// isAttrChar 判斷是否為 RFC 5987 的 attr-char。
func isAttrChar(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", b) >= 0
}
