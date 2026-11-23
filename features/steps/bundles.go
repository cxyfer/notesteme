package steps

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math/rand/v2"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cucumber/godog"
)

// registerBundles 註冊 BND 的句型（steps.md 4.20 的「Bundle（BND）」）。
// bundle 一律在執行時產生（archive/zip，必要時改寫 central directory），不 commit 二進位 fixture；
// 每次修改都重新寫到磁碟（路徑套用 /tmp/<x> → <場景根目錄>/tmp/<x>）。
func registerBundles(r *Registry, s *Scenario) {
	r.Step(`the bundle {string} contains the item {string} titled {string}`, s.bundleContainsItem)
	r.Step(`the bundle {string} also has the entries:`, s.bundleAlsoHas)
	r.Step(`the bundle {string} has the manifest field {string} set to:`, s.bundleManifestField)
	r.Step(`the bundle {string} has the content of {string} changed`, s.bundleContentChanged)
	r.Step(`the bundle {string} has the file {string} replaced with:`, s.bundleFileReplaced)
	r.Step(`the bundle {string} is the text:`, s.bundleIsText)
	r.Step(`the bundle instance ID is {string}`, s.bundleInstanceID)
	r.Step(`the bundle {string} is imported`, func(p string) error { return s.bundleImported(p, "", "") })
	r.Step(`the bundle {string} is imported with {string}`, func(p, q string) error { return s.bundleImported(p, q, "") })
	r.Step(`the bundle {string} is imported with the token {string}`, func(p, tok string) error { return s.bundleImported(p, "", tok) })
	r.Step(`the bundle {string} passes its own checks`, s.bundlePassesChecks)
	r.Step(`the bundle {string} lists the artifacts:`, s.bundleListsArtifacts)
	r.Step(`the bundle {string} has the manifest values:`, s.bundleManifestValues)
	r.Step(`the bundle {string} contains no local paths`, s.bundleNoLocalPaths)
	r.Step(`the bundle {string} does not contain {string}`, s.bundleDoesNotContain)
}

// bndState 是場景中以 Given 產生的 bundle（以場景路徑為鍵）與實例 ID。
type bndState struct {
	bundles  map[string]*bndBundle
	instance string
}

// bndEntry 是 manifest 以外的一個 zip 項目。
type bndEntry struct {
	name     string
	data     []byte
	mode     fs.FileMode
	dir      bool
	declared int64 // -1 代表照實宣告
	encrypt  bool
	// hardlink 非空時，這個項目只是一筆指向 hardlink 項目 local header 的 central directory 記錄。
	hardlink string
}

type bndBundle struct {
	manifest map[string]any
	entries  []*bndEntry
	// text 非 nil 時 bundle 是這段文字（不是 zip）。
	text []byte
}

const (
	bndInstance   = "0190a1b2-0000-7000-8000-000000000001"
	bndCorrectID  = "0190a1b2-0000-7000-8000-0000000000a1"
	bndNoteID     = "0190a1b2-0000-7000-8000-0000000000a2"
	bndNoteText   = "# Bundled note\n\nNotes from another instance.\n"
	bndCuesFormat = "notesteme-cues+json"
)

func (s *Scenario) bndBundles() map[string]*bndBundle {
	if s.bnd.bundles == nil {
		s.bnd.bundles = map[string]*bndBundle{}
	}
	return s.bnd.bundles
}

func (s *Scenario) bndBundle(path string) (*bndBundle, error) {
	b, ok := s.bndBundles()[path]
	if !ok || b.text != nil {
		return nil, fmt.Errorf("沒有以 `the bundle %q contains the item …` 產生的 bundle", path)
	}
	return b, nil
}

func sha256Of(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// bundleContainsItem 產生固定內容的合法 bundle v0（steps.md 4.20 的第一列）。
func (s *Scenario) bundleContainsItem(path, ref, title string) error {
	ext, ok := strings.CutPrefix(ref, "yt:")
	if !ok || ext == "" {
		return fmt.Errorf("bundle 的影音只接受 yt: 代號，收到 %q", ref)
	}
	cues := map[string]any{"lang": "en", "variant": "corrected", "speakers": []any{}, "cues": []any{
		map[string]any{"index": 1, "start_ms": 1000, "end_ms": 3000, "text": "Bundled cue one.", "timing": "source", "speaker": nil},
		map[string]any{"index": 2, "start_ms": 3000, "end_ms": 6000, "text": "Bundled cue two.", "timing": "source", "speaker": nil},
	}}
	cuesJSON, err := json.Marshal(cues)
	if err != nil {
		return err
	}
	cuesPath := "artifacts/" + bndCorrectID + ".json"
	notePath := "artifacts/" + bndNoteID + ".md"
	b := &bndBundle{
		entries: []*bndEntry{
			{name: cuesPath, data: cuesJSON, mode: 0o644, declared: -1},
			{name: notePath, data: []byte(bndNoteText), mode: 0o644, declared: -1},
		},
	}
	b.manifest = map[string]any{
		"bundle_version": 0,
		"item": map[string]any{"platform": "youtube", "external_id": ext, "part": nil, "title": title,
			"original_lang": "en", "availability": "public"},
		"artifacts": []any{
			map[string]any{"id": bndCorrectID, "kind": "transcript", "variant": "corrected", "lang": "en",
				"format": bndCuesFormat, "path": cuesPath, "parents": []any{},
				"provenance": map[string]any{"executor": "api", "provider": "anthropic", "model": "fixture-model"}},
			map[string]any{"id": bndNoteID, "kind": "note", "lang": "en", "format": "text/markdown", "path": notePath,
				"parents": []any{bndCorrectID}, "provenance": map[string]any{"executor": "api", "provider": "anthropic", "model": "fixture-model"}},
		},
		"files": []any{
			map[string]any{"path": cuesPath, "sha256": sha256Of(cuesJSON), "size": len(cuesJSON)},
			map[string]any{"path": notePath, "sha256": sha256Of([]byte(bndNoteText)), "size": len(bndNoteText)},
		},
		"created_by": map[string]any{"instance_id": bndInstance, "app_version": "0.4.0"},
		"created_at": "2026-11-23T10:00:00Z",
		"license":    nil,
	}
	s.bndBundles()[path] = b
	return s.writeBundle(path)
}

// parseBndSize 解析 16B、300KiB、2MiB 這類大小。
func parseBndSize(v string) (int64, error) {
	v = strings.TrimSpace(v)
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"B", 1}} {
		if n, ok := strings.CutSuffix(v, u.suf); ok {
			v, mult = n, u.m
			break
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("大小 %q 不正確（例如 16B、300KiB、2MiB）", v)
	}
	return n * mult, nil
}

// bndFill 產生內容：text 是重複的 x、zeros 是全零、random 是以 path 為種子的確定性亂數。
func bndFill(kind, path string, size int64) ([]byte, error) {
	out := make([]byte, size)
	switch kind {
	case "", "text":
		for i := range out {
			out[i] = 'x'
		}
	case "zeros":
	case "random":
		h := sha256.Sum256([]byte(path))
		rng := rand.NewChaCha8(h)
		_, _ = rng.Read(out)
	default:
		return nil, fmt.Errorf("不認得的 fill %q", kind)
	}
	return out, nil
}

// bundleAlsoHas 在 bundle 的最後加上項目（steps.md 4.20）。
func (s *Scenario) bundleAlsoHas(path string, table *godog.Table) error {
	b, err := s.bndBundle(path)
	if err != nil {
		return err
	}
	if table == nil || len(table.Rows) < 2 {
		return errors.New("表格至少要有表頭與一列")
	}
	head := []string{}
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	for _, row := range table.Rows[1:] {
		f := map[string]string{}
		for i, c := range row.Cells {
			f[head[i]] = c.Value
		}
		name := f["path"]
		if name == "" {
			return errors.New("path 是必填的")
		}
		typ := f["type"]
		if typ == "" {
			typ = "file"
		}
		size := int64(16)
		if f["size"] != "" {
			if size, err = parseBndSize(f["size"]); err != nil {
				return err
			}
		}
		data, err := bndFill(f["fill"], name, size)
		if err != nil {
			return err
		}
		en := &bndEntry{name: name, data: data, mode: 0o644, declared: -1}
		switch typ {
		case "file":
		case "dir":
			en.name, en.data, en.dir, en.mode = strings.TrimSuffix(name, "/")+"/", nil, true, fs.ModeDir|0o755
		case "symlink":
			en.data, en.mode = []byte(f["target"]), fs.ModeSymlink|0o777
		case "char-device":
			en.mode = fs.ModeDevice | fs.ModeCharDevice | 0o644
		case "fifo":
			en.mode = fs.ModeNamedPipe | 0o644
		case "socket":
			en.mode = fs.ModeSocket | 0o644
		case "hardlink":
			t := f["target"]
			if t != "manifest.json" && !slices.ContainsFunc(b.entries, func(e *bndEntry) bool { return e.name == t && e.hardlink == "" }) {
				return fmt.Errorf("hardlink 的 target %q 不存在", t)
			}
			en.hardlink, en.data = t, nil
		case "encrypted-file":
			en.encrypt = true
		default:
			return fmt.Errorf("不認得的 type %q", typ)
		}
		if f["declared_size"] != "" {
			if en.declared, err = parseBndSize(f["declared_size"]); err != nil {
				return err
			}
		}
		b.entries = append(b.entries, en)
		listed := f["listed"]
		if listed == "" {
			listed = "yes"
		}
		if listed != "yes" && listed != "no" {
			return fmt.Errorf("listed 只能是 yes 或 no，收到 %q", listed)
		}
		if listed == "yes" && typ == "file" {
			files, _ := b.manifest["files"].([]any)
			b.manifest["files"] = append(files, map[string]any{"path": name, "sha256": sha256Of(data), "size": len(data)})
		}
	}
	return s.writeBundle(path)
}

// setDotPath 把 v 寫到點號路徑；數字段落是陣列索引（等於長度時附加），中間不存在的物件會建立。
func setDotPath(root map[string]any, path string, v any) error {
	segs := strings.Split(path, ".")
	var cur any = root
	set := func(parent any, key string, val any) (any, error) {
		switch p := parent.(type) {
		case map[string]any:
			p[key] = val
			return p, nil
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i > len(p) {
				return nil, fmt.Errorf("%q 不是陣列（%d 項）的索引", key, len(p))
			}
			if i == len(p) {
				return append(p, val), nil
			}
			p[i] = val
			return p, nil
		}
		return nil, fmt.Errorf("%q 的上層不是物件或陣列", key)
	}
	// parents 記錄每一層，附加陣列項目時要寫回上一層。
	type frame struct {
		container any
		key       string
	}
	var stack []frame
	for i, seg := range segs {
		if i == len(segs)-1 {
			nc, err := set(cur, seg, v)
			if err != nil {
				return err
			}
			for j := len(stack) - 1; j >= 0; j-- {
				parent, err := set(stack[j].container, stack[j].key, nc)
				if err != nil {
					return err
				}
				nc = parent
			}
			return nil
		}
		var next any
		switch p := cur.(type) {
		case map[string]any:
			next = p[seg]
			if next == nil {
				next = map[string]any{}
			}
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(p) {
				return fmt.Errorf("%q 不是陣列（%d 項）的索引", seg, len(p))
			}
			next = p[idx]
		default:
			return fmt.Errorf("%q 的上層不是物件或陣列", seg)
		}
		stack = append(stack, frame{cur, seg})
		cur = next
	}
	return nil
}

func (s *Scenario) bundleManifestField(path, field string, doc *godog.DocString) error {
	b, err := s.bndBundle(path)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal([]byte(doc.Content), &v); err != nil {
		return fmt.Errorf("doc string 不是合法的 JSON：%w", err)
	}
	if err := setDotPath(b.manifest, field, v); err != nil {
		return fmt.Errorf("manifest 的 %s：%w", field, err)
	}
	return s.writeBundle(path)
}

func (b *bndBundle) entry(name string) *bndEntry {
	for _, e := range b.entries {
		if e.name == name && e.hardlink == "" {
			return e
		}
	}
	return nil
}

func (s *Scenario) bundleContentChanged(path, name string) error {
	b, err := s.bndBundle(path)
	if err != nil {
		return err
	}
	e := b.entry(name)
	if e == nil || len(e.data) == 0 {
		return fmt.Errorf("bundle 沒有項目 %q（或是空的）", name)
	}
	e.data = slices.Clone(e.data)
	e.data[len(e.data)-1]++
	return s.writeBundle(path)
}

func (s *Scenario) bundleFileReplaced(path, name string, doc *godog.DocString) error {
	b, err := s.bndBundle(path)
	if err != nil {
		return err
	}
	e := b.entry(name)
	if e == nil {
		return fmt.Errorf("bundle 沒有項目 %q", name)
	}
	e.data = []byte(doc.Content)
	files, _ := b.manifest["files"].([]any)
	for _, f := range files {
		if m, ok := f.(map[string]any); ok && m["path"] == name {
			m["sha256"], m["size"] = sha256Of(e.data), len(e.data)
		}
	}
	return s.writeBundle(path)
}

func (s *Scenario) bundleIsText(path string, doc *godog.DocString) error {
	s.bndBundles()[path] = &bndBundle{text: []byte(doc.Content)}
	return s.writeBundle(path)
}

// bundleInstanceID 記下實例 ID；server 已在執行時立刻寫入，否則在 server 啟動時寫入（applyBundleInstance）。
func (s *Scenario) bundleInstanceID(id string) error {
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(id) {
		return fmt.Errorf("instance ID %q 不是小寫的 UUID", id)
	}
	s.bnd.instance = id
	if s.server != nil {
		return s.applyBundleInstance()
	}
	return nil
}

// applyBundleInstance 在 server 啟動之後寫入場景指定的實例 ID（startServer 呼叫）。
func (s *Scenario) applyBundleInstance() error {
	if s.bnd.instance == "" || s.server == nil {
		return nil
	}
	return s.server.SetInstanceID(s.ctx, s.bnd.instance)
}

// ---- 寫出 zip ----

func (s *Scenario) writeBundle(path string) error {
	b := s.bndBundles()[path]
	data := b.text
	if data == nil {
		var err error
		if data, err = b.zip(); err != nil {
			return err
		}
	}
	real := s.filePath(path)
	if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
		return err
	}
	return os.WriteFile(real, data, 0o600)
}

// zip 依序寫出 manifest.json 與其他項目；hardlink 在寫完之後以改寫 central directory 加上。
func (b *bndBundle) zip() ([]byte, error) {
	man, err := json.MarshalIndent(b.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	all := append([]*bndEntry{{name: "manifest.json", data: man, mode: 0o644, declared: -1}}, b.entries...)
	var links []*bndEntry
	for _, e := range all {
		if e.hardlink != "" {
			links = append(links, e)
			continue
		}
		if err := writeBndEntry(zw, e); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	for _, l := range links {
		if out, err = addHardlink(out, l.name, l.hardlink); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func writeBndEntry(zw *zip.Writer, e *bndEntry) error {
	h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
	h.SetMode(e.mode)
	if e.dir {
		h.Method = zip.Store
	}
	if e.declared < 0 && !e.encrypt {
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(e.data)
		return err
	}
	// 自訂 header：宣告的大小或加密位元，資料自己壓縮後以 CreateRaw 寫入。
	var comp bytes.Buffer
	if e.encrypt {
		h.Method = zip.Store
		h.Flags |= 0x1
		comp.Write(e.data)
	} else {
		fw, err := flate.NewWriter(&comp, flate.DefaultCompression)
		if err != nil {
			return err
		}
		if _, err := fw.Write(e.data); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}
	}
	h.CRC32 = crc32.ChecksumIEEE(e.data)
	h.CompressedSize64 = uint64(comp.Len())
	h.UncompressedSize64 = uint64(len(e.data))
	if e.declared >= 0 {
		h.UncompressedSize64 = uint64(e.declared)
	}
	w, err := zw.CreateRaw(h)
	if err != nil {
		return err
	}
	_, err = w.Write(comp.Bytes())
	return err
}

// addHardlink 在 central directory 的最後加上一筆名稱為 name、local header offset 指向 target 的記錄。
func addHardlink(data []byte, name, target string) ([]byte, error) {
	eocd := len(data) - 22
	if eocd < 0 || binary.LittleEndian.Uint32(data[eocd:]) != 0x06054b50 {
		return nil, errors.New("找不到 end of central directory")
	}
	count := binary.LittleEndian.Uint16(data[eocd+10:])
	cdSize := binary.LittleEndian.Uint32(data[eocd+12:])
	cdOff := binary.LittleEndian.Uint32(data[eocd+16:])
	cd := data[cdOff : cdOff+cdSize]
	var rec []byte
	for p := 0; p+46 <= len(cd); {
		n := int(binary.LittleEndian.Uint16(cd[p+28:]))
		m := int(binary.LittleEndian.Uint16(cd[p+30:]))
		k := int(binary.LittleEndian.Uint16(cd[p+32:]))
		if string(cd[p+46:p+46+n]) == target {
			rec = slices.Clone(cd[p : p+46+n+m+k])
			break
		}
		p += 46 + n + m + k
	}
	if rec == nil {
		return nil, fmt.Errorf("central directory 沒有 %q", target)
	}
	n := int(binary.LittleEndian.Uint16(rec[28:]))
	nr := append(slices.Clone(rec[:46]), name...)
	nr = append(nr, rec[46+n:]...)
	binary.LittleEndian.PutUint16(nr[28:], uint16(len(name)))
	out := slices.Clone(data[:cdOff+cdSize])
	out = append(out, nr...)
	tail := slices.Clone(data[eocd:])
	binary.LittleEndian.PutUint16(tail[8:], count+1)
	binary.LittleEndian.PutUint16(tail[10:], count+1)
	binary.LittleEndian.PutUint32(tail[12:], cdSize+uint32(len(nr)))
	return append(out, tail...), nil
}

// ---- 匯入 ----

func (s *Scenario) bundleImported(path, query, token string) error {
	data, err := os.ReadFile(s.filePath(path))
	if err != nil {
		return fmt.Errorf("讀取 bundle %s：%w", path, err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	spec := "POST /api/bundles/import"
	if query != "" {
		spec += "?" + query
	}
	o := requestOpts{who: asOwner, body: body.Bytes(), contentType: mw.FormDataContentType()}
	if token != "" {
		h, err := s.bearerHeader(token)
		if err != nil {
			return err
		}
		o = requestOpts{who: asClient, body: body.Bytes(), contentType: mw.FormDataContentType(), headers: h}
	}
	return s.send(spec, o)
}

// ---- 讀取匯出的 bundle（獨立的驗證，不呼叫產品的 internal/bundle）----

type bndRead struct {
	manifest map[string]any
	raw      []byte
	files    map[string][]byte // 解壓後的內容（含 manifest.json）
	names    []string
}

func (s *Scenario) readBundleFile(path string) (*bndRead, error) {
	data, err := os.ReadFile(s.filePath(path))
	if err != nil {
		return nil, fmt.Errorf("讀取 bundle %s：%w", path, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s 不是 zip：%w", path, err)
	}
	r := &bndRead{raw: data, files: map[string][]byte{}}
	for _, f := range zr.File {
		r.names = append(r.names, f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s 的 %s：%w", path, f.Name, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, 512<<20))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s 的 %s：%w", path, f.Name, err)
		}
		r.files[f.Name] = b
	}
	man, ok := r.files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("%s 沒有 manifest.json", path)
	}
	dec := json.NewDecoder(bytes.NewReader(man))
	dec.UseNumber()
	if err := dec.Decode(&r.manifest); err != nil {
		return nil, fmt.Errorf("%s 的 manifest.json 不是 JSON 物件：%w", path, err)
	}
	return r, nil
}

var (
	bndEntryName = regexp.MustCompile(`^artifacts/[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	bndUUID      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	bndSHA       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// bundlePassesChecks 以 BND-R4～R7 的規則驗證匯出的 bundle（獨立實作）。
func (s *Scenario) bundlePassesChecks(path string) error {
	data, err := os.ReadFile(s.filePath(path))
	if err != nil {
		return fmt.Errorf("讀取 bundle %s：%w", path, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%s 不是 zip：%w", path, err)
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		if f.Name != "manifest.json" && !bndEntryName.MatchString(f.Name) {
			return fmt.Errorf("項目名稱 %q 不合法", f.Name)
		}
		if seen[strings.ToLower(f.Name)] {
			return fmt.Errorf("項目 %q 重複", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		if !f.Mode().IsRegular() {
			return fmt.Errorf("項目 %q 不是一般檔案（mode %v）", f.Name, f.Mode())
		}
		if f.Flags&0x1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return fmt.Errorf("項目 %q 加密或使用不支援的壓縮方式", f.Name)
		}
	}
	r, err := s.readBundleFile(path)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if uint64(len(r.files[f.Name])) != f.UncompressedSize64 {
			return fmt.Errorf("項目 %q 的實際大小與宣告的不符", f.Name)
		}
	}
	m := r.manifest
	for k := range m {
		if !slices.Contains([]string{"bundle_version", "item", "artifacts", "files", "created_by", "created_at", "license"}, k) {
			return fmt.Errorf("manifest 有未定義的欄位 %q", k)
		}
	}
	if v, _ := m["bundle_version"].(json.Number); v.String() != "0" {
		return fmt.Errorf("bundle_version 應是 0，實際是 %v", m["bundle_version"])
	}
	item, _ := m["item"].(map[string]any)
	if item == nil || !slices.Contains([]string{"youtube", "bilibili", "podcast", "local"}, fmt.Sprint(item["platform"])) {
		return fmt.Errorf("item 不正確：%v", m["item"])
	}
	if t, _ := item["title"].(string); utf8.RuneCountInString(t) > 500 {
		return errors.New("item.title 超過 500 字元")
	}
	by, _ := m["created_by"].(map[string]any)
	if id, _ := by["instance_id"].(string); !bndUUID.MatchString(id) {
		return fmt.Errorf("created_by.instance_id %q 不是 UUID", by["instance_id"])
	}
	files, _ := m["files"].([]any)
	listed := map[string]bool{}
	for i, x := range files {
		f, _ := x.(map[string]any)
		p, _ := f["path"].(string)
		sum, _ := f["sha256"].(string)
		size, _ := f["size"].(json.Number)
		if !bndEntryName.MatchString(p) || !bndSHA.MatchString(sum) {
			return fmt.Errorf("files.%d 不正確：%v", i, x)
		}
		content, ok := r.files[p]
		if !ok {
			return fmt.Errorf("files 列出的 %s 不在 zip 中", p)
		}
		if sha256Of(content) != sum || size.String() != strconv.Itoa(len(content)) {
			return fmt.Errorf("%s 的 sha256 或 size 與 manifest 不符", p)
		}
		listed[p] = true
	}
	for name := range r.files {
		if name != "manifest.json" && !listed[name] {
			return fmt.Errorf("zip 中的 %s 沒有列在 files", name)
		}
	}
	arts, _ := m["artifacts"].([]any)
	for i, x := range arts {
		a, _ := x.(map[string]any)
		id, _ := a["id"].(string)
		kind, _ := a["kind"].(string)
		p, _ := a["path"].(string)
		if !bndUUID.MatchString(id) || !slices.Contains([]string{"transcript", "note", "chapters", "sponsor_segments", "frames"}, kind) || !listed[p] {
			return fmt.Errorf("artifacts.%d 不正確：%v", i, x)
		}
		if _, ok := a["provenance"].(map[string]any); !ok {
			return fmt.Errorf("artifacts.%d 沒有 provenance 物件", i)
		}
		if _, has := a["variant"]; has != (kind == "transcript") {
			return fmt.Errorf("artifacts.%d 的 variant 只有 transcript 有", i)
		}
		if kind == "transcript" {
			var tr struct {
				Cues []json.RawMessage `json:"cues"`
			}
			if err := json.Unmarshal(r.files[p], &tr); err != nil {
				return fmt.Errorf("artifacts.%d 的字幕不是 cues JSON：%w", i, err)
			}
		}
	}
	return nil
}

func (s *Scenario) bundleListsArtifacts(path string, table *godog.Table) error {
	r, err := s.readBundleFile(path)
	if err != nil {
		return err
	}
	arts, _ := r.manifest["artifacts"].([]any)
	if table == nil || len(table.Rows) < 2 {
		return errors.New("表格至少要有表頭與一列")
	}
	for _, row := range table.Rows[1:] {
		kind, variant, lang := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		found := false
		for _, x := range arts {
			a, _ := x.(map[string]any)
			v, hasV := a["variant"].(string)
			l, _ := a["lang"].(string)
			if a["kind"] != kind {
				continue
			}
			if variant == "" && hasV {
				continue
			}
			if variant != "" && variant != "*" && v != variant {
				continue
			}
			if lang != "*" && l != lang {
				continue
			}
			found = true
		}
		if !found {
			return fmt.Errorf("bundle 沒有產出 %s/%s/%s：%s", kind, variant, lang, snippet(r.files["manifest.json"]))
		}
	}
	return nil
}

func (s *Scenario) bundleManifestValues(path string, table *godog.Table) error {
	r, err := s.readBundleFile(path)
	if err != nil {
		return err
	}
	for _, row := range table.Rows[1:] {
		field, want := row.Cells[0].Value, row.Cells[1].Value
		var cur any = r.manifest
		for _, seg := range strings.Split(field, ".") {
			switch c := cur.(type) {
			case map[string]any:
				v, ok := c[seg]
				if !ok {
					return fmt.Errorf("manifest 沒有 %s", field)
				}
				cur = v
			case []any:
				i, err := strconv.Atoi(seg)
				if err != nil || i < 0 || i >= len(c) {
					return fmt.Errorf("manifest 的 %s：%q 不是索引", field, seg)
				}
				cur = c[i]
			default:
				return fmt.Errorf("manifest 沒有 %s", field)
			}
		}
		got := ""
		switch v := cur.(type) {
		case nil:
			got = "null"
		case string:
			got = v
		default:
			b, _ := json.Marshal(v)
			got = string(b)
		}
		if got != want {
			return fmt.Errorf("manifest 的 %s 是 %q，預期 %q", field, got, want)
		}
	}
	return nil
}

// bundleNeedles 是不能出現在匯出的 bundle 中的本機路徑（steps.md 4.20）。
func (s *Scenario) bundleNeedles() []string {
	out := []string{s.World.Root, "/data/", "/config/", "/media/", "/vault/", s.World.InboxDir()}
	if real, err := filepath.EvalSymlinks(s.World.Root); err == nil && real != s.World.Root {
		out = append(out, real)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		out = append(out, home)
	}
	return out
}

func (s *Scenario) bundleNoLocalPaths(path string) error {
	r, err := s.readBundleFile(path)
	if err != nil {
		return err
	}
	var bad []string
	for _, name := range r.names {
		for _, n := range s.bundleNeedles() {
			if strings.Contains(name, n) || bytes.Contains(r.files[name], []byte(n)) {
				bad = append(bad, fmt.Sprintf("%s（%s）", name, n))
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("bundle 含有本機路徑：%s", strings.Join(bad, "、"))
	}
	return nil
}

func (s *Scenario) bundleDoesNotContain(path, text string) error {
	r, err := s.readBundleFile(path)
	if err != nil {
		return err
	}
	if len(r.files) == 0 {
		return errors.New("bundle 沒有任何檔案項目，無法驗證")
	}
	for _, name := range r.names {
		if strings.Contains(name, text) || bytes.Contains(r.files[name], []byte(text)) {
			return fmt.Errorf("bundle 的 %s 含有 %q", name, text)
		}
	}
	if bytes.Contains(r.raw, []byte(text)) {
		return fmt.Errorf("bundle 的原始 bytes 含有 %q", text)
	}
	return nil
}
