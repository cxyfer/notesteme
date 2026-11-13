package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
)

// exportFormats 是 `export --format` 接受的格式（規格的介面）；API 另外有 cues-json，但不屬於 CLI。
var exportFormats = []string{"srt", "vtt", "txt"}

func parseExport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("export", g)
	format := fs.String("format", "srt", "格式：srt、vtt 或 txt")
	lang := fs.String("lang", "", "語言")
	variant := fs.String("variant", "", "字幕變體")
	out := fs.String("out", "", "輸出到檔案；沒有指定時輸出到 stdout")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("export 需要一個 <item>。")
	}
	if !slices.Contains(exportFormats, *format) {
		return nil, usagef("--format 必須是 srt、vtt 或 txt，收到 %q。", *format)
	}
	arg := pos[0]
	q := url.Values{"format": {*format}}
	if *lang != "" {
		q.Set("lang", *lang)
	}
	if *variant != "" {
		q.Set("variant", *variant)
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		id, err := s.resolveItem(ctx, arg)
		if err != nil {
			return 0, err
		}
		resp, err := s.be.open(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id)+"/subtitles?"+q.Encode(), nil)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return 0, errorFromResponse(resp.StatusCode, raw)
		}
		if *out == "" {
			_, err := io.Copy(s.stdout, resp.Body)
			return exitOK, err
		}
		if err := writeFile(*out, resp.Body); err != nil {
			return 0, err
		}
		fmt.Fprintf(s.stdout, "已輸出到 %s\n", *out)
		return exitOK, nil
	}}, nil
}

// writeFile 把 r 寫到 path；寫入失敗時不留下不完整的檔案。
func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("無法寫入 %s：%w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	return nil
}
