package cli

// 這個檔案是 M3 的子命令（FRM、NOTE 的「介面」）：frames ls、frames get、note show。
// frames 在 M3-S3、note show 在 M3-S4 實作。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
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
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/items/"+url.PathEscape(item)+"/frames", nil)
		if err != nil {
			return 0, err
		}
		var v struct {
			Frames []struct {
				N        int    `json:"n"`
				At       string `json:"at"`
				Selected bool   `json:"selected"`
				Excluded string `json:"excluded"`
				Caption  string `json:"caption"`
				Chapter  string `json:"chapter"`
			} `json:"frames"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		if g.json {
			if *all {
				s.printRaw(raw)
				return exitOK, nil
			}
			sel := v.Frames[:0]
			for _, f := range v.Frames {
				if f.Selected {
					sel = append(sel, f)
				}
			}
			return exitOK, s.printJSON(map[string]any{"frames": sel})
		}
		// 圖說與章節是不可信的資料：去掉控制字元，一張畫面只佔一行。
		for _, f := range v.Frames {
			if !f.Selected && !*all {
				continue
			}
			mark := " "
			switch {
			case f.Selected:
				mark = "*"
			case f.Excluded != "":
				mark = "x"
			}
			line := fmt.Sprintf("%s %3d  %s  %s", mark, f.N, f.At, oneLine(f.Caption))
			if f.Chapter != "" {
				line += "  [" + oneLine(f.Chapter) + "]"
			}
			fmt.Fprintln(s.stdout, strings.TrimRight(line, " "))
		}
		return exitOK, nil
	}}, nil
}

// oneLine 把換行與控制字元換成空白。
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
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
		if err := writeFile(*out, bytes.NewReader(raw)); err != nil {
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
