package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io/fs"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

const artID = "0193a3b4-0000-7000-8000-0000000000a1"

type entrySpec struct {
	name   string
	data   []byte
	mode   fs.FileMode
	method uint16
}

func shaOf(d []byte) string {
	s := sha256.Sum256(d)
	return hex.EncodeToString(s[:])
}

func cues(t testing.TB) []byte {
	var b bytes.Buffer
	err := subtitle.RenderCuesJSON(&b, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource,
		Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "hi", Timing: subtitle.TimingSource}}})
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// manifest 回傳一個有效的 manifest（以 map 表示，方便測試改動）。
func manifest(files map[string][]byte, artifacts ...map[string]any) map[string]any {
	list := []any{}
	for p, d := range files {
		list = append(list, map[string]any{"path": p, "sha256": shaOf(d), "size": len(d)})
	}
	arts := []any{}
	for _, a := range artifacts {
		arts = append(arts, a)
	}
	return map[string]any{
		"bundle_version": 0,
		"item": map[string]any{"platform": "youtube", "external_id": "abc", "part": nil, "title": "T",
			"channel": map[string]any{"external_id": "UC1", "title": "C"}, "availability": "public", "original_lang": "en"},
		"artifacts":  arts,
		"files":      list,
		"created_by": map[string]any{"instance_id": "0193a3b4-0000-7000-8000-000000000001", "app_version": "0.4.0"},
		"created_at": "2026-11-23T10:00:00Z",
		"license":    "CC-BY-4.0",
	}
}

func transcriptArtifact() map[string]any {
	return map[string]any{"id": artID, "kind": "transcript", "variant": "source", "lang": "en",
		"format": CuesFormat, "path": "artifacts/" + artID + ".json", "parents": []any{}, "provenance": map[string]any{"executor": "api"}}
}

func build(t testing.TB, entries ...entrySpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mj(t testing.TB, m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// valid 回傳一個有效的 bundle 與它的 entries。
func valid(t testing.TB) []entrySpec {
	c := cues(t)
	path := "artifacts/" + artID + ".json"
	m := manifest(map[string][]byte{path: c}, transcriptArtifact())
	return []entrySpec{{name: ManifestName, data: mj(t, m), method: zip.Deflate}, {name: "artifacts/", mode: fs.ModeDir | 0o755}, {name: path, data: c, method: zip.Deflate}}
}

func mustErr(t *testing.T, data []byte, o Options, want string) *Error {
	t.Helper()
	_, err := Read(data, o)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("Read 回 %v，預期 *Error（%s）", err, want)
	}
	if !strings.Contains(e.Message, want) {
		t.Fatalf("訊息 = %q，預期含 %q", e.Message, want)
	}
	return e
}

func TestReadValidBundle(t *testing.T) {
	b, err := Read(build(t, valid(t)...), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Item.ExternalID != "abc" || len(b.Manifest.Artifacts) != 1 || b.Manifest.Artifacts[0].Variant != "source" {
		t.Errorf("manifest = %+v", b.Manifest)
	}
	if got := b.Paths(); len(got) != 1 || got[0] != "artifacts/"+artID+".json" {
		t.Errorf("Paths = %v", got)
	}
	if _, ok := b.File(b.Paths()[0]); !ok {
		t.Error("File 找不到產出")
	}
	if b.Manifest.CreatedBy.InstanceID == "" || b.Manifest.License != "CC-BY-4.0" || b.Manifest.Item.Channel.ExternalID != "UC1" {
		t.Errorf("manifest = %+v", b.Manifest)
	}
}

func TestEntryNames(t *testing.T) {
	base := valid(t)
	cases := []struct {
		name string
		want string
	}{
		{"/etc/notesteme", "使用絕對路徑"},
		{"C:/x", "使用絕對路徑"},
		{"artifacts/../x", "含有「..」"},
		{"../manifest.json", "含有「..」"},
		{"artifacts/./x", "不是允許的路徑"},
		{"artifacts\\x", "不是允許的路徑"},
		{"artifacts/sub/x", "不是允許的路徑"},
		{"other.json", "不是允許的路徑"},
		{"artifacts/.hidden", "不是允許的路徑"},
		{"artifacts/a\x01b", "不是允許的路徑"},
		{"artifacts/" + strings.Repeat("a", 129), "不是允許的路徑"},
		{"artifacts/é", "不是允許的路徑"},
		{"x/", "不是允許的路徑"},
	}
	for _, c := range cases {
		var data []byte
		if !strings.HasSuffix(c.name, "/") {
			data = []byte("x")
		}
		e := mustErr(t, build(t, append(base, entrySpec{name: c.name, data: data})...), Options{}, c.want)
		if e.Code != CodeInvalid || e.Entry == "" {
			t.Errorf("%q：%+v", c.name, e)
		}
		if strings.ContainsRune(e.Message, '\x01') {
			t.Errorf("訊息含控制字元：%q", e.Message)
		}
	}
}

func TestEntryModes(t *testing.T) {
	base := valid(t)
	for mode, want := range map[fs.FileMode]string{
		fs.ModeSymlink | 0o777:                    "（symlink）",
		fs.ModeDevice | 0o600:                     "（裝置檔）",
		fs.ModeDevice | fs.ModeCharDevice | 0o600: "（裝置檔）",
		fs.ModeNamedPipe | 0o600:                  "（FIFO）",
		fs.ModeSocket | 0o600:                     "（socket）",
	} {
		mustErr(t, build(t, append(base, entrySpec{name: "artifacts/link", data: []byte("/etc/passwd"), mode: mode})...), Options{}, want)
	}
	// 名稱不是目錄卻是目錄項目、manifest.json 是目錄。
	mustErr(t, build(t, append(base, entrySpec{name: "artifacts/d", mode: fs.ModeDir | 0o755})...), Options{}, "不是允許的路徑")
}

func TestDuplicateNames(t *testing.T) {
	base := valid(t)
	mustErr(t, build(t, append(base, entrySpec{name: "ARTIFACTS/" + artID + ".json", data: []byte("x")})...), Options{}, "不是允許的路徑")
	dup := "artifacts/" + strings.ToUpper(artID) + ".json"
	mustErr(t, build(t, append(base, entrySpec{name: dup, data: []byte("x")})...), Options{}, "重複的項目")
}

// centralDir 回傳每個 central directory 項目的位置。
func centralDir(data []byte) []int {
	var out []int
	for i := 0; i+4 <= len(data); i++ {
		if string(data[i:i+4]) == "PK\x01\x02" {
			out = append(out, i)
		}
	}
	return out
}

func TestSharedLocalHeader(t *testing.T) {
	data := build(t, append(valid(t), entrySpec{name: "artifacts/x.jpg", data: []byte("xx")})...)
	cd := centralDir(data)
	// 最後一個項目的 local header offset 改成指到前一個項目。
	prev := binary.LittleEndian.Uint32(data[cd[len(cd)-2]+42:])
	binary.LittleEndian.PutUint32(data[cd[len(cd)-1]+42:], prev)
	mustErr(t, data, Options{}, "共用同一份資料")
}

func TestLocalHeaderNameMismatch(t *testing.T) {
	data := build(t, valid(t)...)
	i := bytes.Index(data, []byte("PK\x03\x04"))
	j := bytes.Index(data[i+1:], []byte("PK\x03\x04")) + i + 1 // 第二個項目（artifacts/）
	k := bytes.Index(data[j+1:], []byte("PK\x03\x04")) + j + 1 // 第三個項目
	copy(data[k+30:], []byte("artifacts/X"))
	mustErr(t, data, Options{}, "不是允許的路徑")
}

func TestEncryptedAndMethods(t *testing.T) {
	data := build(t, valid(t)...)
	cd := centralDir(data)
	enc := bytes.Clone(data)
	binary.LittleEndian.PutUint16(enc[cd[2]+8:], binary.LittleEndian.Uint16(enc[cd[2]+8:])|1)
	mustErr(t, enc, Options{}, "不支援的壓縮方式或加密")
	bz := bytes.Clone(data)
	binary.LittleEndian.PutUint16(bz[cd[2]+10:], 12) // bzip2
	mustErr(t, bz, Options{}, "不支援的壓縮方式或加密")
}

func TestEntryCount(t *testing.T) {
	data := build(t, valid(t)...)
	mustErr(t, data, Options{Limits: Limits{MaxFiles: 2}}, "超過 2 個的上限")
	if _, err := Read(data, Options{Limits: Limits{MaxFiles: 3}}); err != nil {
		t.Fatalf("剛好上限：%v", err)
	}
}

func TestSizeLimits(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 3<<20)
	base := valid(t)
	// 單檔上限（宣告的大小已經超過）。
	mustErr(t, build(t, append(base, entrySpec{name: "artifacts/big.jpg", data: big, method: zip.Deflate})...),
		Options{Limits: Limits{MaxFileBytes: 1 << 20}}, "解壓後超過 1 MiB 的上限")
	// 合計上限。
	mustErr(t, build(t, append(base, entrySpec{name: "artifacts/big.jpg", data: big[:1<<19]})...),
		Options{Limits: Limits{MaxTotalBytes: 1 << 19}}, "合計超過 512 KiB 的上限")
	// 壓縮比（zip bomb）：3 MiB 的 a 壓縮後只有幾 KiB。
	e := mustErr(t, build(t, append(base, entrySpec{name: "artifacts/bomb.jpg", data: big, method: zip.Deflate})...), Options{}, "壓縮比超過 100 倍的上限")
	if e.Entry != "artifacts/bomb.jpg" {
		t.Errorf("Entry = %q", e.Entry)
	}
	// manifest.json 固定 4 MiB。
	m := manifest(nil)
	m["item"].(map[string]any)["title"] = strings.Repeat("x", 5<<20)
	mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}), Options{}, "「manifest.json」解壓後超過 4 MiB")
}

// 宣告的大小不可信：central directory 說比實際小（或大）時都拒絕；宣告的小值不能繞過上限。
func TestDeclaredSizeLies(t *testing.T) {
	// 不容易壓縮的內容（壓縮比不會先觸發）。
	big := make([]byte, 0, 2<<20)
	for seed := sha256.Sum256([]byte("seed")); len(big) < 2<<20; seed = sha256.Sum256(seed[:]) {
		big = append(big, seed[:]...)
	}
	for _, declared := range []uint32{10, uint32(len(big) + 5)} {
		data := build(t, append(valid(t), entrySpec{name: "artifacts/x.jpg", data: big, method: zip.Deflate})...)
		cd := centralDir(data)
		binary.LittleEndian.PutUint32(data[cd[len(cd)-1]+24:], declared)
		mustErr(t, data, Options{}, "實際大小與宣告的不符")
	}
	// 宣告很小，實際超過單檔上限：讀到上限加 1 byte 就停止。
	data := build(t, append(valid(t), entrySpec{name: "artifacts/x.jpg", data: big, method: zip.Deflate})...)
	cd := centralDir(data)
	binary.LittleEndian.PutUint32(data[cd[len(cd)-1]+24:], 10)
	mustErr(t, data, Options{Limits: Limits{MaxFileBytes: 1 << 20}}, "解壓後超過 1 MiB 的上限")
}

func TestNotZip(t *testing.T) {
	mustErr(t, []byte("not a zip"), Options{}, "無法以 zip 讀取")
	mustErr(t, build(t, entrySpec{name: "artifacts/x.json", data: []byte("{}")}), Options{}, "沒有 manifest.json")
	// 壞掉的 deflate 資料。
	data := build(t, valid(t)...)
	cd := centralDir(data)
	off := binary.LittleEndian.Uint32(data[cd[2]+42:])
	nlen := binary.LittleEndian.Uint16(data[off+26:])
	start := int(off) + 30 + int(nlen)
	for i := start; i < start+8 && i < cd[0]; i++ {
		data[i] = 0xff
	}
	if _, err := Read(data, Options{}); err == nil {
		t.Fatal("壞掉的 deflate 資料要被拒絕")
	}
}

func manifestErr(t *testing.T, mutate func(m map[string]any), o Options, wantPath, wantMsg string) {
	t.Helper()
	c := cues(t)
	path := "artifacts/" + artID + ".json"
	m := manifest(map[string][]byte{path: c}, transcriptArtifact())
	mutate(m)
	e := mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: path, data: c}), o, wantMsg)
	if e.Path != wantPath {
		t.Errorf("Path = %q，預期 %q（%s）", e.Path, wantPath, e.Message)
	}
}

func art(m map[string]any) map[string]any { return m["artifacts"].([]any)[0].(map[string]any) }

func TestManifestSchema(t *testing.T) {
	o := Options{}
	manifestErr(t, func(m map[string]any) { m["extra"] = 1 }, o, "extra", "不允許的欄位")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["cookies"] = "x" }, o, "item.cookies", "不允許的欄位")
	manifestErr(t, func(m map[string]any) { art(m)["workdir"] = "/data" }, o, "artifacts.0.workdir", "不允許的欄位")
	manifestErr(t, func(m map[string]any) { delete(m, "files") }, o, "files", "缺少這個欄位")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["platform"] = "vimeo" }, o, "item.platform", "必須是")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["title"] = strings.Repeat("字", 501) }, o, "item.title", "最多 500")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["part"] = 0 }, o, "item.part", "1 以上")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["duration_s"] = -1 }, o, "item.duration_s", "0 以上")
	manifestErr(t, func(m map[string]any) { m["item"].(map[string]any)["original_lang"] = "not a tag" }, o, "item.original_lang", "BCP 47")
	manifestErr(t, func(m map[string]any) { art(m)["id"] = "x" }, o, "artifacts.0.id", "UUID")
	manifestErr(t, func(m map[string]any) { art(m)["kind"] = "audio" }, o, "artifacts.0.kind", "必須是")
	manifestErr(t, func(m map[string]any) { art(m)["variant"] = "final" }, o, "artifacts.0.variant", "必須是")
	manifestErr(t, func(m map[string]any) { delete(art(m), "lang") }, o, "artifacts.0.lang", "字幕必須有語言")
	manifestErr(t, func(m map[string]any) { art(m)["kind"] = "note"; art(m)["variant"] = "source" }, o, "artifacts.0.variant", "只有 transcript")
	manifestErr(t, func(m map[string]any) { art(m)["path"] = "artifacts/missing.json" }, o, "artifacts.0.path", "必須列在 files 中")
	manifestErr(t, func(m map[string]any) { art(m)["parents"] = []any{"nope"} }, o, "artifacts.0.parents.0", "UUID")
	manifestErr(t, func(m map[string]any) { art(m)["provenance"] = "x" }, o, "artifacts.0.provenance", "必須是物件")
	manifestErr(t, func(m map[string]any) {
		m["files"] = append(m["files"].([]any), map[string]any{"path": "../x", "sha256": strings.Repeat("0", 64), "size": 1})
	}, o, "files.1.path", "artifacts/<檔名>")
	manifestErr(t, func(m map[string]any) {
		m["files"].([]any)[0].(map[string]any)["sha256"] = "ABC"
	}, o, "files.0.sha256", "64 個小寫")
	manifestErr(t, func(m map[string]any) { m["files"].([]any)[0].(map[string]any)["size"] = 1.5 }, o, "files.0.size", "整數")
	manifestErr(t, func(m map[string]any) { m["created_at"] = "yesterday" }, o, "created_at", "RFC 3339")
	// instance_id：BND 必填，Hub（InstanceIDOptional）可以省略，但有的話要是 UUID。
	manifestErr(t, func(m map[string]any) { delete(m["created_by"].(map[string]any), "instance_id") }, o, "created_by.instance_id", "缺少")
	manifestErr(t, func(m map[string]any) { m["created_by"].(map[string]any)["instance_id"] = "host-1" },
		Options{InstanceIDOptional: true}, "created_by.instance_id", "UUID")
	// 來源紀錄是自由的物件（Hub 另外依白名單過濾）。
	c := cues(t)
	path := "artifacts/" + artID + ".json"
	m := manifest(map[string][]byte{path: c}, transcriptArtifact())
	delete(m["created_by"].(map[string]any), "instance_id")
	art(m)["provenance"] = map[string]any{"workdir": "/data/tmp", "nested": map[string]any{"a": []any{1, "b"}}}
	b, err := Read(build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: path, data: c}), Options{InstanceIDOptional: true})
	if err != nil {
		t.Fatalf("Hub 的 bundle：%v", err)
	}
	if !bytes.Contains(b.Manifest.Artifacts[0].Provenance, []byte("workdir")) {
		t.Errorf("provenance = %s", b.Manifest.Artifacts[0].Provenance)
	}
}

func TestManifestJSON(t *testing.T) {
	c := cues(t)
	path := "artifacts/" + artID + ".json"
	m := mj(t, manifest(map[string][]byte{path: c}, transcriptArtifact()))
	read := func(manifest []byte) error {
		_, err := Read(build(t, entrySpec{name: ManifestName, data: manifest}, entrySpec{name: path, data: c}), Options{})
		return err
	}
	// 重複的鍵（不同的解析器可能取不同的值）。
	dupKey := bytes.Replace(m, []byte(`"license":"CC-BY-4.0"`), []byte(`"license":"CC-BY-4.0","license":"x"`), 1)
	if err := read(dupKey); err == nil || !strings.Contains(err.Error(), "重複的欄位") {
		t.Errorf("重複的鍵 = %v", err)
	}
	if err := read(append(bytes.Clone(m), []byte(`{}`)...)); err == nil {
		t.Error("結尾多餘的內容要被拒絕")
	}
	if err := read([]byte(`[]`)); err == nil {
		t.Error("不是物件要被拒絕")
	}
	deep := strings.Repeat(`{"a":`, 100) + "1" + strings.Repeat("}", 100)
	if err := read([]byte(deep)); err == nil {
		t.Error("太深的巢狀要被拒絕")
	}
	for _, v := range []string{`1`, `"0"`, `0.0`} {
		x := bytes.Replace(m, []byte(`"bundle_version":0`), []byte(`"bundle_version":`+v), 1)
		var e *Error
		if err := read(x); !errors.As(err, &e) || e.Code != CodeUnsupportedVersion {
			t.Errorf("bundle_version %s = %v", v, err)
		}
	}
}

func TestFilesConsistency(t *testing.T) {
	c := cues(t)
	path := "artifacts/" + artID + ".json"
	m := manifest(map[string][]byte{path: c}, transcriptArtifact())
	// zip 中有 manifest 沒列的檔案。
	mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: path, data: c},
		entrySpec{name: "artifacts/extra.jpg", data: []byte("x")}), Options{}, "沒有列在 manifest 中")
	// manifest 列的檔案不在 zip 中。
	mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}), Options{}, "不在 bundle 中")
	// 竄改內容（sha256 不符）與大小不符。
	mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: path, data: append(bytes.Clone(c), ' ')}),
		Options{}, "與 manifest 記錄的 sha256 不符")
	m["files"].([]any)[0].(map[string]any)["size"] = len(c) + 1
	mustErr(t, build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: path, data: c}), Options{}, "sha256 不符")
}

func jpegBytes(t *testing.T) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestValidateContents(t *testing.T) {
	path := "artifacts/" + artID + ".json"
	check := func(kind, format string, d []byte, extra map[string][]byte) error {
		a := transcriptArtifact()
		a["kind"], a["format"] = kind, format
		if kind != "transcript" {
			delete(a, "variant")
		}
		files := map[string][]byte{path: d}
		entries := []entrySpec{{name: path, data: d}}
		for p, x := range extra {
			files[p] = x
			entries = append(entries, entrySpec{name: p, data: x})
		}
		m := manifest(files, a)
		b, err := Read(build(t, append([]entrySpec{{name: ManifestName, data: mj(t, m)}}, entries...)...), Options{})
		if err != nil {
			t.Fatal(err)
		}
		return b.ValidateContents()
	}
	if err := check("transcript", CuesFormat, []byte(`{"lang":"en","variant":"nope","cues":[]}`), nil); err == nil {
		t.Error("不合法的 cues JSON 要被拒絕")
	}
	if err := check("transcript", "srt", cues(t), nil); err == nil {
		t.Error("字幕的格式要是 notesteme-cues+json")
	}
	if err := check("note", "markdown", []byte{0xff, 0xfe}, nil); err == nil {
		t.Error("筆記要是 UTF-8")
	}
	if err := check("chapters", "json", []byte(`{`), nil); err == nil {
		t.Error("章節要是 JSON")
	}
	if err := check("note", "markdown", []byte("# ok"), map[string][]byte{"artifacts/" + artID + "-001.jpg": []byte("not jpeg")}); err == nil ||
		!strings.Contains(err.Error(), "JPEG") {
		t.Errorf("不是 JPEG 的圖片 = %v", err)
	}
	if err := check("frames", "json", []byte(`{"frames":[]}`), map[string][]byte{"artifacts/" + artID + "-001.jpg": jpegBytes(t)}); err != nil {
		t.Errorf("有效的畫面 = %v", err)
	}
}

func TestShowEntryAndFormatSize(t *testing.T) {
	if got := ShowEntry("a\x00b\nc"); got != "a�b�c" {
		t.Errorf("ShowEntry = %q", got)
	}
	if got := []rune(ShowEntry(strings.Repeat("x", 300))); len(got) != 200 {
		t.Errorf("ShowEntry 的長度 = %d", len(got))
	}
	for n, want := range map[int64]string{64 << 20: "64 MiB", 1 << 10: "1 KiB", 1 << 30: "1 GiB", 1000: "1000 bytes"} {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q", n, got)
		}
	}
}

// FuzzRead：任何輸入都不能 panic，通過的 bundle 一定和 manifest 一致。
func FuzzRead(f *testing.F) {
	f.Add(build(f, valid(f)...))
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := Read(data, Options{Limits: Limits{MaxTotalBytes: 8 << 20, MaxFileBytes: 4 << 20}})
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Message == "" {
				t.Fatalf("錯誤不是 *Error：%v", err)
			}
			return
		}
		for _, file := range b.Manifest.Files {
			d, ok := b.File(file.Path)
			if !ok || shaOf(d) != file.SHA256 {
				t.Fatalf("%s 與 manifest 不一致", file.Path)
			}
		}
		_ = b.ValidateContents()
	})
}

// FuzzManifest：manifest 的解析器不能 panic。
func FuzzManifest(f *testing.F) {
	f.Add(mj(f, manifest(nil)))
	f.Add([]byte(`{"bundle_version":0,"item":{}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseManifest(data, Options{InstanceIDOptional: true})
	})
}
