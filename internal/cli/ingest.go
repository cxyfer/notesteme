package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
)

// defaultWaitTimeout 是 --wait 沒有指定 --timeout 時的上限。
const defaultWaitTimeout = 30 * time.Minute

// ingestTarget 把位置參數轉成 POST /api/items 的欄位：
// 網址原樣傳送；yt:、bili: 代號轉成網址（source.ParseRef）；其他視為本機路徑（絕對化後傳給 server 檢查）。
func ingestTarget(arg string) (field, value string, err error) {
	switch {
	case refPrefix.MatchString(arg):
		u, ok := source.ParseRef(arg)
		if !ok {
			return "", "", usagef("無法辨識影音代號 %q：格式是 yt:<id> 或 bili:<BV>/p<N>。", arg)
		}
		return "url", u, nil
	case strings.Contains(arg, "://"):
		return "url", arg, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", "", usagef("路徑 %q 不正確：%v。", arg, err)
	}
	return "path", abs, nil
}

func parseIngest(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("ingest", g)
	profile := fs.String("profile", "", "處理方案")
	until := fs.String("until", "", "做到哪個步驟為止（含）："+strings.Join(pipeline.Stages, "、"))
	wait := fs.Bool("wait", false, "等到指定步驟完成或停住才結束")
	timeout := fs.Duration("timeout", defaultWaitTimeout, "--wait 的等待上限")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("ingest 需要一個 <url|ref|path>。")
	}
	if *until != "" && !pipeline.ValidStage(*until) {
		return nil, usagef("--until 必須是 %s 其中之一，收到 %q。", strings.Join(pipeline.Stages, "、"), *until)
	}
	if fs.Changed("timeout") && !*wait {
		return nil, usagef("--timeout 需要搭配 --wait。")
	}
	if *timeout <= 0 {
		return nil, usagef("--timeout 必須大於 0。")
	}
	field, value, err := ingestTarget(pos[0])
	if err != nil {
		return nil, err
	}
	body := map[string]any{field: value}
	if *profile != "" {
		body["profile"] = *profile
	}
	if *until != "" {
		body["until"] = *until
	}
	opts := waitOpts{until: *until, timeout: *timeout}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		return s.ingest(ctx, body, *wait, opts)
	}}, nil
}

// ingested 是新增的結果中，CLI 要追蹤的影音。
type ingested struct {
	id      string
	already bool
}

// ingest 新增影音；--wait 時等到指定步驟完成或停住（CLI-R7），結束碼依 CLI-R6。
func (s *session) ingest(ctx context.Context, body map[string]any, wait bool, opts waitOpts) (int, error) {
	raw, _, err := s.be.call(ctx, http.MethodPost, "/api/items", body)
	if err != nil {
		return 0, err
	}
	var created struct {
		Items []struct {
			ID      string `json:"id"`
			Already bool   `json:"already_in_library"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	items := make([]ingested, 0, len(created.Items))
	for _, it := range created.Items {
		items = append(items, ingested{id: it.ID, already: it.Already})
	}

	code := exitOK
	if wait {
		if code, err = s.waitFor(ctx, items, opts); err != nil {
			return 0, err
		}
	}
	// 輸出在等待之後：--json 的 stages 是最後的狀態。
	if err := s.printIngested(ctx, items); err != nil {
		return 0, err
	}
	return code, nil
}

// printIngested 輸出每部影音目前的狀態。--json 時每部影音一行（JSON Lines）：
// 影音的欄位加上 stages（步驟名稱對應狀態，CLI-R5）。
func (s *session) printIngested(ctx context.Context, items []ingested) error {
	for _, it := range items {
		v, obj, err := s.fetchItem(ctx, it.id)
		if err != nil {
			return err
		}
		if !s.g.json {
			state := "已存在"
			if !it.already {
				state = "新增"
			}
			fmt.Fprintf(s.stdout, "%s\t%s\t%s\t%s\n", v.label(), v.Title, state, stageSummary(v.Stages))
			continue
		}
		stages := make(map[string]string, len(v.Stages))
		for _, st := range v.Stages {
			stages[st.Stage] = st.Status
		}
		out := make(map[string]any, len(obj)+1)
		for k, raw := range obj {
			if k != "sources" && k != "stages" {
				out[k] = raw
			}
		}
		out["stages"] = stages
		out["already_in_library"] = it.already
		if err := s.printJSON(out); err != nil {
			return err
		}
	}
	return nil
}
