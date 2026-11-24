package steps

import (
	"encoding/json"
	"errors"
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
	// LogExcerpt 是 failed、blocked 步驟的 log 片段（PIPE-R16）；nil 代表回應中沒有這個欄位。
	LogExcerpt *string `json:"log_excerpt"`
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
		return &apiFailure{method: method, path: path, status: resp.status, body: resp.body}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return fmt.Errorf("%s %s：回應不是預期的 JSON：%w\n%s", method, path, err, snippet(resp.body))
	}
	return nil
}

// apiFailure 是非 2xx 的回應；步驟可以用 errors.As 取出，依回應內容決定怎麼處理（例如垃圾桶中的影音，見 inTrashBody）。
type apiFailure struct {
	method, path string
	status       int
	body         []byte
}

func (e *apiFailure) Error() string {
	return fmt.Sprintf("%s %s：HTTP %d %s", e.method, e.path, e.status, snippet(e.body))
}

// trashBody 是影音在垃圾桶中時，404 與 409 的回應內容（DEL-R1、R10）。
type trashBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Actions []string `json:"actions"`
	ItemID  string   `json:"item_id"`
}

// inTrashBody 在 err 是「影音在垃圾桶中」的 404（錯誤碼 in_trash）時回傳它的內容。
func inTrashBody(err error) (trashBody, bool) {
	var f *apiFailure
	if !errors.As(err, &f) || f.status != http.StatusNotFound {
		return trashBody{}, false
	}
	var b trashBody
	if json.Unmarshal(f.body, &b) != nil || b.Code != "in_trash" {
		return trashBody{}, false
	}
	return b, true
}

func isNotImplemented(body []byte) bool {
	var e struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &e) == nil && e.Code == "not_implemented"
}

// listItems 回傳資料庫中的影音（不含垃圾桶中的）。
func (s *Scenario) listItems() ([]itemJSON, error) {
	return pageItems(func(path string, out any) error {
		return s.apiJSON(http.MethodGet, path, nil, out)
	})
}

// pageItems 用游標讀完 GET /api/items 的每一頁。get 送出 GET 請求並把回應解碼到 out。
// 游標循環（A→A、A→B→A）與累積筆數超過 total 都視為失敗，避免無窮迴圈或悄悄回傳不完整的結果。
func pageItems(get func(path string, out any) error) ([]itemJSON, error) {
	var all []itemJSON
	cursor := ""
	seen := map[string]bool{}
	for {
		path := "/api/items?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var out struct {
			Items      []itemJSON `json:"items"`
			Total      int        `json:"total"`
			NextCursor string     `json:"next_cursor"`
		}
		if err := get(path, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if len(all) > out.Total {
			return nil, fmt.Errorf("GET /api/items 累積了 %d 筆，超過 total %d", len(all), out.Total)
		}
		// 最後一頁沒有 next_cursor（舊 API 也不回），迴圈在此結束。
		if out.NextCursor == "" {
			return all, nil
		}
		// 回到讀過的游標（A→A、A→B→A）代表分頁不會結束。
		if seen[out.NextCursor] || out.NextCursor == cursor {
			return nil, fmt.Errorf("GET /api/items 重複回傳同一個 next_cursor %q", out.NextCursor)
		}
		seen[out.NextCursor] = true
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
// 代號只換成 ID，不讀影音詳情：s.itemID 先查列表，再查新增時記下的 World.Refs，所以影音在垃圾桶中
// （詳情回 404 in_trash）時，`/api/trash/<代號>/restore` 一樣能換成正確的 ID。
// 找不到影音的代號維持原樣，讓請求自己回報 404。
func (s *Scenario) substRefs(path string) (string, error) {
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(path, func(m string) string {
		ref, channel := strings.CutPrefix(m, "channel-of:")
		if !channel {
			if strings.HasPrefix(m, "local:") {
				return resolveLocalRef(m, s.itemID)
			}
			id, err := s.itemID(m)
			if err != nil {
				return m
			}
			return id
		}
		it, err := s.item(ref)
		if err != nil {
			return m
		}
		if it.ChannelID == "" {
			firstErr = fmt.Errorf("影音 %q 沒有所屬的頻道", ref)
			return m
		}
		return it.ChannelID
	})
	return out, firstErr
}

// resolveLocalRef 處理請求路徑中的 local: 代號。refPattern 的 `local:[^\s?]+` 會把後面的路徑段一起吃進去
// （local:lecture.mp4/media），所以先整段查；不是已知的影音時，從右邊依 / 逐段縮短，找最長的已知前綴，
// 換成影音 ID 並接回被縮掉的部分（<id>/media）。lookup 找不到任何前綴時原樣回傳，讓請求自己回報 404。
func resolveLocalRef(m string, lookup func(ref string) (string, error)) string {
	for cand := m; ; {
		if id, err := lookup(cand); err == nil {
			return id + m[len(cand):]
		}
		i := strings.LastIndex(cand, "/")
		if i < 0 {
			return m
		}
		cand = cand[:i]
		if cand == "local:" || cand == "local" {
			return m
		}
	}
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
