package cli

// 這個檔案是刪除與垃圾桶的子命令（DEL-R13）：
// items rm、items restore、trash ls、trash empty。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"
)

// isTerminal 判斷 stdin 是不是終端機：只有 *os.File 而且 fd 是終端機（isatty）才算。
// 只看字元裝置不夠：/dev/null 也是字元裝置。做成變數，測試才能模擬終端機。
var isTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// requireConfirmable 在判斷模式之前檢查：沒有 --yes 又不在終端機上是參數錯誤（DEL-R13）。
func requireConfirmable(yes *bool) func(io.Reader) error {
	return func(stdin io.Reader) error {
		if *yes || isTerminal(stdin) {
			return nil
		}
		return usagef("不在終端機上執行，無法詢問確認；請加上 --yes。")
	}
}

// confirm 在終端機上詢問；只有 y、yes 算同意，讀不到內容（EOF）視為不同意。
func (s *session) confirm(prompt string) bool {
	if s.stdin == nil {
		return false
	}
	fmt.Fprintf(s.stderr, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(s.stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// declined 回報使用者沒有同意：沒做任何事，結束碼 1。
func (s *session) declined() (int, error) {
	fmt.Fprintln(s.stderr, "已取消。")
	return exitError, nil
}

// printRawLine 把 API 的回應壓成一行輸出（JSON Lines），內容仍以 API 的回應為準（CLI-R5）。
func (s *session) printRawLine(raw []byte) {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		raw = buf.Bytes()
	}
	s.printRaw(raw)
}

// bareBili 是沒有帶分P 的 Bilibili 代號（DEL-R13）。
var bareBili = regexp.MustCompile(`^bili:[^/]+$`)

// refMatches 判斷影音代號是否符合使用者給的 <ref>；Bilibili 代號不帶分P 時，符合所有分P。
func refMatches(arg, ref string) bool {
	if ref == arg {
		return true
	}
	return bareBili.MatchString(arg) && strings.HasPrefix(ref, arg+"/p")
}

// target 是解析完成、要處理的影音。
type target struct {
	id  string
	ref string
}

// label 是顯示用的名稱：優先用影音代號。
func (t target) label() string {
	if t.ref != "" {
		return t.ref
	}
	return t.id
}

// resolveTargets 把每個 <ref> 換成影音。任何一個找不到就整批失敗（回 404 not_found）；
// 結果依參數順序並去除重複。非代號的參數當成影音 ID 比對。
func resolveTargets(args []string, candidates []target, notFound string) ([]target, error) {
	var out []target
	seen := map[string]bool{}
	for _, arg := range args {
		n := 0
		for _, c := range candidates {
			if c.id == arg || refMatches(arg, c.ref) {
				n++
				if !seen[c.id] {
					seen[c.id] = true
					out = append(out, c)
				}
			}
		}
		if n == 0 {
			return nil, &apiError{Status: http.StatusNotFound, Code: "not_found", Message: fmt.Sprintf(notFound, arg)}
		}
	}
	return out, nil
}

// itemCandidates 讀完 GET /api/items 的所有頁面，回傳所有影音。
func (s *session) itemCandidates(ctx context.Context) ([]target, error) {
	var all []target
	err := s.walkItemPages(ctx, nil, func(p itemsPage) (bool, error) {
		for _, raw := range p.Items {
			var it struct {
				ID  string `json:"id"`
				Ref string `json:"ref"`
			}
			if err := json.Unmarshal(raw, &it); err != nil {
				return false, fmt.Errorf("回應不是預期的 JSON：%w", err)
			}
			all = append(all, target{id: it.ID, ref: it.Ref})
		}
		return false, nil
	})
	return all, err
}

// trashedItem 是 GET /api/trash 回應中的一部影音。
type trashedItem struct {
	ID      string    `json:"id"`
	Ref     string    `json:"ref"`
	Title   string    `json:"title"`
	PurgeAt time.Time `json:"purge_at"`
}

// fetchTrash 讀取垃圾桶，回傳解析後的清單與原始回應。
func (s *session) fetchTrash(ctx context.Context) ([]trashedItem, []byte, error) {
	raw, _, err := s.be.call(ctx, http.MethodGet, "/api/trash", nil)
	if err != nil {
		return nil, nil, err
	}
	var body struct {
		Items []trashedItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	return body.Items, raw, nil
}

func trashCandidates(items []trashedItem) []target {
	out := make([]target, 0, len(items))
	for _, it := range items {
		out = append(out, target{id: it.ID, ref: it.Ref})
	}
	return out
}

// resolveForRm 解析 items rm 的代號：先查媒體庫；--purge 時再查垃圾桶（已在垃圾桶中的影音直接永久刪除）。
// 所有代號都解析成功才回傳。
func (s *session) resolveForRm(ctx context.Context, args []string, purge bool) ([]target, error) {
	const notFound = "找不到影音 %s。"
	cands, err := s.itemCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if purge {
		// 垃圾桶中的影音不會出現在清單中；清單解析不了時才需要查垃圾桶。
		// 不帶分P 的 Bilibili 代號符合所有分P，有些在媒體庫、有些在垃圾桶，所以一定要一起查。
		_, err := resolveTargets(args, cands, notFound)
		if err != nil || slices.ContainsFunc(args, bareBili.MatchString) {
			items, _, terr := s.fetchTrash(ctx)
			if terr != nil {
				return nil, terr
			}
			cands = append(cands, trashCandidates(items)...)
		}
	}
	return resolveTargets(args, cands, notFound)
}

// parseItemsRm 解析 `items rm <ref>... [--delete-outputs] [--purge] [--yes]`。
func parseItemsRm(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items rm", g)
	purge := fs.Bool("purge", false, "跳過垃圾桶，直接永久刪除")
	deleteOutputs := fs.Bool("delete-outputs", false, "同時刪除沒被改過的輸出檔")
	yes := fs.Bool("yes", false, "不詢問確認")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) == 0 {
		return nil, usagef("items rm 需要至少一個 <ref>。")
	}
	refs := pos
	return &plan{g: g, precheck: requireConfirmable(yes), run: func(ctx context.Context, s *session) (int, error) {
		// 先把所有代號解析完，有任何一個找不到就整批不做。
		targets, err := s.resolveForRm(ctx, refs, *purge)
		if err != nil {
			return 0, err
		}
		if !*yes {
			prompt := fmt.Sprintf("要把 %d 部影音移到垃圾桶嗎？", len(targets))
			if *purge {
				prompt = fmt.Sprintf("要永久刪除 %d 部影音嗎？無法復原。", len(targets))
			}
			if !s.confirm(prompt) {
				return s.declined()
			}
		}
		var body any
		if *deleteOutputs {
			body = map[string]bool{"delete_outputs": true}
		}
		for _, t := range targets {
			if err := s.removeOne(ctx, t, *purge, body); err != nil {
				return 0, fmt.Errorf("%s：%w", t.label(), err)
			}
		}
		return exitOK, nil
	}}, nil
}

// removeOne 移除一部影音：先移到垃圾桶；purge 時再永久刪除，
// 已經在垃圾桶中（404 in_trash）就直接永久刪除。--json 每部影音輸出最後一個 API 回應。
func (s *session) removeOne(ctx context.Context, t target, purge bool, body any) error {
	id := url.PathEscape(t.id)
	raw, _, err := s.be.call(ctx, http.MethodDelete, "/api/items/"+id, body)
	var ae *apiError
	switch {
	case err == nil && !purge:
		if s.g.json {
			s.printRawLine(raw)
		} else {
			fmt.Fprintf(s.stdout, "已移到垃圾桶：%s\n", t.label())
		}
		return nil
	case err == nil:
		// purge：接著永久刪除。
	case purge && errors.As(err, &ae) && ae.Status == http.StatusNotFound && ae.Code == "in_trash":
		// 已經在垃圾桶中：直接永久刪除。
	default:
		return err
	}
	raw, _, err = s.be.call(ctx, http.MethodDelete, "/api/trash/"+id, nil)
	if err != nil {
		return err
	}
	if s.g.json {
		s.printRawLine(raw)
	} else {
		fmt.Fprintf(s.stdout, "已永久刪除：%s\n", t.label())
	}
	return nil
}

// parseItemsRestore 解析 `items restore <ref>...`。
func parseItemsRestore(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items restore", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) == 0 {
		return nil, usagef("items restore 需要至少一個 <ref>。")
	}
	refs := pos
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		items, _, err := s.fetchTrash(ctx)
		if err != nil {
			return 0, err
		}
		targets, err := resolveTargets(refs, trashCandidates(items), "垃圾桶中找不到影音 %s。")
		if err != nil {
			return 0, err
		}
		for _, t := range targets {
			raw, _, err := s.be.call(ctx, http.MethodPost, "/api/trash/"+url.PathEscape(t.id)+"/restore", nil)
			if err != nil {
				return 0, fmt.Errorf("%s：%w", t.label(), err)
			}
			if s.g.json {
				s.printRawLine(raw)
			} else {
				fmt.Fprintf(s.stdout, "已還原：%s\n", t.label())
			}
		}
		return exitOK, nil
	}}, nil
}

// parseTrash 解析 `trash ls` 與 `trash empty [--yes]`。
func parseTrash(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("trash 需要子命令：ls 或 empty。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls":
		return parseTrashLs(rest)
	case "empty":
		return parseTrashEmpty(rest)
	default:
		return nil, usagef("不認得的子命令 trash %s。", sub)
	}
}

func parseTrashLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("trash ls", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("trash ls 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		items, raw, err := s.fetchTrash(ctx)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		for _, it := range items {
			t := target{id: it.ID, ref: it.Ref}
			fmt.Fprintf(s.stdout, "%s\t%s\t剩餘 %d 天\n", t.label(), it.Title, daysLeft(it.PurgeAt, s.clock.Now()))
		}
		return exitOK, nil
	}}, nil
}

// daysLeft 是距離到期還有幾天（進位，最少 0）。
func daysLeft(purgeAt, now time.Time) int {
	d := purgeAt.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}

func parseTrashEmpty(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("trash empty", g)
	yes := fs.Bool("yes", false, "不詢問確認")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("trash empty 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, precheck: requireConfirmable(yes), run: func(ctx context.Context, s *session) (int, error) {
		if !*yes && !s.confirm("要清空垃圾桶嗎？其中所有影音都會永久刪除，無法復原。") {
			return s.declined()
		}
		raw, _, err := s.be.call(ctx, http.MethodDelete, "/api/trash", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRawLine(raw)
			return exitOK, nil
		}
		var body struct {
			Purged int `json:"purged"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已永久刪除 %d 部影音。\n", body.Purged)
		return exitOK, nil
	}}, nil
}
