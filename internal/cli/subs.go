package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/subscription"
)

// parseSubs 解析 `notesteme subs ls|add|show|preview|check|rm|import`（SUB-R13）。規則與錯誤和 API 相同：
// 參數的格式在這裡檢查（結束碼 2），數值的範圍與網址的辨識交給 server（同樣的 422 → 結束碼 2）。
func parseSubs(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("subs 需要子命令：ls、add、show、preview、check、rm 或 import。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls":
		return parseSubsLs(rest)
	case "add":
		return parseSubsAdd(rest)
	case "show":
		return parseSubsShow(rest)
	case "preview":
		return parseSubsPreview(rest)
	case "check":
		return parseSubsCheck(rest)
	case "rm":
		return parseSubsRm(rest)
	case "import":
		return parseSubsImport(rest)
	default:
		return nil, usagef("不認得的子命令 subs %s。", sub)
	}
}

// subView 是 CLI 文字輸出用得到的訂閱欄位。
type subView struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Platform       string     `json:"platform"`
	ExternalID     string     `json:"external_id"`
	URL            string     `json:"url"`
	Title          string     `json:"title"`
	Profile        *string    `json:"profile"`
	Schedule       string     `json:"schedule"`
	Enabled        bool       `json:"enabled"`
	LastCheckedAt  *time.Time `json:"last_checked_at"`
	NextCheckAt    *time.Time `json:"next_check_at"`
	LastError      *string    `json:"last_error"`
	LastErrorClass *string    `json:"last_error_class"`
	Backfill       struct {
		Mode  string `json:"mode"`
		Count *int   `json:"count"`
	} `json:"backfill"`
}

func (v subView) backfillText() string {
	if v.Backfill.Mode == subscription.BackfillRecent && v.Backfill.Count != nil {
		return fmt.Sprintf("recent:%d", *v.Backfill.Count)
	}
	return v.Backfill.Mode
}

func timeText(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// subID 檢查位置參數是一個訂閱 id（不能是網址或路徑）。
func subID(cmd string, pos []string) (string, error) {
	if len(pos) != 1 || pos[0] == "" || strings.ContainsAny(pos[0], "/?#") || len(pos[0]) > 128 {
		return "", usagef("subs %s 需要一個訂閱的 id。", cmd)
	}
	return pos[0], nil
}

func subPath(id, suffix string) string { return "/api/subscriptions/" + url.PathEscape(id) + suffix }

func parseSubsLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs ls", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("subs ls 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/subscriptions", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var body struct {
			Subscriptions []subView `json:"subscriptions"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		for _, v := range body.Subscriptions {
			state := "啟用"
			if !v.Enabled {
				state = "停用"
			}
			fmt.Fprintf(s.stdout, "%s\t%s\t%s\t%s\t%s\t下次 %s\t%s\n", v.ID, v.Kind, v.Schedule, state, v.backfillText(), timeText(v.NextCheckAt), v.URL)
		}
		return exitOK, nil
	}}, nil
}

// parseBackfill 解析 --backfill：none、all 或 recent:<N>；N 的範圍由 server 檢查。
func parseBackfill(v string) (map[string]any, error) {
	switch v {
	case subscription.BackfillNone, subscription.BackfillAll:
		return map[string]any{"mode": v}, nil
	}
	n, ok := strings.CutPrefix(v, subscription.BackfillRecent+":")
	if ok {
		if c, err := strconv.Atoi(n); err == nil {
			return map[string]any{"mode": subscription.BackfillRecent, "count": c}, nil
		}
	}
	return nil, usagef("--backfill 必須是 none、recent:<N> 或 all，收到 %q。", v)
}

func parseSubsAdd(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs add", g)
	profile := fs.String("profile", "", "處理方案（預設用預設處理方案）")
	schedule := fs.String("schedule", "", "檢查頻率（Go duration，1h～168h，預設 6h）")
	backfill := fs.String("backfill", "", "回補範圍：none（預設）、recent:<N> 或 all")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || pos[0] == "" {
		return nil, usagef("subs add 需要一個網址。")
	}
	body := map[string]any{"url": pos[0]}
	if *profile != "" {
		body["profile"] = *profile
	}
	if fs.Changed("schedule") {
		body["schedule"] = *schedule
	}
	if fs.Changed("backfill") {
		bf, err := parseBackfill(*backfill)
		if err != nil {
			return nil, err
		}
		body["backfill"] = bf
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/subscriptions", body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var v subView
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已建立訂閱 %s（%s %s），回補 %s，下一次檢查 %s。\n", v.ID, v.Kind, v.ExternalID, v.backfillText(), timeText(v.NextCheckAt))
		return exitOK, nil
	}}, nil
}

func parseSubsShow(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs show", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	id, err := subID("show", pos)
	if err != nil {
		return nil, err
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, subPath(id, ""), nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var v subView
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		profile := "（預設）"
		if v.Profile != nil {
			profile = *v.Profile
		}
		fmt.Fprintf(s.stdout, "%s\n  種類：%s（%s %s）\n  網址：%s\n  標題：%s\n  處理方案：%s\n  檢查頻率：%s，回補：%s，啟用：%t\n  上次檢查：%s\n  下次檢查：%s\n",
			v.ID, v.Kind, v.Platform, v.ExternalID, v.URL, v.Title, profile, v.Schedule, v.backfillText(), v.Enabled,
			timeText(v.LastCheckedAt), timeText(v.NextCheckAt))
		if v.LastError != nil {
			class := ""
			if v.LastErrorClass != nil {
				class = *v.LastErrorClass
			}
			fmt.Fprintf(s.stdout, "  最近的錯誤（%s）：%s\n", class, *v.LastError)
		}
		return exitOK, nil
	}}, nil
}

// parseSubsPreview 解析 `subs preview <id|url>`：含 :// 的是網址（還沒建立的訂閱），其他是訂閱的 id。
func parseSubsPreview(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs preview", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || pos[0] == "" {
		return nil, usagef("subs preview 需要一個訂閱的 id 或網址。")
	}
	path, body := "", any(nil)
	if strings.Contains(pos[0], "://") {
		path, body = "/api/subscriptions/preview", map[string]any{"url": pos[0]}
	} else {
		id, err := subID("preview", pos)
		if err != nil {
			return nil, err
		}
		path = subPath(id, "/preview")
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, path, body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var p subscription.Preview
		if err := json.Unmarshal(raw, &p); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "會處理 %d 部：\n", len(p.WouldProcess))
		for _, r := range p.WouldProcess {
			fmt.Fprintf(s.stdout, "  %s\t%s\n", r.Ref, r.Title)
		}
		fmt.Fprintf(s.stdout, "會略過 %d 部：\n", len(p.WouldSkip))
		for _, r := range p.WouldSkip {
			fmt.Fprintf(s.stdout, "  %s\t%s\t%s\n", r.Ref, r.Reason, r.Title)
		}
		if p.Truncated {
			fmt.Fprintf(s.stdout, "（只列出各 %d 部）\n", subscription.MaxPreviewRows)
		}
		return exitOK, nil
	}}, nil
}

func parseSubsCheck(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs check", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	id, err := subID("check", pos)
	if err != nil {
		return nil, err
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, subPath(id, "/check"), nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var v struct {
			NextCheckAt *time.Time `json:"next_check_at"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已排定在下一輪檢查（%s）。\n", timeText(v.NextCheckAt))
		return exitOK, nil
	}}, nil
}

// parseSubsRm 解析 `subs rm <id> [--yes]`：沒有 --yes 又不在終端機上是參數錯誤（同 DEL-R13）。
func parseSubsRm(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs rm", g)
	yes := fs.Bool("yes", false, "不詢問確認")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	id, err := subID("rm", pos)
	if err != nil {
		return nil, err
	}
	return &plan{g: g, precheck: requireConfirmable(yes), run: func(ctx context.Context, s *session) (int, error) {
		if !*yes && !s.confirm("要刪除這個訂閱嗎？已建立的影音會保留。") {
			return s.declined()
		}
		if _, _, err := s.be.call(ctx, http.MethodDelete, subPath(id, ""), nil); err != nil {
			return 0, err
		}
		if !g.json {
			fmt.Fprintf(s.stdout, "已刪除訂閱 %s。\n", id)
		}
		return exitOK, nil
	}}, nil
}

// parseSubsImport 解析 `subs import <file>`：讀取本機的 OPML 或 subscriptions.csv，以 multipart 上傳。
// 最多讀上限加 1 個位元組，超過上限的檔案由 server 回 413（訊息與 API 相同）。
func parseSubsImport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("subs import", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || pos[0] == "" {
		return nil, usagef("subs import 需要一個檔案。")
	}
	file := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		f, err := os.Open(file)
		if err != nil {
			return 0, fmt.Errorf("無法讀取匯入檔：%w", err)
		}
		content, err := io.ReadAll(io.LimitReader(f, subscription.MaxImportBytes+1))
		f.Close()
		if err != nil {
			return 0, fmt.Errorf("無法讀取匯入檔：%w", err)
		}
		body, contentType, err := multipartBody("file", filepath.Base(file), content)
		if err != nil {
			return 0, err
		}
		raw, _, err := s.be.callRaw(ctx, http.MethodPost, "/api/subscriptions/import", contentType, body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var res subscription.ImportResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已建立 %d 個訂閱，重複 %d 個，失敗 %d 個。\n", res.Created, len(res.Duplicates), len(res.Failed))
		for _, d := range res.Duplicates {
			fmt.Fprintf(s.stdout, "  重複：%s\n", d)
		}
		for _, f := range res.Failed {
			fmt.Fprintf(s.stdout, "  第 %d 行 %s：%s\n", f.Line, f.URL, f.Message)
		}
		return exitOK, nil
	}}, nil
}

// multipartBody 建立只有一個檔案欄位的 multipart body，回傳 body 與 Content-Type。
func multipartBody(field, filename string, content []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}
