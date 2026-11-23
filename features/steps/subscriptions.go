package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakefeed"
	"github.com/cxyfer/notesteme/features/internal/fakepodcast"
	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// registerSubscriptions 註冊「訂閱、事後的更好來源與新增入口」的步驟（steps.md 4.17，M4）。
//
// M4-S2 實作了訂閱核心的句型，M4-S3 實作了匯入（SUB-R7）；SUB 的閘門只對 SUB 範圍的場景放行（見 subGate）。
// SRC-10 的句型在 better_source.go；ENTRY 的句型在 entry.go。
func registerSubscriptions(r *Registry, s *Scenario) {
	r.Step(`the YouTube feeds are served from the registered videos`, s.youtubeFeedsServed)
	r.Step(`the YouTube feed of the channel {string} fails with HTTP {int}`, s.youtubeFeedFails)
	r.Step(`the YouTube feed of the channel {string} recovers`, s.youtubeFeedRecovers)
	r.Step(`the YouTube feed of the channel {string} was requested {int} times`, s.youtubeFeedRequested)
	r.Step(`the subscription {string} for {string}`, func(alias, u string) error { return s.subscriptionFor(alias, u, nil) })
	r.Step(`the subscription {string} for {string} with:`, s.subscriptionFor)
	r.Step(`the subscription {string} has the entries:`, s.subscriptionHasEntries)
	r.Step(`the subscription {string} has no entries`, func(alias string) error { return s.subscriptionHasEntries(alias, nil) })
	r.Step(`the next check of the subscription {string} is at {string}`, s.nextCheckAt)
	r.Step(`the next check of the subscription {string} is between {string} and {string}`, s.nextCheckBetween)
	r.Step(`the number of subscriptions is {int}`, s.numberOfSubscriptions)
	r.Step(`the podcast feed {string} adds the episode {string} titled {string} with the audio {string}`, s.podcastFeedAdds)
	r.Step(`the owner imports the subscriptions from {string}`, s.ownerImportsSubscriptions)
	r.Step(`the owner imports a generated OPML file with {int} feeds`, s.ownerImportsGeneratedOPML)
	r.Step(`the platform adds {word} captions in {string} to {string} at {string}`, s.platformAddsCaptions)
	registerEntry(r, s)
}

// subCoreRules 是已實作的 SUB 規則（沿用 M4-S2 的名稱）：M4-S2 的訂閱核心，加上 M4-S3 的 R6 試跑預覽、R7 匯入與 R13 的 MCP、CLI（SUB 全部的規則）。
var subCoreRules = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true, 11: true, 12: true, 13: true}

// subGate 讓 SUB 的閘門只對 SUB 範圍的場景放行：至少一個 @SUB-R*，而且所有 @SUB-R* 都在 subCoreRules。
// 其他場景（DIG 等只有 @SUB-01 的場景）停在同一個閘門，回傳裸的 godog.ErrPending，junit 的訊息與切片之前相同（同 hubGate）。
func (s *Scenario) subGate() error {
	if s.digGate() {
		// M4-S6：DIG 的訂閱場景（只有 @SUB-01、沒有 @SUB-R）放行。
		return nil
	}
	core := false
	for _, t := range s.tags {
		v, ok := strings.CutPrefix(t, "@SUB-R")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || !subCoreRules[n] {
			return godog.ErrPending
		}
		core = true
	}
	if !core {
		return godog.ErrPending
	}
	return nil
}

// subState 是場景的訂閱狀態。
type subState struct {
	// pending 是 server 啟動之前宣告的訂閱，依宣告順序在第一個送出請求的步驟寫入（見 flushSubscriptions）。
	pending []pendingSubscription
}

type pendingSubscription struct {
	alias string
	body  map[string]any
}

var subAliasPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// youtubeFeedsServed 是 SUB 的閘門：把 fakefeed 登記到 www.youtube.com，並讓假 yt-dlp 認得預設頻道的 handle。
func (s *Scenario) youtubeFeedsServed() error {
	if err := s.subGate(); err != nil {
		return err
	}
	s.World.ServeFeeds()
	if s.World.Tools.Channels == nil {
		s.World.Tools.Channels = map[string]string{}
	}
	s.World.Tools.Channels[faketool.HandleOf(DefaultChannelName)] = DefaultChannelID
	return s.World.SyncTools()
}

func (s *Scenario) youtubeFeedFails(channelID string, status int) error {
	if status < 400 || status > 599 {
		return fmt.Errorf("HTTP %d 不是錯誤的狀態碼", status)
	}
	s.World.Feed.Fail(channelID, status)
	return nil
}

func (s *Scenario) youtubeFeedRecovers(channelID string) error {
	s.World.Feed.Recover(channelID)
	return nil
}

func (s *Scenario) youtubeFeedRequested(channelID string, want int) error {
	if got := s.World.Feed.Requests(channelID); got != want {
		return fmt.Errorf("頻道 %s 的 feed 被請求了 %d 次，預期 %d 次", channelID, got, want)
	}
	return nil
}

// subscriptionFor 累積（server 還沒啟動時）或立即建立訂閱（steps.md 4.17）。
func (s *Scenario) subscriptionFor(alias, rawURL string, doc *godog.DocString) error {
	if !subAliasPattern.MatchString(alias) {
		return fmt.Errorf("訂閱的別名 %q 不正確：只能是小寫英文、數字與 -", alias)
	}
	body := map[string]any{}
	if doc != nil {
		m, err := s.yamlMap(doc.Content, "訂閱 "+alias+" 的設定")
		if err != nil {
			return err
		}
		body = m
	}
	body["url"] = rawURL
	p := pendingSubscription{alias: alias, body: body}
	if s.server == nil {
		s.sub.pending = append(s.sub.pending, p)
		return nil
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	return s.createSubscription(p)
}

// flushSubscriptions 依宣告順序寫入累積的訂閱（在處理方案與預設處理方案之後，見 prepareOwner）。
func (s *Scenario) flushSubscriptions() error {
	ps := s.sub.pending
	s.sub.pending = nil
	for _, p := range ps {
		if err := s.createSubscription(p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scenario) createSubscription(p pendingSubscription) error {
	resp, err := s.configWrite(http.MethodPost, "/api/subscriptions", p.body, http.StatusCreated, "建立訂閱 "+p.alias)
	if err != nil {
		return err
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.body, &got); err != nil || got.ID == "" {
		return fmt.Errorf("建立訂閱 %s：回應沒有 id：%s", p.alias, snippet(resp.body))
	}
	s.World.SetSubscription(p.alias, got.ID)
	return nil
}

// subscriptionGet 讀取 GET /api/subscriptions/<id>（不覆寫最近一次回應）。
func (s *Scenario) subscriptionGet(alias, suffix string, out any) error {
	id, ok := s.World.Subscription(alias)
	if !ok {
		return fmt.Errorf("沒有別名為 %q 的訂閱", alias)
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	resp, err := s.do(http.MethodGet, "/api/subscriptions/"+url.PathEscape(id)+suffix, requestOpts{who: asOwner})
	if err != nil {
		return err
	}
	if _, err := checkConfigResponse(resp, http.MethodGet, "/api/subscriptions/"+id+suffix, http.StatusOK, "讀取訂閱 "+alias); err != nil {
		return err
	}
	return json.Unmarshal(resp.body, out)
}

// subscriptionHasEntries 比對條目（不看順序）；table 為 nil 代表沒有條目。
func (s *Scenario) subscriptionHasEntries(alias string, table *godog.Table) error {
	var got struct {
		Entries []struct {
			Ref      string  `json:"ref"`
			Decision string  `json:"decision"`
			Reason   *string `json:"reason"`
		} `json:"entries"`
	}
	if err := s.subscriptionGet(alias, "/entries", &got); err != nil {
		return err
	}
	var have []string
	for _, e := range got.Entries {
		r := ""
		if e.Reason != nil {
			r = *e.Reason
		}
		have = append(have, e.Ref+" | "+e.Decision+" | "+r)
	}
	var want []string
	if table != nil {
		for i, row := range table.Rows {
			if len(row.Cells) != 3 {
				return fmt.Errorf("表格第 %d 列應有 video、decision、reason 三欄", i+1)
			}
			c := func(j int) string { return strings.TrimSpace(row.Cells[j].Value) }
			if i == 0 && c(0) == "video" {
				continue
			}
			want = append(want, c(0)+" | "+c(1)+" | "+c(2))
		}
	}
	sort.Strings(have)
	sort.Strings(want)
	if !slices.Equal(have, want) {
		return fmt.Errorf("訂閱 %s 的條目不符：\n實際：%v\n預期：%v", alias, have, want)
	}
	return nil
}

func (s *Scenario) nextCheck(alias string) (time.Time, error) {
	var got struct {
		NextCheckAt *time.Time `json:"next_check_at"`
	}
	if err := s.subscriptionGet(alias, "", &got); err != nil {
		return time.Time{}, err
	}
	if got.NextCheckAt == nil {
		return time.Time{}, fmt.Errorf("訂閱 %s 沒有下一次檢查（next_check_at 是 null）", alias)
	}
	return *got.NextCheckAt, nil
}

func (s *Scenario) nextCheckAt(alias, at string) error {
	want, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("%q 不是 RFC 3339：%w", at, err)
	}
	got, err := s.nextCheck(alias)
	if err != nil {
		return err
	}
	if !got.Equal(want) {
		return fmt.Errorf("訂閱 %s 的下一次檢查是 %s，預期 %s", alias, got.Format(time.RFC3339Nano), at)
	}
	return nil
}

func (s *Scenario) nextCheckBetween(alias, from, to string) error {
	lo, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return err
	}
	hi, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return err
	}
	got, err := s.nextCheck(alias)
	if err != nil {
		return err
	}
	if got.Before(lo) || !got.Before(hi) {
		return fmt.Errorf("訂閱 %s 的下一次檢查是 %s，預期在 [%s, %s) 之間", alias, got.Format(time.RFC3339Nano), from, to)
	}
	return nil
}

func (s *Scenario) numberOfSubscriptions(want int) error {
	n := 0
	if s.stopped {
		var err error
		if n, err = s.World.DBCountSubscriptions(); err != nil {
			return err
		}
	} else {
		if err := s.prepareOwner(); err != nil {
			return err
		}
		resp, err := s.do(http.MethodGet, "/api/subscriptions", requestOpts{who: asOwner})
		if err != nil {
			return err
		}
		if _, err := checkConfigResponse(resp, http.MethodGet, "/api/subscriptions", http.StatusOK, "列出訂閱"); err != nil {
			return err
		}
		var got struct {
			Subscriptions []json.RawMessage `json:"subscriptions"`
		}
		if err := json.Unmarshal(resp.body, &got); err != nil {
			return err
		}
		n = len(got.Subscriptions)
	}
	if n != want {
		return fmt.Errorf("有 %d 個訂閱，預期 %d 個", n, want)
	}
	return nil
}

// podcastFeedAdds 在既有的假 feed 最前面加上一集（規則同 `a podcast feed … with the episodes:`）。
func (s *Scenario) podcastFeedAdds(name, guid, title, audio string) error {
	audioURL, fixture, err := podcastAudioTarget(name, audio)
	if err != nil {
		return err
	}
	b, err := s.fixtureFile("podcast", fixture)
	if err != nil {
		return err
	}
	if err := s.World.Podcasts.Prepend(name, fakepodcast.Episode{GUID: guid, Title: title, AudioURL: audioURL, Audio: b}); err != nil {
		return err
	}
	return s.World.Net.Route(audioURL, s.World.Podcasts.AudioHandler(fakepodcast.Ref(name, guid)))
}

// ownerImportsSubscriptions 以 multipart 的 file 欄位送出 POST /api/subscriptions/import（steps.md 4.17）：
// 以 / 開頭的是場景中的路徑（/tmp 的對應見 filePath），否則是 features/fixtures/subscriptions/ 的檔名。
func (s *Scenario) ownerImportsSubscriptions(name string) error {
	var (
		content []byte
		err     error
	)
	if strings.HasPrefix(name, "/") {
		if content, err = os.ReadFile(s.filePath(name)); err != nil {
			return fmt.Errorf("讀取要匯入的檔案 %s：%w", name, err)
		}
	} else if content, err = s.fixtureFile("subscriptions", name); err != nil {
		return err
	}
	return s.importSubscriptions(filepath.Base(name), content)
}

// ownerImportsGeneratedOPML 以 fakefeed.GeneratedOPML 產生含 n 個 outline 的 OPML 並匯入。
func (s *Scenario) ownerImportsGeneratedOPML(n int) error {
	if n < 0 || n > 100000 {
		return fmt.Errorf("outline 數 %d 不合理", n)
	}
	return s.importSubscriptions("generated.opml", fakefeed.GeneratedOPML(n))
}

func (s *Scenario) importSubscriptions(filename string, content []byte) error {
	body, contentType, err := multipartFile("file", filename, content)
	if err != nil {
		return err
	}
	return s.send("POST /api/subscriptions/import", requestOpts{who: asOwner, body: body, contentType: contentType})
}
