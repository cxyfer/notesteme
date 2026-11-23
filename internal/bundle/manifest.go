package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// manifest.json 的 schema（BND-R6）：不允許未定義的欄位，只有 artifacts[].provenance 是自由的物件
// （來源紀錄的欄位隨 pipeline 增加；Hub 另外依白名單過濾，HREP-R4）。

// Manifest 是 bundle v0 的 manifest。
type Manifest struct {
	BundleVersion int
	Item          Item
	Artifacts     []Artifact
	Files         []File
	CreatedBy     CreatedBy
	CreatedAt     string
	// License 是 SPDX ID；沒有指定時是空字串。
	License string
}

// Item 是影音的 metadata；可以省略的欄位以零值表示。
type Item struct {
	Platform     string
	ExternalID   string
	Part         int // 沒有分P時是 0
	Title        string
	Channel      Channel
	PublishedAt  string
	DurationS    *float64
	OriginalLang string
	Availability string
	SourceName   string
}

// Channel 是頻道。
type Channel struct{ ExternalID, Title string }

// Artifact 是一個產出。
type Artifact struct {
	ID, Kind, Variant, Lang, Format, Path string
	Parents                               []string
	// Provenance 是來源紀錄的原始 JSON 物件。
	Provenance json.RawMessage
}

// File 是 manifest 的完整性清單的一項。
type File struct {
	Path, SHA256 string
	Size         int64
}

// CreatedBy 是產生 bundle 的實例。
type CreatedBy struct{ InstanceID, AppVersion string }

// 產出的種類與字幕變體（BND-R6）。
var (
	Kinds    = []string{"transcript", "note", "chapters", "sponsor_segments", "frames"}
	Variants = []string{"source", "normalized", "segmented", "corrected", "translated", "edited"}
	// Platforms 是 item.platform 允許的值。
	Platforms = []string{"youtube", "bilibili", "podcast", "local"}
)

const (
	maxTitle     = 500
	maxArtifacts = 500
	maxShortText = 256
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// langRe 是 BCP 47 的語法（主語言 2～3 或 5～8 個字母，之後是 1～8 個英數字的子標籤）。
	langRe = regexp.MustCompile(`^(?:[A-Za-z]{2,3}|[A-Za-z]{5,8})(?:-[A-Za-z0-9]{1,8})*$`)
)

// schemaError 是 manifest 不符合 schema 的錯誤（BND-R10：「manifest.json 不符合格式：<點號路徑>：<原因>。」）。
func schemaError(path, reason string) *Error {
	shown := path
	if shown == "" {
		shown = "（根）"
	}
	return &Error{Code: CodeInvalid, Message: fmt.Sprintf("manifest.json 不符合格式：%s：%s。", shown, reason), Path: path, Entry: ManifestName}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// decodeStrict 解碼 JSON（數字保留為 json.Number），重複的鍵與結尾多餘的內容都是錯誤。
func decodeStrict(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 之後還有其他內容")
	}
	return v, nil
}

// maxDepth 限制巢狀的深度（來源紀錄是自由的物件）。
const maxDepth = 64

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("巢狀太深")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			keys := []string{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				if _, dup := m[k]; dup {
					return nil, fmt.Errorf("重複的欄位 %q", k)
				}
				v, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				m[k] = v
				keys = append(keys, k)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return orderedObject{m: m, keys: keys}, nil
		case '[':
			var a []any
			for dec.More() {
				v, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if a == nil {
				a = []any{}
			}
			return a, nil
		}
	}
	return tok, nil
}

// orderedObject 是保留鍵順序的 JSON 物件（未定義的欄位依出現順序回報第一個）。
type orderedObject struct {
	m    map[string]any
	keys []string
}

// object 檢查 v 是物件、只有 allowed 的欄位、required 的欄位都在。
func object(v any, path string, allowed, required []string) (orderedObject, *Error) {
	o, ok := v.(orderedObject)
	if !ok {
		return orderedObject{}, schemaError(path, "必須是物件")
	}
	for _, k := range o.keys {
		if !slices.Contains(allowed, k) {
			return o, schemaError(join(path, k), "不允許的欄位")
		}
	}
	for _, k := range required {
		if _, ok := o.m[k]; !ok {
			return o, schemaError(join(path, k), "缺少這個欄位")
		}
	}
	return o, nil
}

func str(o orderedObject, key, path string, maxLen int, nullable bool) (string, *Error) {
	v, ok := o.m[key]
	if !ok || (nullable && v == nil) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", schemaError(join(path, key), "必須是字串")
	}
	if !utf8.ValidString(s) {
		return "", schemaError(join(path, key), "不是合法的 UTF-8")
	}
	if maxLen > 0 && utf8.RuneCountInString(s) > maxLen {
		return "", schemaError(join(path, key), fmt.Sprintf("最多 %d 個字元", maxLen))
	}
	return s, nil
}

func enum(o orderedObject, key, path string, values []string) (string, *Error) {
	s, e := str(o, key, path, maxShortText, false)
	if e != nil {
		return "", e
	}
	if !slices.Contains(values, s) {
		return "", schemaError(join(path, key), "必須是 "+strings.Join(values, "、")+" 之一")
	}
	return s, nil
}

func integer(v any, path string) (int64, *Error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, schemaError(path, "必須是整數")
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		return 0, schemaError(path, "必須是整數")
	}
	return i, nil
}

// parseManifest 依 BND-R6 驗證 manifest。bundle_version 先檢查：不是 0 時回 bundle_unsupported_version。
func parseManifest(data []byte, opt Options) (Manifest, *Error) {
	var m Manifest
	root, err := decodeStrict(data)
	if err != nil {
		return m, schemaError("", "不是合法的 JSON（"+err.Error()+"）")
	}
	o, ok := root.(orderedObject)
	if !ok {
		return m, schemaError("", "必須是物件")
	}
	if v, ok := o.m["bundle_version"]; ok {
		n, isNum := v.(json.Number)
		if !isNum || n.String() != "0" {
			shown := fmt.Sprint(v)
			if s, isStr := v.(string); isStr {
				shown = strconv.Quote(s)
			}
			return m, &Error{Code: CodeUnsupportedVersion, Path: "bundle_version", Entry: ManifestName,
				Message: fmt.Sprintf("不支援的 bundle 版本：%s。這個版本只能匯入 bundle_version 0。", ShowEntry(shown))}
		}
	}
	o, e := object(root, "", []string{"bundle_version", "item", "artifacts", "files", "created_by", "created_at", "license"},
		[]string{"bundle_version", "item", "artifacts", "files", "created_by"})
	if e != nil {
		return m, e
	}
	if m.Item, e = parseItem(o.m["item"]); e != nil {
		return m, e
	}
	if m.Files, e = parseFiles(o.m["files"]); e != nil {
		return m, e
	}
	if m.Artifacts, e = parseArtifacts(o.m["artifacts"], m.Files); e != nil {
		return m, e
	}
	if m.CreatedBy, e = parseCreatedBy(o.m["created_by"], opt); e != nil {
		return m, e
	}
	if m.CreatedAt, e = str(o, "created_at", "", maxShortText, true); e != nil {
		return m, e
	}
	if m.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
			return m, schemaError("created_at", "必須是 RFC 3339 的時間")
		}
	}
	if m.License, e = str(o, "license", "", maxShortText, true); e != nil {
		return m, e
	}
	return m, nil
}

func parseItem(v any) (Item, *Error) {
	var it Item
	o, e := object(v, "item", []string{"platform", "external_id", "part", "title", "channel", "published_at",
		"duration_s", "original_lang", "availability", "source_name"}, []string{"platform", "external_id", "title", "availability"})
	if e != nil {
		return it, e
	}
	if it.Platform, e = enum(o, "platform", "item", Platforms); e != nil {
		return it, e
	}
	if it.ExternalID, e = str(o, "external_id", "item", maxShortText, false); e != nil {
		return it, e
	}
	if it.ExternalID == "" {
		return it, schemaError("item.external_id", "不能是空字串")
	}
	if p, ok := o.m["part"]; ok && p != nil {
		n, e := integer(p, "item.part")
		if e != nil {
			return it, e
		}
		if n < 1 || n > 100000 {
			return it, schemaError("item.part", "必須是 1 以上的整數或 null")
		}
		it.Part = int(n)
	}
	if it.Title, e = str(o, "title", "item", maxTitle, false); e != nil {
		return it, e
	}
	if c, ok := o.m["channel"]; ok && c != nil {
		co, e := object(c, "item.channel", []string{"external_id", "title"}, []string{"external_id"})
		if e != nil {
			return it, e
		}
		if it.Channel.ExternalID, e = str(co, "external_id", "item.channel", maxShortText, false); e != nil {
			return it, e
		}
		if it.Channel.Title, e = str(co, "title", "item.channel", maxTitle, true); e != nil {
			return it, e
		}
	}
	if it.PublishedAt, e = str(o, "published_at", "item", 64, true); e != nil {
		return it, e
	}
	if d, ok := o.m["duration_s"]; ok && d != nil {
		n, isNum := d.(json.Number)
		f, err := n.Float64()
		if !isNum || err != nil || f < 0 || f > 1e7 {
			return it, schemaError("item.duration_s", "必須是 0 以上的數字或 null")
		}
		it.DurationS = &f
	}
	if it.OriginalLang, e = str(o, "original_lang", "item", 64, true); e != nil {
		return it, e
	}
	if it.OriginalLang != "" && !langRe.MatchString(it.OriginalLang) {
		return it, schemaError("item.original_lang", "必須是 BCP 47 的語言標記")
	}
	if it.Availability, e = str(o, "availability", "item", 64, false); e != nil {
		return it, e
	}
	if it.Availability == "" {
		return it, schemaError("item.availability", "不能是空字串")
	}
	if it.SourceName, e = str(o, "source_name", "item", maxShortText, true); e != nil {
		return it, e
	}
	return it, nil
}

func parseFiles(v any) ([]File, *Error) {
	a, ok := v.([]any)
	if !ok {
		return nil, schemaError("files", "必須是陣列")
	}
	seen := map[string]bool{}
	out := make([]File, 0, len(a))
	for i, x := range a {
		p := fmt.Sprintf("files.%d", i)
		o, e := object(x, p, []string{"path", "sha256", "size"}, []string{"path", "sha256", "size"})
		if e != nil {
			return nil, e
		}
		var f File
		if f.Path, e = str(o, "path", p, maxNameBytes, false); e != nil {
			return nil, e
		}
		if dir, ok := allowedName(f.Path); !ok || dir || f.Path == ManifestName {
			return nil, schemaError(p+".path", "必須是 artifacts/<檔名>")
		}
		if seen[strings.ToLower(f.Path)] {
			return nil, schemaError(p+".path", "重複的路徑")
		}
		seen[strings.ToLower(f.Path)] = true
		if f.SHA256, e = str(o, "sha256", p, 64, false); e != nil {
			return nil, e
		}
		if !shaRe.MatchString(f.SHA256) {
			return nil, schemaError(p+".sha256", "必須是 64 個小寫十六進位字元")
		}
		n, e := integer(o.m["size"], p+".size")
		if e != nil {
			return nil, e
		}
		if n < 0 {
			return nil, schemaError(p+".size", "不能是負數")
		}
		f.Size = n
		out = append(out, f)
	}
	return out, nil
}

func parseArtifacts(v any, files []File) ([]Artifact, *Error) {
	a, ok := v.([]any)
	if !ok {
		return nil, schemaError("artifacts", "必須是陣列")
	}
	if len(a) > maxArtifacts {
		return nil, schemaError("artifacts", fmt.Sprintf("最多 %d 個", maxArtifacts))
	}
	listed := map[string]bool{}
	for _, f := range files {
		listed[f.Path] = true
	}
	ids := map[string]bool{}
	paths := map[string]bool{}
	out := make([]Artifact, 0, len(a))
	for i, x := range a {
		p := fmt.Sprintf("artifacts.%d", i)
		o, e := object(x, p, []string{"id", "kind", "variant", "lang", "format", "path", "parents", "provenance", "sha256"},
			[]string{"id", "kind", "format", "path", "provenance"})
		if e != nil {
			return nil, e
		}
		var ar Artifact
		if ar.ID, e = str(o, "id", p, 64, false); e != nil {
			return nil, e
		}
		if !uuidRe.MatchString(ar.ID) {
			return nil, schemaError(p+".id", "必須是 UUID")
		}
		if ids[strings.ToLower(ar.ID)] {
			return nil, schemaError(p+".id", "重複的 ID")
		}
		ids[strings.ToLower(ar.ID)] = true
		if ar.Kind, e = enum(o, "kind", p, Kinds); e != nil {
			return nil, e
		}
		if _, has := o.m["variant"]; has || ar.Kind == "transcript" {
			if ar.Kind != "transcript" {
				return nil, schemaError(p+".variant", "只有 transcript 有 variant")
			}
			if ar.Variant, e = enum(o, "variant", p, Variants); e != nil {
				return nil, e
			}
		}
		if ar.Lang, e = str(o, "lang", p, 64, true); e != nil {
			return nil, e
		}
		if ar.Lang != "" && !langRe.MatchString(ar.Lang) {
			return nil, schemaError(p+".lang", "必須是 BCP 47 的語言標記")
		}
		if ar.Kind == "transcript" && ar.Lang == "" {
			return nil, schemaError(p+".lang", "字幕必須有語言")
		}
		if ar.Format, e = str(o, "format", p, 64, false); e != nil {
			return nil, e
		}
		if ar.Path, e = str(o, "path", p, maxNameBytes, false); e != nil {
			return nil, e
		}
		if !listed[ar.Path] {
			return nil, schemaError(p+".path", "必須列在 files 中")
		}
		if paths[ar.Path] {
			return nil, schemaError(p+".path", "兩個產出不能指向同一個檔案")
		}
		paths[ar.Path] = true
		if ps, ok := o.m["parents"]; ok && ps != nil {
			pa, ok := ps.([]any)
			if !ok {
				return nil, schemaError(p+".parents", "必須是陣列")
			}
			for j, x := range pa {
				s, ok := x.(string)
				if !ok || !uuidRe.MatchString(s) {
					return nil, schemaError(fmt.Sprintf("%s.parents.%d", p, j), "必須是 UUID")
				}
				ar.Parents = append(ar.Parents, s)
			}
		}
		if sha, ok := o.m["sha256"]; ok {
			// ADR-0005 的舊欄位：雜湊以 files 為準（N2），這裡只檢查格式。
			s, isStr := sha.(string)
			if !isStr || !shaRe.MatchString(s) {
				return nil, schemaError(p+".sha256", "必須是 64 個小寫十六進位字元")
			}
		}
		prov, ok := o.m["provenance"].(orderedObject)
		if !ok {
			return nil, schemaError(p+".provenance", "必須是物件")
		}
		raw, err := json.Marshal(toPlain(prov))
		if err != nil {
			return nil, schemaError(p+".provenance", "無法編碼")
		}
		ar.Provenance = raw
		out = append(out, ar)
	}
	return out, nil
}

// toPlain 把 decodeStrict 的結果轉回 encoding/json 可以編碼的值。
func toPlain(v any) any {
	switch t := v.(type) {
	case orderedObject:
		m := make(map[string]any, len(t.m))
		for k, x := range t.m {
			m[k] = toPlain(x)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = toPlain(x)
		}
		return out
	}
	return v
}

func parseCreatedBy(v any, opt Options) (CreatedBy, *Error) {
	var c CreatedBy
	required := []string{"instance_id", "app_version"}
	if opt.InstanceIDOptional {
		required = []string{"app_version"}
	}
	o, e := object(v, "created_by", []string{"instance_id", "app_version"}, required)
	if e != nil {
		return c, e
	}
	if c.InstanceID, e = str(o, "instance_id", "created_by", 64, false); e != nil {
		return c, e
	}
	if _, has := o.m["instance_id"]; has && !uuidRe.MatchString(c.InstanceID) {
		return c, schemaError("created_by.instance_id", "必須是 UUID")
	}
	if c.AppVersion, e = str(o, "app_version", "created_by", 64, false); e != nil {
		return c, e
	}
	return c, nil
}
