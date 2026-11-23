package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakenotify"
)

// registerNotify 註冊「摘要、通知與無人值守」的步驟（steps.md 4.18，M4）。
//
// M4 規格展開時（2026-11-23）只註冊步驟樁；M4-S1 實作 NOTIFY 的句型。NOTIFY 的句型也是 DIG 與 OPS-06～08 的閘門，
// 所以只對 NOTIFY 的場景放行（見 notifyGate），其他領域維持 pending、停在同一個閘門。
// DIG 的句型在 M4-S6 實作（digest.go）；OPS 自己的句型仍是步驟樁，由它的切片實作。
func registerNotify(r *Registry, s *Scenario) {
	r.Step(`the summary profile is:`, s.summaryProfileIs)
	r.Step(`the summary of {string} is:`, s.summaryOfIs)
	r.Step(`the digest {string} is:`, s.digestIs)
	r.Step(`the notification endpoints are reachable`, s.notificationEndpointsReachable)
	r.Step(`the notifier {string} is:`, s.notifierIs)
	r.Step(`the notification endpoint {string} answers HTTP {int} for the next {int} requests`, s.notifyAnswersNext)
	r.Step(`the notification endpoint {string} answers HTTP {int}`, s.notifyAnswersAlways)
	r.Step(`the notification endpoint {string} received {int} requests`, s.notifyReceivedCount)
	r.Step(`the last request to the notification endpoint {string} contains:`, s.notifyLastContains)
	r.Step(`the last request to the notification endpoint {string} has the header {string} containing {string}`, s.notifyLastHeader)
	r.Step(`the last request to the notification endpoint {string} is signed with {string}`, s.notifyLastSigned)
	r.Step(`the notification endpoint {string} received a request containing {string}`, s.notifyReceivedContaining)
	r.Step(`no request to a notification endpoint contains {string}`, s.noNotifyRequestContains)
	r.Step(`the backup directory {string} contains:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the backup directory {string} contains exactly:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`the data volume is {int} percent full`, func(int) error { return godog.ErrPending })
	r.Step(`the cookie for {string} was last used successfully at {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`yt-dlp recorded {int} failures out of {int} calls in the last hour`, func(int, int) error { return godog.ErrPending })
	r.Step(`the instance is deployed with the {string} profile`, func(string) error { return godog.ErrPending })
	r.Step(`the server starts with the flag {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the compose .env file contains:`, func(*godog.Table) error { return godog.ErrPending })
}

// notifyGate 讓 NOTIFY 的句型只對 NOTIFY 的場景（有 @NOTIFY- 開頭的 tag）放行（M4-S1）；M4-S6 起也對 DIG 的場景放行（digGate）。
// DIG 的彙整與 OPS-06～08 的告警共用這些句型，在它們的切片實作之前維持 pending；回傳裸的 godog.ErrPending，
// junit 的訊息與切片之前相同（同 M5-S1 的 hubGate）。
func (s *Scenario) notifyGate() error {
	if !slices.ContainsFunc(s.tags, func(t string) bool { return strings.HasPrefix(t, "@NOTIFY-") }) && !s.digGate() {
		return godog.ErrPending
	}
	return nil
}

// pendingNotifier 是 `the notifier … is:` 累積的 notifier（第一個送出請求的步驟才寫入，見 flushNotifiers）。
type pendingNotifier struct {
	id   string
	body map[string]any
}

func (s *Scenario) notificationEndpointsReachable() error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	_, err := s.World.ReachNotifyEndpoints()
	return err
}

// notifierIs 累積一個 notifier：doc string 是 POST /api/notifiers 的 JSON（不含 id）。
func (s *Scenario) notifierIs(id string, doc *godog.DocString) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(doc.Content), &body); err != nil {
		return fmt.Errorf("notifier %s 的 JSON 不正確：%w", id, err)
	}
	if _, ok := body["id"]; ok {
		return fmt.Errorf("notifier %s：doc string 不含 id（id 是參數）", id)
	}
	body["id"] = id
	s.notifiers = append(s.notifiers, pendingNotifier{id: id, body: body})
	return nil
}

// flushNotifiers 依宣告順序寫入累積的 notifier（steps.md 4.18：接在佇列之後）；必須回 201，501 時 pending。
func (s *Scenario) flushNotifiers() error {
	pending := s.notifiers
	s.notifiers = nil
	for _, n := range pending {
		if _, err := s.configWrite(http.MethodPost, "/api/notifiers", n.body, http.StatusCreated, "建立 notifier "+n.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scenario) notifyAnswersNext(endpoint string, status, n int) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	if fakenotify.EndpointOf(endpoint) == "" || n < 1 {
		return fmt.Errorf("通知端點 %q 或次數 %d 不正確", endpoint, n)
	}
	s.World.Notify.FailNext(endpoint, status, n)
	return nil
}

func (s *Scenario) notifyAnswersAlways(endpoint string, status int) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	if fakenotify.EndpointOf(endpoint) == "" {
		return fmt.Errorf("通知端點 %q 不正確", endpoint)
	}
	s.World.Notify.FailAlways(endpoint, status)
	return nil
}

func (s *Scenario) notifyReceivedCount(endpoint string, n int) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	if got := len(s.World.Notify.RequestsTo(endpoint)); got != n {
		return fmt.Errorf("通知端點 %s 收到 %d 個請求，預期 %d 個%s", endpoint, got, n, s.notifyHint())
	}
	return nil
}

// notifyHint 在失敗訊息中附上 fakenotify 收到的請求（主機與路徑）與 outbox 的狀態，方便除錯。
func (s *Scenario) notifyHint() string {
	var eps []string
	for _, r := range s.World.Notify.Requests() {
		eps = append(eps, r.Endpoint())
	}
	return fmt.Sprintf("（收到的請求：%v）", eps)
}

func (s *Scenario) lastNotifyRequest(endpoint string) (fakenotify.Request, error) {
	r, ok := s.World.Notify.Last(endpoint)
	if !ok {
		return fakenotify.Request{}, fmt.Errorf("通知端點 %s 沒有收到請求%s", endpoint, s.notifyHint())
	}
	return r, nil
}

func (s *Scenario) notifyLastContains(endpoint string, doc *godog.DocString) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	r, err := s.lastNotifyRequest(endpoint)
	if err != nil {
		return err
	}
	if !json.Valid(r.Body) {
		return fmt.Errorf("通知端點 %s 最近一次請求的 body 不是 JSON：%s", endpoint, snippet(r.Body))
	}
	if err := jsonSubset([]byte(doc.Content), r.Body); err != nil {
		return fmt.Errorf("%w\n請求內容：%s", err, snippet(r.Body))
	}
	return nil
}

func (s *Scenario) notifyLastHeader(endpoint, name, want string) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	r, err := s.lastNotifyRequest(endpoint)
	if err != nil {
		return err
	}
	values := r.Header.Values(name)
	if len(values) == 0 {
		return fmt.Errorf("通知端點 %s 最近一次請求沒有 %s 標頭", endpoint, http.CanonicalHeaderKey(name))
	}
	if v := strings.Join(values, ","); !strings.Contains(v, want) {
		return fmt.Errorf("通知端點 %s 最近一次請求的 %s 是 %q，不含 %q", endpoint, http.CanonicalHeaderKey(name), v, want)
	}
	return nil
}

// notifyLastSigned 由 fakenotify 獨立驗證簽章，並確認時間戳等於請求抵達時 fake clock 的時間（steps.md 4.18）。
func (s *Scenario) notifyLastSigned(endpoint, secret string) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	all := s.World.Notify.Requests()
	key := fakenotify.EndpointOf(endpoint)
	idx := -1
	for i, r := range all {
		if r.Endpoint() == key {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("通知端點 %s 沒有收到請求%s", endpoint, s.notifyHint())
	}
	ts, ok := fakenotify.VerifySignature(all[idx], secret)
	if !ok {
		return fmt.Errorf("通知端點 %s 最近一次請求的簽章不符（X-Notesteme-Timestamp=%q、X-Notesteme-Signature=%q）", endpoint,
			all[idx].Header.Get("X-Notesteme-Timestamp"), all[idx].Header.Get("X-Notesteme-Signature"))
	}
	at, ok := s.World.NotifyReceivedAt(idx)
	if !ok {
		return errors.New("找不到請求抵達的時間")
	}
	if at.Unix() != ts {
		return fmt.Errorf("X-Notesteme-Timestamp 是 %d，送出時的時間是 %s（%s）", ts, strconv.FormatInt(at.Unix(), 10), at.UTC().Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

// bodyContains 回報請求的原始 body 或 JSON 解碼後的任何字串值是否包含 text。
func bodyContains(r fakenotify.Request, text string) bool {
	if strings.Contains(string(r.Body), text) {
		return true
	}
	var v any
	if json.Unmarshal(r.Body, &v) != nil {
		return false
	}
	return jsonStringsContain(v, text)
}

func jsonStringsContain(v any, text string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, text)
	case []any:
		return slices.ContainsFunc(x, func(e any) bool { return jsonStringsContain(e, text) })
	case map[string]any:
		for _, e := range x {
			if jsonStringsContain(e, text) {
				return true
			}
		}
	}
	return false
}

func (s *Scenario) notifyReceivedContaining(endpoint, text string) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	reqs := s.World.Notify.RequestsTo(endpoint)
	for _, r := range reqs {
		if bodyContains(r, text) {
			return nil
		}
	}
	var bodies []string
	for _, r := range reqs {
		bodies = append(bodies, snippet(r.Body))
	}
	return fmt.Errorf("通知端點 %s 的 %d 個請求都不含 %q：%v", endpoint, len(reqs), text, bodies)
}

func (s *Scenario) noNotifyRequestContains(text string) error {
	if err := s.notifyGate(); err != nil {
		return err
	}
	reqs := s.World.Notify.Requests()
	if len(reqs) == 0 {
		return errors.New("通知端點沒有收到任何請求，沒有東西可以驗證")
	}
	for _, r := range reqs {
		if strings.Contains(r.Text(), text) {
			return fmt.Errorf("送往 %s 的請求含有 %q", (&url.URL{Host: r.Host, Path: r.Path}).String(), text)
		}
	}
	return nil
}
