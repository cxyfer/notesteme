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
	var list struct {
		Items []struct {
			ID  string `json:"id"`
			Ref string `json:"ref"`
		} `json:"items"`
	}
	if err := s.be.getJSON(ctx, "/api/items", &list); err != nil {
		return "", err
	}
	for _, it := range list.Items {
		if it.Ref == arg {
			return it.ID, nil
		}
	}
	return "", &apiError{Status: http.StatusNotFound, Code: "not_found", Message: fmt.Sprintf("找不到影音 %s。", arg)}
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
	status := fs.String("status", "", "依步驟狀態篩選（<stage>=<status>）")
	channel := fs.String("channel", "", "依頻道篩選")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("items ls 不接受位置參數：%q。", pos[0])
	}
	// TODO(LIB)：API 的清單還沒有篩選條件；做 --status、--channel 時加在 API 上，CLI 只傳遞參數。
	if *status != "" || *channel != "" {
		return nil, usagef("items ls 的 --status、--channel 尚未支援。")
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var list struct {
			Items []itemView `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		for _, it := range list.Items {
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
