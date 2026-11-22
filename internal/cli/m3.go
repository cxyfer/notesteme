package cli

// 這個檔案是 M3 的子命令（FRM、NOTE 的「介面」）：frames ls、frames get、note show。
// M3-S0 先接上參數與 API 路由：路由目前是 501 stub，所以會以「尚未實作」結束；
// 文字輸出的格式由 FRM、NOTE 的切片補上，在那之前成功時原樣輸出 API 的回應。

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func parseFrames(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("frames 需要子命令：ls 或 get。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls":
		return parseFramesLs(rest)
	case "get":
		return parseFramesGet(rest)
	default:
		return nil, usagef("不認得的子命令 frames %s。", sub)
	}
}

// frames ls <item> [--all] [--json]（FRM-R10）：預設只列選中的畫面。
func parseFramesLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("frames ls", g)
	all := fs.Bool("all", false, "列出全部候選畫面，不只選中的")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("frames ls 需要一個影音：notesteme frames ls <item>。")
	}
	item := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		q := url.Values{}
		if *all {
			q.Set("selected_only", "false")
		}
		path := "/api/items/" + url.PathEscape(item) + "/frames"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		raw, _, err := s.be.call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return 0, err
		}
		s.printRaw(raw)
		return exitOK, nil
	}}, nil
}

// frames get <item> <n> -o <file>（FRM-R10）：寫出原圖。
func parseFramesGet(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("frames get", g)
	out := fs.StringP("out", "o", "", "寫出圖片的檔案")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 2 {
		return nil, usagef("frames get 需要影音與畫面編號：notesteme frames get <item> <n> -o <file>。")
	}
	n, err := strconv.Atoi(pos[1])
	if err != nil || n < 1 {
		return nil, usagef("畫面編號必須是從 1 起算的整數，收到 %q。", pos[1])
	}
	if *out == "" {
		return nil, usagef("frames get 需要 -o <file>。")
	}
	item := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items/"+url.PathEscape(item)+"/frames/"+strconv.Itoa(n)+"?size=full", nil)
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(*out, raw, 0o644); err != nil {
			return 0, err
		}
		return exitOK, nil
	}}, nil
}

func parseNote(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("note 需要子命令：show。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "show":
		return parseNoteShow(rest)
	default:
		return nil, usagef("不認得的子命令 note %s。", sub)
	}
}

// note show <item> [--lang] [--format md|json]（NOTE-R13）：預設輸出 Markdown。
func parseNoteShow(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("note show", g)
	lang := fs.String("lang", "", "筆記的語言；沒有指定時用預設的筆記語言")
	format := fs.String("format", "md", "輸出格式：md 或 json")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("note show 需要一個影音：notesteme note show <item>。")
	}
	if *format != "md" && *format != "json" {
		return nil, usagef("--format 必須是 md 或 json，收到 %q。", *format)
	}
	item := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		q := url.Values{}
		if *lang != "" {
			q.Set("lang", *lang)
		}
		if *format == "md" && !g.json {
			q.Set("format", "md")
		}
		path := "/api/items/" + url.PathEscape(item) + "/note"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		raw, _, err := s.be.call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return 0, err
		}
		s.printRaw(raw)
		return exitOK, nil
	}}, nil
}
