package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是 bundle 的匯出（BND-R1、R2）：只放現行版本的字幕、筆記、章節、贊助片段與 frames（含選中的畫面），
// 來源紀錄去掉路徑與 secrets；組好之後再掃描一次整份內容，出現本機路徑或 secret 時不送出。

// instanceIDKey 是 settings 中實例 ID 的鍵（BND-R1 的 created_by.instance_id）。
const instanceIDKey = "instance_id"

// bundleFormatNote 是 bundle 中筆記的格式（Markdown 本文；產品內部的 note 產出是 notesteme-note+json）。
const bundleFormatNote = "text/markdown"

// ErrBundleLeak 表示組好的 bundle 含有本機路徑或 secret（BND-R2 的最後一道檢查）；這是程式錯誤，回 500。
var ErrBundleLeak = errors.New("bundle 含有本機路徑或 secret，已停止匯出")

// BundleExport 是匯出的結果。
type BundleExport struct {
	// Name 是下載的檔名（<代號清理後>.notesteme.zip）。
	Name string
	Data []byte
	Item store.Item
}

// InstanceID 回傳這個實例的 ID（UUIDv7）；第一次呼叫時產生並存進 settings。
func (p *Pipeline) InstanceID(ctx context.Context) (string, error) {
	if v, ok, err := p.st.Read().Setting(ctx, instanceIDKey); err != nil || ok {
		return v, err
	}
	var id string
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		v, ok, err := r.Setting(ctx, instanceIDKey)
		if err != nil {
			return err
		}
		if ok {
			id = v
			return nil
		}
		id = store.NewID()
		return r.SetSetting(ctx, instanceIDKey, id)
	})
	return id, err
}

// SetInstanceID 設定實例 ID。只給驗收測試的 harness 使用（`the bundle instance ID is`）；必須是 UUID。
func (p *Pipeline) SetInstanceID(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return invalidf("instance_id 必須是 UUID。")
	}
	return p.st.Tx(ctx, func(r *store.Repo) error { return r.SetSetting(ctx, instanceIDKey, strings.ToLower(id)) })
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// BundleFileName 是代號轉成的檔名（BND-R1）：英數字、-、_ 之外的字元換成 -，最多 80 字元，加上 .notesteme.zip。
func BundleFileName(ref string) string {
	var b strings.Builder
	for _, r := range ref {
		if b.Len() >= 80 {
			break
		}
		if r < 0x80 && (r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "item"
	}
	return name + ".notesteme.zip"
}

// itemForBundle 以 ID 或代號找影音；垃圾桶中的回 *InTrashError（BND-R1：404 in_trash）。
func (p *Pipeline) itemForBundle(ctx context.Context, idOrRef string) (store.Item, error) {
	if _, _, _, ok := source.SplitRef(idOrRef); ok {
		return p.ResolveItem(ctx, idOrRef)
	}
	it, err := p.st.Read().ItemByID(ctx, idOrRef)
	if err != nil {
		return store.Item{}, err
	}
	if it.DeletedAt != nil {
		return store.Item{}, &InTrashError{ItemID: it.ID}
	}
	return it, nil
}

// ExportBundle 匯出影音的 bundle（BND-R1、R2）。錯誤：store.ErrNotFound、*InTrashError、ErrBundleLeak、產出檔損毀。
func (p *Pipeline) ExportBundle(ctx context.Context, idOrRef string) (BundleExport, error) {
	it, err := p.itemForBundle(ctx, idOrRef)
	if err != nil {
		return BundleExport{}, err
	}
	instance, err := p.InstanceID(ctx)
	if err != nil {
		return BundleExport{}, err
	}
	r := p.st.Read()
	arts, err := r.CurrentArtifacts(ctx, it.ID, "")
	if err != nil {
		return BundleExport{}, err
	}
	man := bundle.OutManifest{BundleVersion: 0, CreatedBy: bundle.OutCreatedBy{InstanceID: instance, AppVersion: p.appVersion()},
		CreatedAt: p.clock.Now().UTC().Format(time.RFC3339)}
	if man.Item, err = p.bundleItem(ctx, r, it); err != nil {
		return BundleExport{}, err
	}
	exported := map[string]bool{}
	for _, a := range arts {
		if slices.Contains(bundle.Kinds, a.Kind) {
			exported[a.ID] = true
		}
	}
	var entries []bundle.OutEntry
	for _, a := range arts {
		if !exported[a.ID] {
			continue
		}
		data, err := p.readArtifactBytes(a)
		if err != nil {
			return BundleExport{}, err
		}
		oa := bundle.OutArtifact{ID: a.ID, Kind: a.Kind, Format: a.Format, Path: bundle.ArtifactsDir + a.ID + ".json",
			Provenance: sanitizeProvenance(a.Provenance)}
		if a.Lang != "" {
			lang := a.Lang
			oa.Lang = &lang
		}
		for _, par := range a.Parents {
			if exported[par] {
				oa.Parents = append(oa.Parents, par)
			}
		}
		switch a.Kind {
		case "transcript":
			oa.Variant = a.Variant
		case KindNote:
			var doc noteDoc
			if err := json.Unmarshal(data, &doc); err != nil {
				return BundleExport{}, fmt.Errorf("讀取筆記 %s：%w", a.ID, err)
			}
			data = []byte(doc.Markdown)
			oa.Format, oa.Path = bundleFormatNote, bundle.ArtifactsDir+a.ID+".md"
		case KindFrames:
			body, imgs, err := p.exportFrames(it.ID, a, data)
			if err != nil {
				return BundleExport{}, err
			}
			data = body
			entries = append(entries, imgs...)
		}
		entries = append(entries, bundle.OutEntry{Path: oa.Path, Data: data})
		man.Artifacts = append(man.Artifacts, oa)
	}
	zipData, err := bundle.Write(man, entries)
	if err != nil {
		return BundleExport{}, err
	}
	if err := p.checkBundleLeak(man, entries); err != nil {
		return BundleExport{}, err
	}
	return BundleExport{Name: BundleFileName(source.Ref(it.Platform, it.ExternalID, it.Part)), Data: zipData, Item: it}, nil
}

func (p *Pipeline) appVersion() string {
	if p.version == "" {
		return "dev"
	}
	return p.version
}

// readArtifactBytes 讀取產出檔並確認 sha256。
func (p *Pipeline) readArtifactBytes(a store.Artifact) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(a.Path)))
	if err != nil {
		return nil, err
	}
	if sha256Hex(b) != a.SHA256 {
		return nil, fmt.Errorf("%s：%w", a.Path, ErrCorruptArtifact)
	}
	return b, nil
}

// bundleItem 組出 manifest 的 item（BND-R2：local 影音的 external_id 是內容的 sha256，source_name 是不含目錄的檔名）。
func (p *Pipeline) bundleItem(ctx context.Context, r *store.Repo, it store.Item) (bundle.OutItem, error) {
	oi := bundle.OutItem{Platform: it.Platform, ExternalID: it.ExternalID, Title: truncateRunes(it.Title, 500),
		Availability: it.Availability}
	if !slices.Contains(bundle.Platforms, it.Platform) {
		return oi, fmt.Errorf("影音 %s 的平台 %q 不能匯出成 bundle", it.ID, it.Platform)
	}
	if it.Part > 0 {
		part := it.Part
		oi.Part = &part
	}
	if it.Platform == source.PlatformLocal {
		sha, err := p.localContentSHA(ctx, it)
		if err != nil {
			return oi, err
		}
		oi.ExternalID = sha
		name := baseName(it.ExternalID)
		if it.SourcePath != "" {
			name = baseName(it.SourcePath)
		}
		oi.SourceName = &name
	}
	if oi.Availability == "" {
		oi.Availability = "unknown"
	}
	if it.ChannelID != "" {
		ch, err := r.ChannelByID(ctx, it.ChannelID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return oi, err
		}
		if err == nil && ch.ExternalID != "" {
			oc := &bundle.OutChannel{ExternalID: truncateRunes(ch.ExternalID, 256)}
			if ch.Title != "" {
				t := truncateRunes(ch.Title, 500)
				oc.Title = &t
			}
			oi.Channel = oc
		}
	}
	if it.PublishedAt != nil {
		d := it.PublishedAt.UTC().Format("2006-01-02")
		oi.PublishedAt = &d
	}
	if it.DurationS != nil {
		d := float64(*it.DurationS)
		oi.DurationS = &d
	}
	if it.OriginalLang != "" && bundle.ValidLang(it.OriginalLang) {
		l := it.OriginalLang
		oi.OriginalLang = &l
	}
	return oi, nil
}

// localContentSHA 回傳本機影音內容的 sha256：還沒算過時（metadata 步驟之前）直接雜湊檔案，不寫回 DB。
func (p *Pipeline) localContentSHA(ctx context.Context, it store.Item) (string, error) {
	if it.ContentSHA256 != "" {
		return it.ContentSHA256, nil
	}
	a := localAdapter{files: p.files, media: p.media, dataDir: p.dataDir}
	f, _, err := a.open(it.SourcePath, it.ExternalID)
	if err != nil {
		return "", invalidf("這部本機影音還沒有算出內容的 sha256，檔案也無法讀取，不能匯出成 bundle。")
	}
	defer f.Close()
	return hashFile(ctx, f)
}

// baseName 取路徑的最後一段（/ 與 \ 都當成分隔字元）。
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func truncateRunes(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// exportFrames 只留下選中的畫面（BND-R1：不含候選畫面），圖片寫成 artifacts/<frames-id>-<nnn>.jpg。
func (p *Pipeline) exportFrames(itemID string, a store.Artifact, data []byte) ([]byte, []bundle.OutEntry, error) {
	var man framesManifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, nil, fmt.Errorf("讀取 frames %s：%w", a.ID, err)
	}
	if !versionPattern.MatchString(a.ID) {
		return nil, nil, fmt.Errorf("frames %s 的 ID 不是版本目錄的格式", a.ID)
	}
	dir := framesDir(p.dataDir, itemID, a.ID)
	var keep []frameCandidate
	var imgs []bundle.OutEntry
	for _, c := range man.Candidates {
		if !c.Selected {
			continue
		}
		if c.N < 1 || c.N > maxBundleFrame {
			return nil, nil, fmt.Errorf("frames %s 的畫面編號 %d 超出範圍", a.ID, c.N)
		}
		b, err := os.ReadFile(filepath.Join(dir, frameFile(c.N)))
		if err != nil {
			return nil, nil, err
		}
		name := bundleFrameName(a.ID, c.N)
		c.File, c.Thumb = name, ""
		keep = append(keep, c)
		imgs = append(imgs, bundle.OutEntry{Path: bundle.ArtifactsDir + name, Data: b})
	}
	man.Candidates = nonNilCandidates(keep)
	body, err := json.Marshal(man)
	return body, imgs, err
}

// maxBundleFrame 是 bundle 中畫面編號的上限（三位數，BND-R1）。
const maxBundleFrame = 999

func bundleFrameName(framesID string, n int) string { return fmt.Sprintf("%s-%03d.jpg", framesID, n) }

// ---- 來源紀錄的清理（BND-R2）----

var (
	secretKey   = regexp.MustCompile(`(?i)(^|[_-])(api[_-]?key|apikey|token|secret|password|passwd|authorization|auth|master[_-]?key|credentials?|bearer)($|[_-])`)
	cookieKey   = regexp.MustCompile(`(?i)cookie`)
	windowsPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// sanitizeProvenance 去掉來源紀錄中的 secrets 與本機路徑：像 secret 的欄位刪除；cookie 只留布林值 cookies_used；
// 路徑只留檔名；網址去掉帳密與 query，指向本機或私有網段的網址整個刪除。沒有可以留下的內容時回傳 {}。
func sanitizeProvenance(raw json.RawMessage) json.RawMessage {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return json.RawMessage(`{}`)
	}
	m, ok := sanitizeValue(v).(map[string]any)
	if !ok || m == nil {
		return json.RawMessage(`{}`)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		cookies := false
		sawCookie := false
		for k, x := range t {
			switch {
			case k == "cookies_used":
				sawCookie = true
				cookies = cookies || truthy(x)
			case cookieKey.MatchString(k):
				sawCookie = true
				cookies = cookies || truthy(x)
			case secretKey.MatchString(k):
			default:
				if s, drop := sanitizeOne(x); !drop {
					out[k] = s
				}
			}
		}
		if sawCookie {
			out["cookies_used"] = cookies
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if s, drop := sanitizeOne(x); !drop {
				out = append(out, s)
			}
		}
		return out
	}
	return v
}

// sanitizeOne 清理一個值；drop 為 true 代表整個刪除。
func sanitizeOne(v any) (any, bool) {
	s, ok := v.(string)
	if !ok {
		return sanitizeValue(v), false
	}
	return sanitizeString(s)
}

func sanitizeString(s string) (string, bool) {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", true
		}
		if privateHost(u.Hostname()) {
			return "", true
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String(), false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `\\`) || windowsPath.MatchString(s) {
		return baseName(s), false
	}
	return s, false
}

func privateHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || !strings.Contains(h, ".") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		return t.String() != "0"
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	}
	return true
}

// ---- 最後一道檢查（BND-R2）----

// leakNeedles 是不能出現在 bundle 中的字串：資料目錄、本機檔案的根目錄、家目錄，以及環境變數中像 secret 的值。
func (p *Pipeline) leakNeedles() []string {
	var out []string
	add := func(s string) {
		s = strings.TrimRight(s, `/\`)
		if len(s) >= 4 {
			out = append(out, s)
		}
	}
	if p.dataDir != "" {
		add(p.dataDir)
		if abs, err := filepath.Abs(p.dataDir); err == nil {
			add(abs)
		}
	}
	if p.files != nil {
		for _, r := range p.files.Roots() {
			add(r)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		add(home)
	}
	for k, v := range p.env {
		if len(v) >= 8 && (secretKey.MatchString(k) || cookieKey.MatchString(k)) {
			out = append(out, v)
		}
	}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "NOTESTEME_") && len(v) >= 8 && (secretKey.MatchString(k) || cookieKey.MatchString(k)) {
			out = append(out, v)
		}
	}
	return out
}

func (p *Pipeline) checkBundleLeak(man bundle.OutManifest, entries []bundle.OutEntry) error {
	mj, err := json.Marshal(man)
	if err != nil {
		return err
	}
	needles := p.leakNeedles()
	check := func(what string, b []byte) error {
		for _, n := range needles {
			if bytes.Contains(b, []byte(n)) {
				p.log.Error("bundle 含有本機路徑或 secret", "entry", what)
				return ErrBundleLeak
			}
		}
		return nil
	}
	if err := check(bundle.ManifestName, mj); err != nil {
		return err
	}
	for _, e := range entries {
		if err := check(e.Path, e.Data); err != nil {
			return err
		}
	}
	return nil
}
