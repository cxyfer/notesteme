package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// stageView 是 API 回應中步驟的欄位（只列出 CLI 會用到的）。
type stageView struct {
	Stage       string     `json:"stage"`
	Status      string     `json:"status"`
	BlockReason string     `json:"block_reason"`
	ErrorClass  string     `json:"error_class"`
	NotBefore   *time.Time `json:"not_before"`
	Message     string     `json:"message"`
	Actions     []string   `json:"actions"`
}

// itemView 是 GET /api/items/{id} 回應中 CLI 會用到的欄位。
type itemView struct {
	ID     string      `json:"id"`
	Ref    string      `json:"ref"`
	Title  string      `json:"title"`
	URL    string      `json:"url"`
	Stages []stageView `json:"stages"`
}

// label 是顯示用的名稱：優先用影音代號。
func (v itemView) label() string {
	if v.Ref != "" {
		return v.Ref
	}
	return v.ID
}

// refPrefix 找出影音代號（steps.md 第 2 節）；其他字串視為影音 ID。
var refPrefix = regexp.MustCompile(`^(yt|bili|pod|local):`)

// resolveItem 把 <item> 換成影音 ID：影音代號先查清單；其他字串直接當成 ID。
func (s *session) resolveItem(ctx context.Context, arg string) (string, error) {
	if !refPrefix.MatchString(arg) {
		return arg, nil
	}
	var found string
	err := s.walkItemPages(ctx, nil, func(page itemsPage) (bool, error) {
		for _, raw := range page.Items {
			var it struct {
				ID  string `json:"id"`
				Ref string `json:"ref"`
			}
			if err := json.Unmarshal(raw, &it); err != nil {
				return false, fmt.Errorf("回應不是預期的 JSON：%w", err)
			}
			if it.Ref == arg {
				found = it.ID
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", &apiError{Status: http.StatusNotFound, Code: "not_found", Message: fmt.Sprintf("找不到影音 %s。", arg)}
	}
	return found, nil
}

// itemsPage 是 GET /api/items 的一頁；Items 保留原始 JSON，--json 輸出才能以 API 的回應為準（CLI-R5）。
type itemsPage struct {
	Items      []json.RawMessage `json:"items"`
	Total      int               `json:"total"`
	NextCursor string            `json:"next_cursor"`
	// Raw 是這一頁的原始回應。
	Raw []byte `json:"-"`
}

// maxItemPages 是讀取清單時最多讀幾頁，避免 server 異常（游標一直不結束）時無窮迴圈。
const maxItemPages = 10000

// walkItemPages 從第一頁開始依 next_cursor 讀到最後一頁；fn 回傳 true 時提前結束。
// params 是篩選條件（status、channel 等），每一頁都帶著，游標只在相同的篩選條件下有效。
func (s *session) walkItemPages(ctx context.Context, params url.Values, fn func(itemsPage) (stop bool, err error)) error {
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("limit", "200")
	prev := ""
	for range maxItemPages {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		var page itemsPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		page.Raw = raw
		if stop, err := fn(page); err != nil || stop {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		if page.NextCursor == prev {
			return fmt.Errorf("server 回傳了重複的游標，無法讀完清單")
		}
		prev = page.NextCursor
		q.Set("cursor", page.NextCursor)
	}
	return fmt.Errorf("清單超過 %d 頁，已停止讀取", maxItemPages)
}

// fetchItem 取得影音詳情，同時回傳原始的 JSON 物件（輸出以 API 的回應為準，CLI-R5）。
func (s *session) fetchItem(ctx context.Context, id string) (itemView, map[string]json.RawMessage, error) {
	raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id), nil)
	if err != nil {
		return itemView{}, nil, err
	}
	return decodeItem(raw)
}

func decodeItem(raw []byte) (itemView, map[string]json.RawMessage, error) {
	var v itemView
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, nil, fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return v, nil, fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	return v, obj, nil
}

// stageSummary 是 `stage=status` 的文字，依步驟順序。
func stageSummary(stages []stageView) string {
	parts := make([]string, 0, len(stages))
	for _, st := range stages {
		parts = append(parts, st.Stage+"="+st.Status)
	}
	return strings.Join(parts, " ")
}

// printJSON 輸出一行 JSON。
func (s *session) printJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.stdout, "%s\n", raw)
	return err
}

// printRaw 原樣輸出 API 的回應（補上結尾換行）。
func (s *session) printRaw(raw []byte) {
	s.stdout.Write(raw)
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		fmt.Fprintln(s.stdout)
	}
}

// printItemText 輸出影音詳情的文字版本。
func (s *session) printItemText(v itemView) {
	fmt.Fprintf(s.stdout, "%s\t%s\n", v.label(), v.Title)
	if v.URL != "" {
		fmt.Fprintf(s.stdout, "  網址：%s\n", v.URL)
	}
	for _, st := range v.Stages {
		fmt.Fprintf(s.stdout, "  %-10s %s", st.Stage, st.Status)
		if st.Message != "" {
			fmt.Fprintf(s.stdout, "：%s", st.Message)
		}
		fmt.Fprintln(s.stdout)
	}
}

// ---- items 子命令 ----

// parseItems 解析 `items ls | show | rerun`。
func parseItems(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("items 需要子命令：ls、show 或 rerun。")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return parseItemsLs(rest)
	case "show":
		return parseItemsShow(rest)
	case "rerun":
		return parseItemsRerun(rest)
	case "rm", "restore":
		// 屬於 DEL 切片。
		return unimplemented("items " + sub), nil
	}
	return nil, usagef("不認得的子命令 items %s。", sub)
}

func parseItemsLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items ls", g)
	status := fs.String("status", "", "依步驟狀態篩選（<stage>=<status>，多個以逗號分隔；同一步驟為 OR，不同步驟為 AND）")
	channel := fs.String("channel", "", "依頻道 ID 篩選")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("items ls 不接受位置參數：%q。", pos[0])
	}
	params := url.Values{}
	if *status != "" {
		for _, v := range strings.Split(*status, ",") {
			stage, st, ok := strings.Cut(strings.Replace(strings.TrimSpace(v), ":", "=", 1), "=")
			if !ok || !pipeline.ValidStage(stage) || !pipeline.ValidStatus(st) {
				return nil, usagef("--status 的格式是 <步驟>=<狀態>（步驟：%s），收到 %q。", strings.Join(pipeline.Stages, "、"), v)
			}
			// API 的格式是 <步驟>:<狀態>。
			params.Add("status", stage+":"+st)
		}
	}
	if *channel != "" {
		params.Set("channel", *channel)
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		// 一直讀到沒有 next_cursor 為止：清單由新到舊，超過一頁時不能只看第一頁。
		var items []json.RawMessage
		total, pages := 0, 0
		var firstRaw []byte
		err := s.walkItemPages(ctx, params, func(p itemsPage) (bool, error) {
			if pages == 0 {
				firstRaw = p.Raw
			}
			items = append(items, p.Items...)
			total = p.Total
			pages++
			return false, nil
		})
		if err != nil {
			return 0, err
		}
		if g.json {
			// 只有一頁時原樣輸出 API 的回應（CLI-R5）；多頁時合併成同樣形狀的 JSON（沒有 next_cursor）。
			if pages == 1 {
				s.printRaw(firstRaw)
				return exitOK, nil
			}
			if items == nil {
				items = []json.RawMessage{}
			}
			return exitOK, s.printJSON(struct {
				Items []json.RawMessage `json:"items"`
				Total int               `json:"total"`
			}{items, total})
		}
		for _, raw := range items {
			var it itemView
			if err := json.Unmarshal(raw, &it); err != nil {
				return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
			}
			fmt.Fprintf(s.stdout, "%s\t%s\n", it.label(), it.Title)
		}
		return exitOK, nil
	}}, nil
}

func parseItemsShow(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items show", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("items show 需要一個 <item>。")
	}
	arg := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		id, err := s.resolveItem(ctx, arg)
		if err != nil {
			return 0, err
		}
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id), nil)
		if err != nil {
			return 0, err
		}
		return s.printDetail(raw)
	}}, nil
}

// printDetail 輸出影音詳情：--json 時原樣輸出 API 的回應。
func (s *session) printDetail(raw []byte) (int, error) {
	if s.g.json {
		s.printRaw(raw)
		return exitOK, nil
	}
	v, _, err := decodeItem(raw)
	if err != nil {
		return 0, err
	}
	s.printItemText(v)
	return exitOK, nil
}

func parseItemsRerun(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items rerun", g)
	from := fs.String("from", "", "從哪個步驟開始重新處理")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("items rerun 需要一個 <item>。")
	}
	if *from == "" {
		return nil, usagef("items rerun 需要 --from <stage>（%s）。", strings.Join(pipeline.Stages, "、"))
	}
	if !pipeline.ValidStage(*from) {
		return nil, usagef("--from 必須是 %s 其中之一，收到 %q。", strings.Join(pipeline.Stages, "、"), *from)
	}
	arg, stage := pos[0], *from
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		id, err := s.resolveItem(ctx, arg)
		if err != nil {
			return 0, err
		}
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/items/"+url.PathEscape(id)+"/rerun", map[string]string{"from": stage})
		if err != nil {
			return 0, err
		}
		return s.printDetail(raw)
	}}, nil
}
