package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakehub"
	"github.com/cxyfer/notesteme/features/internal/harness"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// registerHubClient 註冊「Client 與 Hub」的步驟（steps.md 4.32，M5）。測試替身是 fakehub（ADR-0030 第 3 點）。
//
// M5-S7／S8（HSHR）：閘門啟動 fakehub；Client 的設定檔在 server 啟動前補上 hub.url，server 啟動後以
// PUT /api/settings/hub 設定 token（不算最近一次回應、不連線到 Hub）。閘門依 @HSHR-R 放行（hshrRules），
// 其他場景（HMCP 等）停在閘門。
func registerHubClient(r *Registry, s *Scenario) {
	r.Step(`the owner's client is connected to a Hub`, s.clientConnectedToHub)
	r.Step(`the client's Hub token has the permissions {string}`, s.clientHubTokenPermissions)
	r.Step(`the client's Hub token is not set`, s.clientHubTokenNotSet)
	r.Step(`the Hub fails with HTTP {int}`, s.fakeHubFails)
	r.Step(`the Hub does not respond`, s.fakeHubHangs)
	r.Step(`the Hub serves tampered content for the version {string}`, s.fakeHubTampers)
	r.Step(`the Hub received {int} requests`, s.fakeHubReceivedRequests)
	r.Step(`the Hub was asked for a lookup {int} times`, s.fakeHubLookups)
	r.Step(`the Hub received a share of {string} with the artifacts:`, s.fakeHubReceivedShare)
	r.Step(`the last bundle sent to the Hub has the manifest fields:`, s.lastBundleManifestFields)
	r.Step(`the last bundle sent to the Hub does not contain {string}`, s.lastBundleDoesNotContain)
	r.Step(`the Hub never received the video ID of {string}`, s.fakeHubNeverReceivedVideoID)
}

// hubClientState 是 4.32 的場景狀態。
type hubClientState struct {
	// tokenUnset 為 true 時 server 啟動後不設定 token（`the client's Hub token is not set`）。
	tokenUnset bool
	// meta 是 fakehub 版本的變體與語言（`… has the cues:` 用）。
	meta map[string][2]string
}

// hshrRules 是已實作的 HSHR 規則（M5-S7、S8）。
var hshrRules = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true,
	9: true, 10: true, 11: true, 12: true, 13: true, 14: true, 15: true}

// hshrGate 只對已實作規則的場景放行：至少一個已實作的 @HSHR-R，而且沒有還沒實作的。
// 其他場景停在閘門（回傳裸的 godog.ErrPending，同 hubGate）。
func (s *Scenario) hshrGate() error {
	ok := false
	for _, t := range s.tags {
		n, found, valid := ruleOf(t, "@HSHR-R")
		if !found {
			continue
		}
		if !valid || !hshrRules[n] {
			return godog.ErrPending
		}
		ok = true
	}
	if !ok {
		return godog.ErrPending
	}
	return nil
}

func (s *Scenario) clientConnectedToHub() error {
	if err := s.hshrGate(); err != nil {
		return err
	}
	return s.World.StartFakeHub()
}

func (s *Scenario) fakeHub() (*fakehub.Server, error) {
	if s.World.HubKind() != harness.HubFake {
		return nil, errors.New("fakehub 沒有在執行（Background 要有 `Given the owner's client is connected to a Hub`）")
	}
	return s.World.FakeHub(), nil
}

// afterServerStart 在 server 啟動之後設定 Hub token（steps.md 4.32 的閘門）：不改變最近一次回應。
func (s *Scenario) afterServerStart() error {
	if s.World.HubKind() != harness.HubFake || s.hub.client.tokenUnset {
		return nil
	}
	return s.putHubToken(harness.TestHubToken)
}

func (s *Scenario) putHubToken(token string) error {
	if err := s.ensureOwner(); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := s.do(http.MethodPut, "/api/settings/hub", requestOpts{who: asOwner, body: body})
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("設定 Hub token：HTTP %d %s", resp.status, snippet(resp.body))
	}
	return nil
}

func (s *Scenario) clientHubTokenPermissions(perms string) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	var list []string
	for _, p := range strings.Split(perms, ",") {
		if p = strings.TrimSpace(p); p != "" {
			list = append(list, p)
		}
	}
	h.SetPermissions(list...)
	return nil
}

func (s *Scenario) clientHubTokenNotSet() error {
	s.hub.client.tokenUnset = true
	if s.server == nil {
		return nil
	}
	return s.putHubToken("")
}

func (s *Scenario) fakeHubFails(status int) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	h.Fail(status)
	return nil
}

func (s *Scenario) fakeHubHangs() error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	h.Hang()
	return nil
}

func (s *Scenario) fakeHubTampers(name string) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	sv, ok := s.content().versions[name]
	if !ok {
		return fmt.Errorf("沒有名為 %q 的 Hub 版本（先用 `Given the Hub has the version …`）", name)
	}
	h.Tamper(sv.id)
	return nil
}

func requestList(h *fakehub.Server) string {
	var out []string
	for _, r := range h.Requests() {
		out = append(out, r.Method+" "+r.Path)
	}
	if len(out) == 0 {
		return "（沒有）"
	}
	return strings.Join(out, "、")
}

func (s *Scenario) fakeHubReceivedRequests(want int) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	if got := len(h.Requests()); got != want {
		return fmt.Errorf("fakehub 收到 %d 個請求，預期 %d 個：%s", got, want, requestList(h))
	}
	return nil
}

func (s *Scenario) fakeHubLookups(want int) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	if got := h.Lookups(); got != want {
		return fmt.Errorf("fakehub 收到 %d 次查詢，預期 %d 次（請求：%s）", got, want, requestList(h))
	}
	return nil
}

func (s *Scenario) lastShare() (fakehub.Share, error) {
	h, err := s.fakeHub()
	if err != nil {
		return fakehub.Share{}, err
	}
	shares := h.Shares()
	if len(shares) == 0 {
		return fakehub.Share{}, fmt.Errorf("fakehub 沒有收到任何分享（請求：%s）", requestList(h))
	}
	return shares[len(shares)-1], nil
}

func (s *Scenario) fakeHubReceivedShare(ref string, table *godog.Table) error {
	sh, err := s.lastShare()
	if err != nil {
		return err
	}
	platform, extID, part, err := hubVideo(ref)
	if err != nil {
		return err
	}
	item, _ := sh.Manifest["item"].(map[string]any)
	gotPart := 0
	if f, ok := item["part"].(float64); ok {
		gotPart = int(f)
	}
	if item["platform"] != platform || item["external_id"] != extID || gotPart != part {
		return fmt.Errorf("最後的分享是 %v:%v:%d，預期 %s", item["platform"], item["external_id"], gotPart, ref)
	}
	var want []string
	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		if len(row.Cells) != 3 {
			return errors.New("表格要有 kind、variant、lang 三欄")
		}
		want = append(want, row.Cells[0].Value+"|"+row.Cells[1].Value+"|"+row.Cells[2].Value)
	}
	var got []string
	arts, _ := sh.Manifest["artifacts"].([]any)
	for _, a := range arts {
		m, _ := a.(map[string]any)
		k, _ := m["kind"].(string)
		v, _ := m["variant"].(string)
		l, _ := m["lang"].(string)
		got = append(got, k+"|"+v+"|"+l)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return fmt.Errorf("bundle 的產出是 %v，預期 %v", got, want)
	}
	return nil
}

// manifestField 依點號路徑取值（數字是陣列索引）。
func manifestField(v any, path string) (any, bool) {
	for _, k := range strings.Split(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			x, ok := t[k]
			if !ok {
				return nil, false
			}
			v = x
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			v = t[i]
		default:
			return nil, false
		}
	}
	return v, true
}

func manifestText(sh fakehub.Share) string {
	b, _ := json.Marshal(sh.Manifest)
	return snippet(b)
}

// lastBundleManifestFields 比對最後一個 bundle 的 manifest：<absent> 代表不存在，* 代表存在即可。
func (s *Scenario) lastBundleManifestFields(table *godog.Table) error {
	sh, err := s.lastShare()
	if err != nil {
		return err
	}
	rows, err := fieldRows(table)
	if err != nil {
		return err
	}
	for _, r := range rows {
		got, ok := manifestField(sh.Manifest, r[0])
		if r[1] == absent {
			if ok {
				return fmt.Errorf("manifest 的 %s 應該不存在，實際是 %v", r[0], got)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("manifest 沒有 %s：%s", r[0], manifestText(sh))
		}
		if r[1] == "*" {
			continue
		}
		var text string
		switch g := got.(type) {
		case string:
			text = g
		case nil:
			text = "null"
		default:
			b, _ := json.Marshal(g)
			text = string(b)
		}
		if text != r[1] {
			return fmt.Errorf("manifest 的 %s 是 %q，預期 %q", r[0], text, r[1])
		}
	}
	return nil
}

func (s *Scenario) lastBundleDoesNotContain(text string) error {
	sh, err := s.lastShare()
	if err != nil {
		return err
	}
	text = s.World.Expand(text)
	for name, data := range sh.Files {
		if bytes.Contains(data, []byte(text)) {
			return fmt.Errorf("bundle 的 %s 含有 %q", name, text)
		}
	}
	if b, _ := json.Marshal(sh.Manifest); bytes.Contains(b, []byte(text)) {
		return fmt.Errorf("bundle 的 manifest 含有 %q", text)
	}
	return nil
}

func (s *Scenario) fakeHubNeverReceivedVideoID(ref string) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	if len(h.Requests()) == 0 {
		return errors.New("fakehub 沒有收到任何請求，沒有東西可以驗證")
	}
	_, extID, _, err := hubVideo(ref)
	if err != nil {
		return err
	}
	if h.SawString(extID) {
		return fmt.Errorf("fakehub 收到的請求含有影片 ID %q（請求：%s）", extID, requestList(h))
	}
	return nil
}

// ---- fakehub 的 seeding（steps.md 4.31 的兩個句型在 fakehub 上）----

func (s *Scenario) fakeHubHasVersion(name, ref string, table *godog.Table) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
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
	if f["state"] != "visible" {
		return fmt.Errorf("fakehub 的版本只有 visible（state %q 只用於真 Hub）", f["state"])
	}
	platform, extID, part, err := hubVideo(ref)
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
	verified, err := strconv.ParseBool(f["model_verified"])
	if err != nil {
		return fmt.Errorf("model_verified 要是 true 或 false：%q", f["model_verified"])
	}
	score, err := strconv.Atoi(f["score"])
	if err != nil {
		return fmt.Errorf("score 要是整數：%q", f["score"])
	}
	partStr := ""
	if part > 0 {
		partStr = strconv.Itoa(part)
	}
	id := h.AddVersion(fakehub.Version{
		Platform: platform, ExternalID: extID, Part: partStr, Kind: f["kind"], Variant: f["variant"], Lang: f["lang"],
		Uploader: f["uploader"], TranscribeModel: f["transcribe_model"], CorrectModel: f["correct_model"],
		TranslateModel: f["translate_model"], ModelVerified: verified, License: f["license"], Score: score, Content: data,
	})
	c := s.content()
	c.versions[name] = seededVersion{id: id, sha: c.noteContent(f["content"], data)}
	if s.hub.client.meta == nil {
		s.hub.client.meta = map[string][2]string{}
	}
	s.hub.client.meta[name] = [2]string{f["variant"], f["lang"]}
	s.World.SetHubRef("version", name, id)
	return nil
}

func (s *Scenario) fakeHubVersionHasCues(name string, table *godog.Table) error {
	h, err := s.fakeHub()
	if err != nil {
		return err
	}
	sv, ok := s.content().versions[name]
	if !ok {
		return fmt.Errorf("沒有名為 %q 的 Hub 版本（先用 `Given the Hub has the version …`）", name)
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
	m := s.hub.client.meta[name]
	data, err := hubCuesJSON(m[0], m[1], cues)
	if err != nil {
		return err
	}
	if err := h.ReplaceContent(sv.id, data); err != nil {
		return err
	}
	s.content().versions[name] = seededVersion{id: sv.id, sha: s.content().noteContent("", data)}
	return nil
}
