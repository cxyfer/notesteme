package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// refPattern 找出請求路徑中的影音代號（steps.md 第 2 節）：yt:<id>、bili:<bvid>/p<n>、pod:<feed>/<guid>、
// local:<path>，以及 channel-of:<代號>。
var refPattern = regexp.MustCompile(`(?:channel-of:)?(?:yt:[A-Za-z0-9_-]+|bili:[A-Za-z0-9]+/p[0-9]+|pod:[^/\s?]+/[^/\s?]+|local:[^\s?]+)`)

// 影音 API 回應的形狀（只列出步驟會用到的欄位）。
type itemJSON struct {
	ID           string       `json:"id"`
	Ref          string       `json:"ref"`
	ChannelID    string       `json:"channel_id"`
	OriginalLang string       `json:"original_lang"`
	LangMixed    bool         `json:"lang_mixed"`
	Stages       []stageJSON  `json:"stages"`
	Sources      []sourceJSON `json:"sources"`
}

type stageJSON struct {
	Stage       string   `json:"stage"`
	Status      string   `json:"status"`
	BlockReason string   `json:"block_reason"`
	ErrorClass  string   `json:"error_class"`
	NotBefore   *string  `json:"not_before"`
	Message     string   `json:"message"`
	Actions     []string `json:"actions"`
}

type sourceJSON struct {
	Source string `json:"source"`
	Lang   string `json:"lang"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// refToURL 把影音代號換成網址；不是代號的字串視為網址，原樣回傳。
func refToURL(ref string) string {
	if id, ok := strings.CutPrefix(ref, "yt:"); ok {
		return "https://www.youtube.com/watch?v=" + id
	}
	if rest, ok := strings.CutPrefix(ref, "bili:"); ok {
		if bvid, part, ok := strings.Cut(rest, "/p"); ok {
			return "https://www.bilibili.com/video/" + bvid + "?p=" + part
		}
	}
	return ref
}

// apiCall 以 owner 的身分送出請求並回傳回應，不改動 s.last（它代表場景中最近一次明確送出的請求）。
func (s *Scenario) apiCall(method, path string, body []byte) (*response, error) {
	if err := s.prepareOwner(); err != nil {
		return nil, err
	}
	return s.do(method, path, requestOpts{who: asOwner, body: body})
}

// apiJSON 送出請求並把 2xx 的回應解碼到 out。
// 伺服器明確回應 501 not_implemented 時視為 pending（ADR-0015：路由屬於之後的切片）。
func (s *Scenario) apiJSON(method, path string, body []byte, out any) error {
	resp, err := s.apiCall(method, path, body)
	if err != nil {
		return err
	}
	if resp.status == http.StatusNotImplemented && isNotImplemented(resp.body) {
		return godog.ErrPending
	}
	if resp.status < 200 || resp.status > 299 {
		return fmt.Errorf("%s %s：HTTP %d %s", method, path, resp.status, snippet(resp.body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return fmt.Errorf("%s %s：回應不是預期的 JSON：%w\n%s", method, path, err, snippet(resp.body))
	}
	return nil
}

func isNotImplemented(body []byte) bool {
	var e struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &e) == nil && e.Code == "not_implemented"
}

// listItems 回傳資料庫中的影音（不含垃圾桶中的）。
func (s *Scenario) listItems() ([]itemJSON, error) {
	var all []itemJSON
	cursor := ""
	for {
		path := "/api/items?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var out struct {
			Items      []itemJSON `json:"items"`
			NextCursor string     `json:"next_cursor"`
		}
		if err := s.apiJSON(http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		// 最後一頁沒有 next_cursor（舊 API 也不回），迴圈在此結束。
		if out.NextCursor == "" {
			return all, nil
		}
		if out.NextCursor == cursor {
			return nil, fmt.Errorf("GET /api/items 重複回傳同一個 next_cursor %q", cursor)
		}
		cursor = out.NextCursor
	}
}

// itemID 把影音代號換成實際的影音 ID：先問 API；影音在垃圾桶中（不在清單裡）時，
// 使用新增時記下的 ID（steps.md 第 2 節）。
func (s *Scenario) itemID(ref string) (string, error) {
	items, err := s.listItems()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.Ref == ref {
			return it.ID, nil
		}
	}
	if id, ok := s.World.Refs[ref]; ok {
		return id, nil
	}
	return "", fmt.Errorf("影音 %q 不在資料庫中", ref)
}

// item 取得影音詳情。
func (s *Scenario) item(ref string) (itemJSON, error) {
	id, err := s.itemID(ref)
	if err != nil {
		return itemJSON{}, err
	}
	var it itemJSON
	return it, s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id), nil, &it)
}

// substRefs 把請求路徑中的影音代號換成實際的影音 ID；`channel-of:<代號>` 換成影音所屬頻道的 ID。
// 找不到影音的代號維持原樣，讓請求自己回報 404。
func (s *Scenario) substRefs(path string) (string, error) {
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(path, func(m string) string {
		ref, channel := strings.CutPrefix(m, "channel-of:")
		if !channel {
			ref = m
		}
		it, err := s.item(ref)
		if err != nil {
			return m
		}
		if channel {
			if it.ChannelID == "" {
				firstErr = fmt.Errorf("影音 %q 沒有所屬的頻道", ref)
				return m
			}
			return it.ChannelID
		}
		return it.ID
	})
	return out, firstErr
}

// recordRefs 把新增影音的回應中的代號與 ID 記下來，影音之後進了垃圾桶也能找到原本的 ID。
func (s *Scenario) recordRefs(resp *response) {
	var out struct {
		Items []itemJSON `json:"items"`
	}
	if json.Unmarshal(resp.body, &out) != nil {
		return
	}
	for _, it := range out.Items {
		if it.Ref != "" && it.ID != "" {
			s.World.Refs[it.Ref] = it.ID
		}
	}
}
