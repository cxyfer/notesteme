package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
)

// exportFormats 是 `export --format` 接受的格式（規格的介面）；API 另外有 cues-json 與 bilingual-srt，但不屬於 CLI。
var exportFormats = []string{"srt", "vtt", "txt", "ass"}

func parseExport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("export", g)
	format := fs.String("format", "srt", "格式：srt、vtt、txt、ass 或 bundle")
	style := fs.String("style", "", "ASS 樣式名稱（只對 --format ass 有效），預設 default")
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
	if *format == "bundle" {
		// bundle（BND-R1）：沒有語言、變體與樣式。
		if *lang != "" || *variant != "" || *style != "" {
			return nil, usagef("--format bundle 不能搭配 --lang、--variant 或 --style。")
		}
		arg, dst := pos[0], *out
		return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) { return runExportBundle(ctx, s, arg, dst) }}, nil
	}
	if !slices.Contains(exportFormats, *format) {
		return nil, usagef("--format 必須是 srt、vtt、txt 或 ass，收到 %q。", *format)
	}
	if *style != "" && *format != "ass" {
		return nil, usagef("--style 只能搭配 --format ass。")
	}
	arg := pos[0]
	q := url.Values{"format": {*format}}
	if *lang != "" {
		q.Set("lang", *lang)
	}
	if *variant != "" {
		q.Set("variant", *variant)
	}
	if *style != "" {
		q.Set("style", *style)
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

// writeFile 把 r 寫到 path：先寫同目錄的暫存檔，完整寫完才 Rename 蓋過目標。
// 中途失敗（連線中斷、磁碟已滿）時目標檔維持原樣，也不留下暫存檔。
func writeFile(path string, r io.Reader) error {
	dir, base := filepath.Split(path)
	var (
		f   *os.File
		tmp string
		err error
	)
	// 暫存檔名帶亂數並以 O_EXCL 建立；權限與一般檔案相同（受 umask 影響）。
	for range 10 {
		tmp = filepath.Join(dir, fmt.Sprintf(".%s.%08x.tmp", base, rand.Uint32()))
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("無法寫入 %s：%w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	return nil
}
