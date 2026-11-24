package bundle

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 項目名稱與種類（BND-R4）、項目數與解壓上限（BND-R5）、manifest 與檔案的一致性（BND-R7 的前半）。

// maxNameBytes 是項目名稱的上限（BND-R4）。
const maxNameBytes = 255

var (
	artifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	driveLetter  = regexp.MustCompile(`^[A-Za-z]:`)
)

const msgNotZip = "這不是 Notesteme bundle（無法以 zip 讀取）。"

// msgNoManifest 不在 BND-R10 的清單中：zip 能讀，但沒有 manifest.json（HREP 待決問題 R8）。
const msgNoManifest = "這不是 Notesteme bundle（沒有 manifest.json）。"

func errNotAllowed(name string) *Error {
	s := ShowEntry(name)
	return invalid(s, "bundle 中的項目「%s」不是允許的路徑（只能是 manifest.json 或 artifacts/<檔名>），已拒絕匯入。", s)
}

// entry 是一個已檢查過名稱與種類的項目。
type entry struct {
	f    *zip.File
	name string
	dir  bool
}

// Read 驗證 data（整個 zip 檔）並回傳 Bundle；不通過時回傳 *Error。
// 檢查順序：BND-R4（逐項目，再檢查名稱重複）→ 項目數 → 逐檔解壓（單檔、合計、壓縮比、實際大小）→ BND-R6 → BND-R7 的一致性。
func Read(data []byte, o Options) (*Bundle, error) {
	lim := o.Limits.withDefaults()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	// 不安全的名稱時 archive/zip 依 GODEBUG 可能回 ErrInsecurePath 但 reader 可用：交給自己的 BND-R4 檢查，訊息才正確。
	if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && zr != nil) {
		return nil, invalid("", msgNotZip)
	}
	entries, e := checkEntries(zr, data)
	if e != nil {
		return nil, e
	}
	if len(entries) > lim.MaxFiles {
		return nil, invalid("", "bundle 的項目超過 %d 個的上限，已拒絕匯入。", lim.MaxFiles)
	}
	b := &Bundle{files: map[string][]byte{}, limits: lim}
	var total, packed int64
	for _, en := range entries {
		if en.dir {
			continue
		}
		limit := lim.MaxFileBytes
		if en.name == ManifestName {
			limit = min(limit, ManifestMaxBytes)
		}
		content, e := inflate(en, limit, &total, lim)
		if e != nil {
			return nil, e
		}
		// 合計的壓縮比：單檔 1 MiB 以下不檢查壓縮比，許多高度壓縮的小檔加起來仍可能是炸彈（BND 待決問題 N25）。
		// 每個檔案解壓完才檢查，單檔的壓縮比錯誤先回報。
		// header 宣告的壓縮大小不可信：合計不會超過整個 zip 的大小。
		packed = min(packed+int64(min(en.f.CompressedSize64, uint64(len(data)))), int64(len(data)))
		if total > ratioThreshold && total > int64(lim.MaxRatio)*max(packed, 1) {
			s := ShowEntry(en.name)
			return nil, invalid(s, "bundle 解壓後合計的壓縮比超過 %d 倍的上限，已拒絕匯入。", lim.MaxRatio)
		}
		if en.name == ManifestName {
			b.ManifestJSON = content
			continue
		}
		b.files[en.name] = content
		b.order = append(b.order, en.name)
	}
	if b.ManifestJSON == nil {
		return nil, invalid("", msgNoManifest)
	}
	m, e := parseManifest(b.ManifestJSON, o)
	if e != nil {
		return nil, e
	}
	b.Manifest = m
	if e := checkFiles(b); e != nil {
		return nil, e
	}
	return b, nil
}

// checkEntries 依 BND-R10 的順序逐項目檢查名稱與種類，最後檢查名稱重複。
func checkEntries(zr *zip.Reader, data []byte) ([]entry, *Error) {
	out := make([]entry, 0, len(zr.File))
	offsets := map[int64]string{}
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || driveLetter.MatchString(name) {
			s := ShowEntry(name)
			return nil, invalid(s, "bundle 中的項目「%s」使用絕對路徑，已拒絕匯入。", s)
		}
		segs := strings.Split(name, "/")
		for _, seg := range segs {
			if seg == ".." {
				s := ShowEntry(name)
				return nil, invalid(s, "bundle 中的項目「%s」含有「..」，已拒絕匯入。", s)
			}
		}
		dir, ok := allowedName(name)
		if !ok {
			return nil, errNotAllowed(name)
		}
		off, err := f.DataOffset()
		if err != nil {
			return nil, invalid(ShowEntry(name), msgNotZip)
		}
		if other, dup := offsets[off]; dup {
			s, o := ShowEntry(name), ShowEntry(other)
			return nil, invalid(s, "bundle 中的項目「%s」與「%s」共用同一份資料，已拒絕匯入。", s, o)
		}
		offsets[off] = name
		if local, ok := localName(data, off, len(name)); !ok || local != name {
			return nil, errNotAllowed(name)
		}
		if e := checkMode(f, name, dir); e != nil {
			return nil, e
		}
		if f.Flags&0x1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			s := ShowEntry(name)
			return nil, invalid(s, "bundle 中的項目「%s」使用不支援的壓縮方式或加密，已拒絕匯入。", s)
		}
		out = append(out, entry{f: f, name: name, dir: dir})
	}
	seen := map[string]bool{}
	for _, en := range out {
		// 允許的名稱只有 ASCII，所以不分大小寫的比較就涵蓋了 NFC 正規化。
		k := strings.ToLower(en.name)
		if seen[k] {
			s := ShowEntry(en.name)
			return nil, invalid(s, "bundle 中有重複的項目「%s」，已拒絕匯入。", s)
		}
		seen[k] = true
	}
	return out, nil
}

// allowedName 檢查 BND-R4 的其他名稱條件；dir 表示這是 artifacts/ 目錄項目本身。
func allowedName(name string) (dir, ok bool) {
	if name == "" || len(name) > maxNameBytes || !utf8.ValidString(name) || strings.ContainsRune(name, '\\') {
		return false, false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false, false
		}
	}
	if name == ManifestName {
		return false, true
	}
	if name == ArtifactsDir {
		return true, true
	}
	rest, found := strings.CutPrefix(name, ArtifactsDir)
	if !found || !artifactName.MatchString(rest) || rest == "." {
		return false, false
	}
	return false, true
}

// localName 從資料區的起點往回找出這個項目的 local file header，回傳其中的名稱。
// archive/zip 不公開 local header 的位置，只能從 DataOffset 反推：header 的 30 bytes 加上名稱與 extra 剛好接到資料區。
func localName(data []byte, dataOff int64, wantLen int) (string, bool) {
	sig := []byte("PK\x03\x04")
	lo := max(int64(0), dataOff-30-int64(wantLen)-0xFFFF)
	for p := dataOff - 30 - int64(wantLen); p >= lo; p-- {
		if p < 0 || p+30 > int64(len(data)) {
			continue
		}
		if !bytes.Equal(data[p:p+4], sig) {
			continue
		}
		n := int64(binary.LittleEndian.Uint16(data[p+26:]))
		m := int64(binary.LittleEndian.Uint16(data[p+28:]))
		if p+30+n+m != dataOff || p+30+n > int64(len(data)) {
			continue
		}
		return string(data[p+30 : p+30+n]), true
	}
	return "", false
}

// checkMode 只接受一般檔案（與 artifacts/ 目錄項目）：symlink、裝置檔、FIFO、socket 一律拒絕（BND-R4、N3）。
func checkMode(f *zip.File, name string, dir bool) *Error {
	m := f.Mode()
	kind := ""
	switch {
	case m&fs.ModeSymlink != 0:
		kind = "symlink"
	case m&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		kind = "裝置檔"
	case m&fs.ModeNamedPipe != 0:
		kind = "FIFO"
	case m&fs.ModeSocket != 0:
		kind = "socket"
	}
	if kind != "" {
		s := ShowEntry(name)
		return invalid(s, "bundle 中的項目「%s」不是一般檔案（%s），已拒絕匯入。", s, kind)
	}
	if m.IsDir() != dir || m&fs.ModeIrregular != 0 {
		return errNotAllowed(name)
	}
	return nil
}

// inflate 以 OpenRaw 加上自己的 reader 解壓並計數：header 宣告的大小不可信，讀到上限加 1 byte 就停止（BND-R5）。
func inflate(en entry, limit int64, total *int64, lim Limits) ([]byte, *Error) {
	f := en.f
	s := ShowEntry(en.name)
	tooBig := func() *Error {
		return invalid(s, "bundle 中的檔案「%s」解壓後超過 %s 的上限，已拒絕匯入。", s, FormatSize(limit))
	}
	tooMuch := func() *Error {
		return invalid(s, "bundle 解壓後合計超過 %s 的上限，已拒絕匯入。", FormatSize(lim.MaxTotalBytes))
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, tooBig()
	}
	if uint64(*total)+f.UncompressedSize64 > uint64(lim.MaxTotalBytes) {
		return nil, tooMuch()
	}
	raw, err := f.OpenRaw()
	if err != nil {
		return nil, invalid(s, msgNotZip)
	}
	var r io.Reader = raw
	if f.Method == zip.Deflate {
		fr := flate.NewReader(raw)
		defer fr.Close()
		r = fr
	}
	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	var n int64
	for {
		k, err := r.Read(chunk)
		if k > 0 {
			n += int64(k)
			if n > limit {
				return nil, tooBig()
			}
			if *total+n > lim.MaxTotalBytes {
				return nil, tooMuch()
			}
			if n > ratioThreshold && n > int64(lim.MaxRatio)*max(int64(f.CompressedSize64), 1) {
				return nil, invalid(s, "bundle 中的檔案「%s」壓縮比超過 %d 倍的上限，已拒絕匯入。", s, lim.MaxRatio)
			}
			buf.Write(chunk[:k])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalid(s, msgNotZip)
		}
	}
	if uint64(n) != f.UncompressedSize64 {
		return nil, invalid(s, "bundle 中的檔案「%s」實際大小與宣告的不符，已拒絕匯入。", s)
	}
	*total += n
	return buf.Bytes(), nil
}

// checkFiles 確認 zip 的檔案與 manifest 的 files 雙向一致，sha256 與 size 相符（BND-R7）。
func checkFiles(b *Bundle) *Error {
	listed := map[string]File{}
	for _, f := range b.Manifest.Files {
		listed[f.Path] = f
	}
	for _, p := range b.order {
		if _, ok := listed[p]; !ok {
			s := ShowEntry(p)
			return invalid(s, "bundle 中的檔案「%s」沒有列在 manifest 中，已拒絕匯入。", s)
		}
	}
	for _, f := range b.Manifest.Files {
		d, ok := b.files[f.Path]
		if !ok {
			s := ShowEntry(f.Path)
			return invalid(s, "manifest 列出的檔案「%s」不在 bundle 中，已拒絕匯入。", s)
		}
		sum := sha256.Sum256(d)
		if int64(len(d)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
			s := ShowEntry(f.Path)
			return invalid(s, "bundle 中的檔案「%s」與 manifest 記錄的 sha256 不符，已拒絕匯入。", s)
		}
	}
	return nil
}
