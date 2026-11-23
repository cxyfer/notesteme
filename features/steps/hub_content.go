package steps

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/features/internal/fakeoauth"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// registerHubContent 註冊「Hub 的內容」的步驟（steps.md 4.31，M5）。
//
// M5-S3 實作全部的句型（真 Hub）：seeding 直接寫 Hub 的 DB 與 blob，bundle 在場景目錄產生（不 commit 二進位 fixture），
// 分享以 session 送出 multipart。fakehub（HSHR）的 seeding 仍是 pending（S7），HSHR 的場景停在自己的閘門。
func registerHubContent(r *Registry, s *Scenario) {
	r.Step(`the Hub has the version {string} of {string} with:`, s.hubHasVersion)
	r.Step(`the Hub version {string} has the cues:`, s.hubVersionHasCues)
	r.Step(`a bundle {string} of {string} with the artifacts:`, s.aBundle)
	r.Step(`the bundle {string} has the manifest fields:`, s.bundleManifestFields)
	r.Step(`the bundle {string} has a note containing:`, s.bundleNote)
	r.Step(`the bundle {string} is made unsafe with {string}`, s.bundleUnsafe)
	r.Step(`the hub member {string} shares the bundle {string}`, func(who, name string) error { return s.sharesBundle(who, name, nil) })
	r.Step(`the hub member {string} shares the bundle {string} with:`, s.sharesBundle)
	r.Step(`the Hub version of {string} for {string} is known as {string}`, s.hubVersionKnownAs)
	r.Step(`the Hub has {int} versions of {string}`, s.hubHasVersions)
	r.Step(`the Hub version {string} is {string}`, s.hubVersionIs)
	r.Step(`the Hub version {string} is derived from {string} as {string}`, s.hubVersionDerived)
	r.Step(`the Hub stores the content {string} once`, s.hubStoresContentOnce)
}

// hubContent 是場景中 Hub 的內容：bundle、seeding 的版本與每個內容名稱產生過的 sha256。
type hubContent struct {
	bundles  map[string]*hubBundle
	versions map[string]seededVersion
	shas     map[string][]string
}

type seededVersion struct{ id, sha string }

func (s *Scenario) content() *hubContent {
	if s.hub.content == nil {
		s.hub.content = &hubContent{bundles: map[string]*hubBundle{}, versions: map[string]seededVersion{}, shas: map[string][]string{}}
	}
	return s.hub.content
}

// noteContent 記下內容名稱產生的 sha256（`the Hub stores the content … once` 用）。
func (c *hubContent) noteContent(name string, data []byte) string {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if name != "" && !slices.Contains(c.shas[name], sha) {
		c.shas[name] = append(c.shas[name], sha)
	}
	return sha
}

// absent 是表格中代表「不寫這個欄位」的值。
const absent = "<absent>"

var repeatValue = regexp.MustCompile(`^<repeat:(.):([0-9]+)>$`)

// tableValue 展開 <repeat:<字元>:<次數>>。
func tableValue(v string) string {
	if m := repeatValue.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[2])
		return strings.Repeat(m[1], min(n, 1<<20))
	}
	return v
}

// contentBytes 依內容名稱產生確定的內容：兩次相同的 (名稱, 種類, 變體, 語言) 產生完全相同的 bytes（steps.md 4.31）。
func contentBytes(name, kind, variant, lang string) ([]byte, error) {
	switch kind {
	case "transcript":
		cues := make([]subtitle.Cue, 3)
		for i := range cues {
			cues[i] = subtitle.Cue{Index: i + 1, StartMS: int64(i) * 2000, EndMS: int64(i)*2000 + 1800,
				Text: fmt.Sprintf("%s: sentence number %d of the fixture subtitles.", name, i+1), Timing: subtitle.TimingSource}
		}
		return hubCuesJSON(variant, lang, cues)
	case "note":
		return []byte("## " + name + "\n\nA fixture note.\n"), nil
	case "chapters":
		return json.Marshal(map[string]any{"chapters": []map[string]any{{"start_ms": 0, "title": name}}})
	case "frames":
		return json.Marshal(map[string]any{"frames": []any{}, "name": name})
	}
	return nil, fmt.Errorf("不認得的產出種類 %q", kind)
}

func hubCuesJSON(variant, lang string, cues []subtitle.Cue) ([]byte, error) {
	if variant == "" {
		variant = "source"
	}
	var buf bytes.Buffer
	err := subtitle.RenderCuesJSON(&buf, subtitle.Transcript{Lang: lang, Variant: subtitle.Variant(variant), Cues: cues})
	return buf.Bytes(), err
}

// hubVideo 把影音代號（yt:、bili:、pod:、local:）拆成 Hub 的影片識別。
func hubVideo(ref string) (platform, externalID string, part int, err error) {
	platform, externalID, part, ok := source.SplitRef(ref)
	if !ok {
		return "", "", 0, fmt.Errorf("不認得的影音代號 %q", ref)
	}
	return platform, externalID, part, nil
}

// hubStore 在需要時重新啟動 Hub（設定改變過），回傳它的儲存。
func (s *Scenario) hubStore() (hubstore.Store, error) {
	if s.World.HubKind() != harness.HubReal {
		// fakehub 的 seeding（HSHR）由 S7 實作。
		return nil, godog.ErrPending
	}
	if err := s.World.RestartHubIfNeeded(s.ctx); err != nil {
		return nil, err
	}
	return s.World.HubStore()
}

// ensureHubUser 回傳這個人的帳號；沒有登入過時直接在 DB 建立帳號與一個 github 身分（同 fakeoauth 的預設使用者）。
func (s *Scenario) ensureHubUser(st hubstore.Store, name string) (string, error) {
	if id, ok := s.World.Refs["user:"+name]; ok {
		return id, nil
	}
	u := fakeoauth.DefaultUser(fakeoauth.GitHub, name)
	ext := hubstore.ExternalIdentity{Provider: string(fakeoauth.GitHub), Subject: strconv.FormatInt(fakeoauth.NumericID(u.Subject), 10),
		Login: u.Login, Email: u.Email, EmailVerified: u.EmailVerified}
	user, _, err := st.SignIn(s.ctx, ext, hubauth.DisplayName(name, hubauth.Spec{}))
	if err != nil {
		return "", err
	}
	s.World.SetHubRef("user", name, user.ID)
	return user.ID, nil
}

// ---- seeding ----

func (s *Scenario) hubHasVersion(name, ref string, table *godog.Table) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	cfg := s.World.HubServer().Config()
	rows, err := fieldRows(table)
	if err != nil {
		return err
	}
	f := map[string]string{"kind": "transcript", "lang": "en", "uploader": "uploader", "model_verified": "true",
		"license": "CC-BY-4.0", "score": "0", "state": "visible"}
	for _, r := range rows {
		f[r[0]] = r[1]
	}
	if _, ok := f["variant"]; !ok && f["kind"] == "transcript" {
		f["variant"] = "source"
	}
	platform, extID, part, err := hubVideo(ref)
	if err != nil {
		return err
	}
	uid, err := s.ensureHubUser(st, f["uploader"])
	if err != nil {
		return err
	}
	contentName := f["content"]
	if contentName == "" {
		contentName = name
	}
	data, err := contentBytes(contentName, f["kind"], f["variant"], f["lang"])
	if err != nil {
		return err
	}
	c := s.content()
	sha := c.noteContent(f["content"], data)
	if _, _, err := s.World.HubServer().Blobs().Put(s.ctx, bytes.NewReader(data), sha); err != nil {
		return err
	}
	verified, err := strconv.ParseBool(f["model_verified"])
	if err != nil {
		return fmt.Errorf("model_verified 要是 true 或 false：%q", f["model_verified"])
	}
	score, err := strconv.Atoi(f["score"])
	if err != nil {
		return fmt.Errorf("score 要是整數：%q", f["score"])
	}
	vis := f["visibility"]
	if vis == "" {
		vis = cfg.DefaultVisibility(f["kind"])
	}
	res, err := st.CreateShare(s.ctx, hubstore.NewShare{
		UserID: uid, License: f["license"], BundleSHA256: "seed-" + uuid.NewString(), BundleSize: 0,
		Video: hubstore.Video{Platform: platform, ExternalID: extID, Part: part, Title: "Fixture Video", ChannelExternalID: f["channel"]},
		Blobs: []hubstore.Blob{{SHA256: sha, Size: int64(len(data)), MediaType: "application/json"}},
		Versions: []hubstore.NewVersion{{Version: hubstore.Version{
			Kind: f["kind"], Variant: f["variant"], Lang: f["lang"], Format: formatOf(f["kind"]), SHA256: sha, Size: int64(len(data)),
			Provenance: []byte(`{"executor":"api"}`), TranscribeModel: f["transcribe_model"], CorrectModel: f["correct_model"],
			TranslateModel: f["translate_model"], ModelVerified: verified, Visibility: vis, License: f["license"], Score: score,
		}}},
	})
	if err != nil {
		return err
	}
	v := res.Versions[0]
	if state := f["state"]; state != "visible" {
		if err := st.SetVersionState(s.ctx, v.ID, state); err != nil {
			return err
		}
		if state == "removed" {
			if err := st.AddRemovedHash(s.ctx, v.VideoID, sha, ""); err != nil {
				return err
			}
		}
	}
	c.versions[name] = seededVersion{id: v.ID, sha: sha}
	s.World.SetHubRef("version", name, v.ID)
	return nil
}

func formatOf(kind string) string {
	switch kind {
	case "transcript":
		return "notesteme-cues+json"
	case "note":
		return "markdown"
	}
	return "json"
}

func (s *Scenario) hubVersionHasCues(name string, table *godog.Table) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	sv, ok := s.content().versions[name]
	if !ok {
		return fmt.Errorf("沒有名為 %q 的 Hub 版本（先用 `Given the Hub has the version …`）", name)
	}
	v, err := st.Version(s.ctx, sv.id)
	if err != nil {
		return err
	}
	var cues []subtitle.Cue
	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		if len(row.Cells) != 4 {
			return fmt.Errorf("cues 表格第 %d 列要有 #、start、end、text 四欄", i+1)
		}
		start, err := subtitle.ParseTimecode(row.Cells[1].Value)
		if err != nil {
			return err
		}
		end, err := subtitle.ParseTimecode(row.Cells[2].Value)
		if err != nil {
			return err
		}
		cues = append(cues, subtitle.Cue{Index: len(cues) + 1, StartMS: start, EndMS: end, Text: row.Cells[3].Value, Timing: subtitle.TimingSource})
	}
	data, err := hubCuesJSON(v.Variant, v.Lang, cues)
	if err != nil {
		return err
	}
	sha, _, err := s.World.HubServer().Blobs().Put(s.ctx, bytes.NewReader(data), "")
	if err != nil {
		return err
	}
	if err := st.ReplaceVersionContent(s.ctx, v.ID, hubstore.Blob{SHA256: sha, Size: int64(len(data)), MediaType: "application/json"}); err != nil {
		return err
	}
	s.content().versions[name] = seededVersion{id: v.ID, sha: sha}
	return nil
}

// ---- bundle ----

type bundleRow struct {
	id, kind, variant, lang string
	content                 []byte
	prov                    map[string]any
	parents                 []string // <variant>:<lang>
	hubOrigin               string
}

type hubBundle struct {
	ref    string
	rows   []*bundleRow
	fields [][2]string
	unsafe []string
}

func newArtifactID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id.String()
}

func (s *Scenario) aBundle(name, ref string, table *godog.Table) error {
	if len(table.Rows) < 2 {
		return errors.New("產出表格至少要有標題列與一列")
	}
	head := []string{}
	for _, c := range table.Rows[0].Cells {
		head = append(head, c.Value)
	}
	if _, _, _, err := hubVideo(ref); err != nil {
		return err
	}
	b := &hubBundle{ref: ref}
	c := s.content()
	for i, row := range table.Rows[1:] {
		f := map[string]string{}
		for j, cell := range row.Cells {
			if v := strings.TrimSpace(cell.Value); v != "" && v != absent {
				f[head[j]] = v
			}
		}
		r := &bundleRow{id: newArtifactID(), kind: f["kind"], variant: f["variant"], lang: f["lang"], hubOrigin: f["hub_origin"]}
		contentName := f["content"]
		if contentName == "" {
			contentName = fmt.Sprintf("%s-%d", name, i+1)
		}
		data, err := contentBytes(contentName, r.kind, r.variant, r.lang)
		if err != nil {
			return err
		}
		r.content = data
		c.noteContent(f["content"], data)
		exec := f["executor"]
		if exec == "" {
			exec = "api"
		}
		r.prov = map[string]any{"executor": exec, "created_at": "2026-12-01T00:00:00Z"}
		if v := f["provider"]; v != "" {
			r.prov["provider"] = v
		}
		if v := f["model"]; v != "" {
			if exec == "agent" {
				r.prov["model_reported"] = v
			} else {
				r.prov["model"] = v
			}
		}
		if v := f["source"]; v != "" {
			r.prov["source"] = v
		}
		for _, k := range []string{"model_verified", "cookie_required"} {
			if v := f[k]; v != "" {
				bv, err := strconv.ParseBool(v)
				if err != nil {
					return fmt.Errorf("%s 要是 true 或 false：%q", k, v)
				}
				r.prov[k] = bv
			}
		}
		if v := f["parents"]; v != "" {
			for _, p := range strings.Split(v, ",") {
				r.parents = append(r.parents, strings.TrimSpace(p))
			}
		}
		b.rows = append(b.rows, r)
	}
	c.bundles[name] = b
	return nil
}

func (s *Scenario) bundleOf(name string) (*hubBundle, error) {
	b, ok := s.content().bundles[name]
	if !ok {
		return nil, fmt.Errorf("沒有名為 %q 的 bundle（先用 `Given a bundle … of … with the artifacts:`）", name)
	}
	return b, nil
}

func (s *Scenario) bundleManifestFields(name string, table *godog.Table) error {
	b, err := s.bundleOf(name)
	if err != nil {
		return err
	}
	rows, err := fieldRows(table)
	if err != nil {
		return err
	}
	b.fields = append(b.fields, rows...)
	return nil
}

func (s *Scenario) bundleNote(name string, doc *godog.DocString) error {
	b, err := s.bundleOf(name)
	if err != nil {
		return err
	}
	b.rows = append(b.rows, &bundleRow{id: newArtifactID(), kind: "note", lang: "en", content: []byte(doc.Content),
		prov: map[string]any{"executor": "api", "created_at": "2026-12-01T00:00:00Z"}})
	return nil
}

var unsafeKinds = []string{"an absolute path", "a parent directory entry", "a symlink", "a sha256 mismatch", "a media file"}

func (s *Scenario) bundleUnsafe(name, problem string) error {
	b, err := s.bundleOf(name)
	if err != nil {
		return err
	}
	if !slices.Contains(unsafeKinds, problem) {
		return fmt.Errorf("不認得的問題 %q（%s）", problem, strings.Join(unsafeKinds, "、"))
	}
	b.unsafe = append(b.unsafe, problem)
	return nil
}

func extOf(kind string) string {
	if kind == "note" {
		return ".md"
	}
	return ".json"
}

func shaHex(d []byte) string {
	sum := sha256.Sum256(d)
	return hex.EncodeToString(sum[:])
}

// setPath 依點號路徑設定（value 為 nil 時刪除）manifest 的欄位；陣列以數字索引，中間的物件不存在時建立。
func setPath(root map[string]any, path string, value any, del bool) error {
	parts := strings.Split(path, ".")
	var cur any = root
	for i, p := range parts {
		last := i == len(parts)-1
		switch c := cur.(type) {
		case map[string]any:
			if last {
				if del {
					delete(c, p)
				} else {
					c[p] = value
				}
				return nil
			}
			next, ok := c[p]
			if !ok || next == nil {
				next = map[string]any{}
				c[p] = next
			}
			cur = next
		case []any:
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n >= len(c) {
				return fmt.Errorf("manifest 的路徑 %q：%q 不是有效的索引", path, p)
			}
			if last {
				if del {
					return fmt.Errorf("manifest 的路徑 %q：不能刪除陣列的元素", path)
				}
				c[n] = value
				return nil
			}
			cur = c[n]
		default:
			return fmt.Errorf("manifest 的路徑 %q：%q 不是物件或陣列", path, p)
		}
	}
	return nil
}

// build 產生 bundle 的 zip（steps.md 4.31）：manifest、artifacts/<id>.<ext>，再套用 unsafe 的改動。
func (s *Scenario) buildBundle(b *hubBundle) ([]byte, error) {
	platform, extID, part, err := hubVideo(b.ref)
	if err != nil {
		return nil, err
	}
	item := map[string]any{"platform": platform, "external_id": extID, "part": nil, "title": "Fixture Video",
		"availability": "public", "original_lang": "en"}
	if part > 0 {
		item["part"] = part
	}
	if platform == source.PlatformLocal {
		item["external_id"] = shaHex([]byte(extID))
		item["source_name"] = extID
	}
	type file struct {
		path string
		data []byte
	}
	var files []file
	var artifacts []any
	c := s.content()
	for _, r := range b.rows {
		path := "artifacts/" + r.id + extOf(r.kind)
		prov := map[string]any{}
		for k, v := range r.prov {
			prov[k] = v
		}
		if len(r.parents) > 0 {
			shas := []any{}
			for _, p := range r.parents {
				variant, lang, _ := strings.Cut(p, ":")
				found := false
				for _, o := range b.rows {
					if o != r && o.variant == variant && o.lang == lang {
						shas = append(shas, shaHex(o.content))
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("parents %q 不是同一個 bundle 中的產出", p)
				}
			}
			prov["parents_sha256"] = shas
		}
		if r.hubOrigin != "" {
			sv, ok := c.versions[r.hubOrigin]
			if !ok {
				return nil, fmt.Errorf("hub_origin %q 不是 seeding 的 Hub 版本", r.hubOrigin)
			}
			prov["hub_origin"] = map[string]any{"version_id": sv.id, "sha256": sv.sha}
		}
		a := map[string]any{"id": r.id, "kind": r.kind, "format": formatOf(r.kind), "path": path, "parents": []any{}, "provenance": prov}
		if r.kind == "transcript" {
			a["variant"] = r.variant
		}
		if r.lang != "" {
			a["lang"] = r.lang
		}
		artifacts = append(artifacts, a)
		files = append(files, file{path: path, data: r.content})
	}
	if slices.Contains(b.unsafe, "a media file") {
		media := append([]byte("\x00\x00\x00\x18ftypmp42"), bytes.Repeat([]byte{0}, 64)...)
		files = append(files, file{path: "artifacts/" + newArtifactID() + ".mp4", data: media})
	}
	list := []any{}
	for _, f := range files {
		list = append(list, map[string]any{"path": f.path, "sha256": shaHex(f.data), "size": len(f.data)})
	}
	manifest := map[string]any{"bundle_version": 0, "item": item, "artifacts": artifacts, "files": list,
		"created_by": map[string]any{"app_version": "0.5.0"}, "created_at": "2026-12-01T00:00:00Z", "license": "CC-BY-4.0"}
	if artifacts == nil {
		manifest["artifacts"] = []any{}
	}
	for _, kv := range b.fields {
		if kv[1] == absent {
			err = setPath(manifest, kv[0], nil, true)
		} else {
			err = setPath(manifest, kv[0], tableValue(kv[1]), false)
		}
		if err != nil {
			return nil, err
		}
	}
	mj, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if slices.Contains(b.unsafe, "a sha256 mismatch") && len(files) > 0 {
		files[0].data = append(append([]byte(nil), files[0].data...), ' ')
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, data []byte, mode fs.FileMode) error {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := put("manifest.json", mj, 0o644); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := put(f.path, f.data, 0o644); err != nil {
			return nil, err
		}
	}
	extra := map[string]func() error{
		"an absolute path":         func() error { return put("/etc/notesteme", []byte("x"), 0o644) },
		"a parent directory entry": func() error { return put("artifacts/../x", []byte("x"), 0o644) },
		"a symlink":                func() error { return put("artifacts/link", []byte("/etc/passwd"), fs.ModeSymlink|0o777) },
	}
	for _, u := range b.unsafe {
		if fn, ok := extra[u]; ok {
			if err := fn(); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sharesBundle 以 session 送出 POST /api/v1/shares（multipart：bundle、attest=true，表格可以覆寫或省略欄位）。
func (s *Scenario) sharesBundle(who, name string, table *godog.Table) error {
	b, err := s.bundleOf(name)
	if err != nil {
		return err
	}
	data, err := s.buildBundle(b)
	if err != nil {
		return err
	}
	// 產生的 bundle 留在場景目錄（除錯用，也讓 After hook 的 secret 掃描涵蓋它）。
	dir := filepath.Join(s.World.Root, "bundles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".notesteme.zip"), data, 0o600); err != nil {
		return err
	}
	fields := map[string]string{"attest": "true"}
	order := []string{"attest"}
	if table != nil {
		rows, err := fieldRows(table)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if !slices.Contains(order, r[0]) {
				order = append(order, r[0])
			}
			fields[r[0]] = r[1]
		}
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="bundle"; filename=%q`, name+".notesteme.zip"))
	h.Set("Content-Type", "application/zip")
	pw, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := pw.Write(data); err != nil {
		return err
	}
	for _, k := range order {
		if v := fields[k]; v != absent {
			if err := mw.WriteField(k, v); err != nil {
				return err
			}
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}
	jar := s.memberCookies(who)
	if jar[hubauth.SessionCookie] == "" {
		return fmt.Errorf("Hub 使用者 %q 還沒有登入（先用 `Given the hub member … signed in with …`）", who)
	}
	resp, err := s.hubDo(hubRequest{method: http.MethodPost, target: "/api/v1/shares", body: body.Bytes(),
		contentType: mw.FormDataContentType(), cookies: jar, csrf: true})
	if err != nil {
		return err
	}
	if resp.status == http.StatusCreated {
		var out struct {
			ShareID string `json:"share_id"`
		}
		if json.Unmarshal(resp.body, &out) == nil && out.ShareID != "" {
			s.World.SetHubRef("share", "last", out.ShareID)
		}
	}
	return nil
}

// ---- 唯讀查詢 ----

func (s *Scenario) videoVersions(st hubstore.Store, ref string) ([]hubstore.Version, error) {
	platform, extID, part, err := hubVideo(ref)
	if err != nil {
		return nil, err
	}
	v, err := st.VideoByKey(s.ctx, platform, extID, part)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return st.VideoVersions(s.ctx, v.ID)
}

func (s *Scenario) hubVersionKnownAs(ref, key, name string) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return fmt.Errorf("版本的鍵要是 <kind>:<variant>:<lang>：%q", key)
	}
	vs, err := s.videoVersions(st, ref)
	if err != nil {
		return err
	}
	var match []hubstore.Version
	for _, v := range vs {
		if v.Kind == parts[0] && v.Variant == parts[1] && v.Lang == parts[2] {
			match = append(match, v)
		}
	}
	if len(match) != 1 {
		return fmt.Errorf("%s 的 %s 版本有 %d 個，預期剛好一個", ref, key, len(match))
	}
	s.content().versions[name] = seededVersion{id: match[0].ID, sha: match[0].SHA256}
	s.World.SetHubRef("version", name, match[0].ID)
	return nil
}

func (s *Scenario) hubHasVersions(n int, ref string) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	vs, err := s.videoVersions(st, ref)
	if err != nil {
		return err
	}
	if len(vs) != n {
		return fmt.Errorf("Hub 上 %s 有 %d 個版本，預期 %d", ref, len(vs), n)
	}
	return nil
}

func (s *Scenario) namedVersion(st hubstore.Store, name string) (hubstore.Version, error) {
	sv, ok := s.content().versions[name]
	if !ok {
		return hubstore.Version{}, fmt.Errorf("沒有名為 %q 的 Hub 版本", name)
	}
	return st.Version(s.ctx, sv.id)
}

func (s *Scenario) hubVersionIs(name, state string) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	v, err := s.namedVersion(st, name)
	if err != nil {
		return err
	}
	if v.State != state {
		return fmt.Errorf("Hub 版本 %q 的狀態是 %s，預期 %s", name, v.State, state)
	}
	return nil
}

func (s *Scenario) hubVersionDerived(child, parent, relation string) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	c, err := s.namedVersion(st, child)
	if err != nil {
		return err
	}
	p, err := s.namedVersion(st, parent)
	if err != nil {
		return err
	}
	parents, _, err := st.VersionDerivations(s.ctx, c.ID)
	if err != nil {
		return err
	}
	for _, d := range parents {
		if d.ID == p.ID && d.Relation == relation {
			return nil
		}
	}
	return fmt.Errorf("Hub 版本 %q 沒有以 %s 衍生自 %q（parents：%v）", child, relation, parent, parents)
}

func (s *Scenario) hubStoresContentOnce(name string) error {
	st, err := s.hubStore()
	if err != nil {
		return err
	}
	shas := s.content().shas[name]
	if len(shas) == 0 {
		return fmt.Errorf("場景中沒有產生過內容 %q", name)
	}
	for _, sha := range shas {
		files, err := filepath.Glob(filepath.Join(s.World.HubServer().Config().Data, "blobs", "*", sha))
		if err != nil {
			return err
		}
		if len(files) != 1 {
			return fmt.Errorf("內容 %q（%s）在 blob 中有 %d 個檔案，預期 1 個", name, sha[:12], len(files))
		}
		n, err := st.BlobRefs(s.ctx, sha)
		if err != nil {
			return err
		}
		if n < 1 {
			return fmt.Errorf("內容 %q 沒有任何版本引用", name)
		}
	}
	return nil
}
