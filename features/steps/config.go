package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/features/internal/fakepodcast"
	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// registerConfig 註冊「Prompt 與處理方案」的步驟，句型見 docs/specs/steps.md。
func registerConfig(r *Registry, s *Scenario) {
	r.Step(`the prompt {string} version {int} is:`, s.promptVersionIs)
	// 沒有任何 feature 用到，語意也不清楚（steps.md 4.11）：維持 pending。
	r.Step(`the default prompt for {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the profile {string} is:`, s.profileIs)
	r.Step(`the default profile is {string}`, s.defaultProfileIs)
	r.Step(`the global settings are:`, s.globalSettingsAre)
	r.Step(`the channel of {string} has the overrides:`, s.channelHasOverrides)
	r.Step(`the effective config of {string} for {string} contains:`, s.effectiveConfigContains)
}

// pendingConfig 是 Given 累積、尚未寫入的設定；各 slice 保留宣告的順序（steps.md 4.11）。
type pendingConfig struct {
	pendingPrompts        []pendingPrompt
	pendingProfiles       []pendingProfile
	pendingDefaultProfile string // 空字串：沒有宣告；多次宣告時以最後一次為準
	pendingChannels       []pendingChannel
}

type pendingPrompt struct {
	id, kind, body string
	version        int
}

type pendingProfile struct {
	name   string
	config map[string]any
}

type pendingChannel struct {
	ref       string
	overrides map[string]any
}

// hasPendingConfig 回報是否有尚未寫入的 prompt、處理方案或頻道覆寫。
func (s *Scenario) hasPendingConfig() bool {
	c := s.pendingConfig
	return len(c.pendingPrompts) > 0 || len(c.pendingProfiles) > 0 || c.pendingDefaultProfile != "" || len(c.pendingChannels) > 0
}

// promptKinds 是 prompt 的類型；id 依 `-`、`.` 切開，第一個出現的類型就是它的 kind（localize 算 translate）。
var promptKinds = map[string]string{
	"summary": "summary", "chapters": "chapters", "segment": "segment", "correct": "correct",
	"translate": "translate", "localize": "translate", "note": "note",
}

// promptKindFromID 從 prompt id 推斷 kind；推斷不出來時回傳錯誤（不用 pending：這是場景寫法的問題）。
func promptKindFromID(id string) (string, error) {
	for _, part := range strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '.' }) {
		if kind, ok := promptKinds[part]; ok {
			return kind, nil
		}
	}
	return "", fmt.Errorf("prompt %q 的 id 推斷不出類型：id 以 - 或 . 切開後要有 summary、chapters、segment、correct、translate、localize、note 其中之一", id)
}

// promptVersionIs 累積 prompt 的一個版本（doc string 是 body）。
func (s *Scenario) promptVersionIs(id string, version int, doc *godog.DocString) error {
	kind, err := promptKindFromID(id)
	if err != nil {
		return err
	}
	if version < 1 {
		return fmt.Errorf("prompt %q 的版本 %d 不正確，至少是 1", id, version)
	}
	s.pendingPrompts = append(s.pendingPrompts, pendingPrompt{id: id, kind: kind, body: doc.Content, version: version})
	return nil
}

// profileIs 累積處理方案（YAML）。
func (s *Scenario) profileIs(name string, doc *godog.DocString) error {
	cfg, err := s.yamlMap(doc.Content, "處理方案 "+name)
	if err != nil {
		return err
	}
	s.pendingProfiles = append(s.pendingProfiles, pendingProfile{name: name, config: cfg})
	return nil
}

// defaultProfileIs 累積預設處理方案。
func (s *Scenario) defaultProfileIs(name string) error {
	if name == "" {
		return fmt.Errorf("預設處理方案的名稱不能是空的")
	}
	s.pendingDefaultProfile = name
	return nil
}

// channelHasOverrides 累積頻道層的覆寫（YAML）；頻道的 natural key 到寫入時才從 fake 平台算出。
func (s *Scenario) channelHasOverrides(ref string, doc *godog.DocString) error {
	if _, err := s.channelKeyFor(ref); err != nil {
		return err
	}
	overrides, err := s.yamlMap(doc.Content, "頻道 "+ref+" 的覆寫")
	if err != nil {
		return err
	}
	s.pendingChannels = append(s.pendingChannels, pendingChannel{ref: ref, overrides: overrides})
	return nil
}

// yamlMap 把 YAML（先換掉虛擬路徑）解成物件；空內容是空物件。
func (s *Scenario) yamlMap(content, what string) (map[string]any, error) {
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(s.World.Expand(content)), &m); err != nil {
		return nil, fmt.Errorf("%s不是合法的 YAML：%w", what, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// channelKeyFor 回傳影音所屬頻道的 natural key：<platform>:<external_id>，與 production 在 metadata 完成時
// 登記的頻道一致。local: 沒有頻道，回傳錯誤。
//   - yt:  youtube:<fake 影片的 channel_id>，沒有登記就是預設頻道（X4）。
//   - bili: bilibili:<uploader_id>，規則與 fake yt-dlp 相同（faketool.UploaderIDFor）。
//   - pod:  podcast:<feed>。
func (s *Scenario) channelKeyFor(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "yt:"):
		id, err := youtubeID(ref)
		if err != nil {
			return "", err
		}
		if v := s.World.Tools.Videos[id]; v != nil && v.ChannelID != "" {
			return "youtube:" + v.ChannelID, nil
		}
		return "youtube:" + DefaultChannelID, nil
	case strings.HasPrefix(ref, "bili:"):
		m := biliRef.FindStringSubmatch(ref)
		if m == nil {
			return "", fmt.Errorf("%q 不是 Bilibili 的代號（bili:<bvid>/p<n>）", ref)
		}
		return "bilibili:" + faketool.UploaderIDFor(s.World.Tools.Videos[m[1]], m[1]), nil
	case strings.HasPrefix(ref, "pod:"):
		feed, _, ok := fakepodcast.SplitRef(ref)
		if !ok {
			return "", fmt.Errorf("%q 不是 Podcast 的代號（pod:<feed>/<guid>）", ref)
		}
		return "podcast:" + feed, nil
	case strings.HasPrefix(ref, "local:"):
		return "", fmt.Errorf("%q 是本機影音，沒有頻道，不能設定頻道的覆寫", ref)
	}
	return "", fmt.Errorf("%q 不是影音的代號，推不出頻道", ref)
}

// configWriter 送出一個設定寫入的請求並檢查回應；測試可以換成記錄呼叫的版本。
type configWriter func(method, path string, body any, want int, what string) (*response, error)

// configWrite 以 owner 的身分送出設定寫入的請求（不覆寫 s.last），再依 checkConfigResponse 判斷結果。
func (s *Scenario) configWrite(method, path string, body any, want int, what string) (*response, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("%s：%w", what, err)
		}
	}
	resp, err := s.do(method, path, requestOpts{who: asOwner, body: raw})
	if err != nil {
		return nil, err
	}
	return checkConfigResponse(resp, method, path, want, what)
}

// checkConfigResponse 回應 501 not_implemented 時轉成 pending（路由屬於還沒做的切片）；
// 其他狀態碼不是 want 時失敗，訊息附上回應內容。
func checkConfigResponse(resp *response, method, path string, want int, what string) (*response, error) {
	switch {
	case resp.status == http.StatusNotImplemented && isNotImplemented(resp.body):
		return nil, fmt.Errorf("%s：%s %s 尚未實作：%w", what, method, path, godog.ErrPending)
	case resp.status != want:
		return nil, fmt.Errorf("%s失敗：%s %s 回應 HTTP %d（預期 %d）%s", what, method, path, resp.status, want, snippet(resp.body))
	}
	return resp, nil
}

// flushConfig 依序寫入累積的設定：prompt → 處理方案 → 預設處理方案 → 頻道覆寫（steps.md 4.11）。
// 用 s.do 而不是 s.send：這些請求不覆寫 s.last，previousRequestNotImplemented 才不會被打亂。
func (s *Scenario) flushConfig() error { return s.flushConfigWith(s.configWrite) }

// flushConfigWith 是 flushConfig 的本體；write 負責實際送出請求。
func (s *Scenario) flushConfigWith(write configWriter) error {
	if !s.hasPendingConfig() {
		return nil
	}
	c := s.pendingConfig
	s.pendingConfig = pendingConfig{}

	for _, p := range c.pendingPrompts {
		what := fmt.Sprintf("建立 prompt %s 的第 %d 版", p.id, p.version)
		var resp *response
		var err error
		if p.version == 1 {
			resp, err = write(http.MethodPost, "/api/prompts", map[string]any{"id": p.id, "kind": p.kind, "body": p.body}, http.StatusCreated, what)
		} else {
			resp, err = write(http.MethodPost, "/api/prompts/"+url.PathEscape(p.id)+"/versions", map[string]any{"body": p.body}, http.StatusCreated, what)
		}
		if err != nil {
			return err
		}
		var got struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(resp.body, &got); err != nil || got.Version != p.version {
			return fmt.Errorf("%s：回應的 version 應該是 %d：%s", what, p.version, snippet(resp.body))
		}
	}
	for _, p := range c.pendingProfiles {
		body := map[string]any{"name": p.name, "config": p.config}
		if _, err := write(http.MethodPost, "/api/profiles", body, http.StatusCreated, "建立處理方案 "+p.name); err != nil {
			return err
		}
	}
	if name := c.pendingDefaultProfile; name != "" {
		if _, err := write(http.MethodPost, "/api/profiles/"+url.PathEscape(name)+"/default", nil, http.StatusOK, "設定預設處理方案 "+name); err != nil {
			return err
		}
	}
	for _, ch := range c.pendingChannels {
		key, err := s.channelKeyFor(ch.ref)
		if err != nil {
			return err
		}
		if _, err := write(http.MethodPatch, "/api/channels/"+url.PathEscape(key)+"/overrides", ch.overrides, http.StatusOK, "設定頻道 "+key+" 的覆寫"); err != nil {
			return err
		}
	}
	return nil
}

// effectiveConfigContains 讀取影音某個步驟最新一筆 stage run 綁定的快照（app.Server.StageConfig），
// 以 YAML 子集比對：物件是子集，陣列必須完全相等（steps.md 4.11）。
func (s *Scenario) effectiveConfigContains(ref, stage string, doc *godog.DocString) error {
	want, err := s.yamlMap(doc.Content, "預期的生效設定")
	if err != nil {
		return err
	}
	if len(want) == 0 {
		// 空的預期內容是任何設定的子集，步驟會空洞通過：視為場景寫錯。
		return fmt.Errorf("預期的生效設定是空的：至少要寫一個欄位")
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if s.stopped {
		return errServerStopped
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	if s.server == nil {
		return errServerStopped
	}
	cfg, _, err := s.server.StageConfig(s.ctx, id, stage)
	if err != nil {
		return pendingIfNotImplemented(fmt.Errorf("%s 的 %q 步驟生效設定：%w", ref, stage, err))
	}
	if err := configSubset(wantJSON, cfg); err != nil {
		return fmt.Errorf("%s 的 %q 步驟生效設定不符：%w\n生效設定：%s", ref, stage, err, snippet(cfg))
	}
	return nil
}

// globalSettingsAre 累積全域設定（YAML，頂層是區段名稱）；第一個送出請求的步驟才寫入（見 flushSettings）。
func (s *Scenario) globalSettingsAre(doc *godog.DocString) error {
	var sections map[string]any
	if err := yaml.Unmarshal([]byte(s.World.Expand(doc.Content)), &sections); err != nil {
		return fmt.Errorf("全域設定不是合法的 YAML：%w", err)
	}
	if s.givenSettings == nil {
		s.givenSettings = map[string]any{}
	}
	for k, v := range sections {
		s.givenSettings[k] = v
	}
	return nil
}

// flushSettings 對每個區段呼叫 PUT /api/settings/{section}。
// 還沒實作的區段（501）讓步驟成為 pending：這個 Given 無法成立，場景不能繼續。
func (s *Scenario) flushSettings() error {
	if len(s.givenSettings) == 0 {
		return nil
	}
	pending := s.givenSettings
	s.givenSettings = nil
	names := make([]string, 0, len(pending))
	for k := range pending {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := json.Marshal(pending[name])
		if err != nil {
			return fmt.Errorf("全域設定 %s：%w", name, err)
		}
		resp, err := s.do(http.MethodPut, "/api/settings/"+name, requestOpts{who: asOwner, body: body})
		if err != nil {
			return err
		}
		switch {
		case resp.status == http.StatusNotImplemented && isNotImplemented(resp.body):
			return godog.ErrPending
		case resp.status != http.StatusOK:
			return fmt.Errorf("設定全域設定 %s 失敗：HTTP %d %s", name, resp.status, snippet(resp.body))
		}
	}
	return nil
}
