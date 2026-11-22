package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// importFormats 是 `import --format` 接受的格式；沒有指定時由 server 依副檔名與內容判斷。
var importFormats = []string{"srt", "vtt", "ass"}

// parseImport 解析 `import <subtitle> --item <item>`（OUT-R13）：讀取本機的字幕檔，連同內容送給 server。
// 檔案由 CLI 讀取，所以 server 不需要看得到這個路徑（遠端的 --server 也可以用）。
func parseImport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("import", g)
	item := fs.String("item", "", "要匯入到哪部影音（ID 或短代號）")
	lang := fs.String("lang", "", "字幕的語言；沒有指定時用影音的原文語言")
	format := fs.String("format", "", "格式：srt、vtt 或 ass；沒有指定時依副檔名與內容判斷")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("import 需要一個 <subtitle> 檔案。")
	}
	if *item == "" {
		return nil, usagef("import 需要 --item <item>。")
	}
	if *format != "" && !slices.Contains(importFormats, *format) {
		return nil, usagef("--format 必須是 srt、vtt 或 ass，收到 %q。", *format)
	}
	path, arg, langTag, fmtName := pos[0], *item, *lang, *format
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		data, err := readSubtitleFile(path)
		if err != nil {
			return 0, err
		}
		id, err := s.resolveItem(ctx, arg)
		if err != nil {
			return 0, err
		}
		body := map[string]string{"content": string(data), "name": filepath.Base(path)}
		if langTag != "" {
			body["lang"] = langTag
		}
		if fmtName != "" {
			body["format"] = fmtName
		}
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/items/"+url.PathEscape(id)+"/subtitles/import", body)
		if err != nil {
			return 0, err
		}
		if s.g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var res struct {
			Cues   int    `json:"cues"`
			Format string `json:"format"`
			Lang   string `json:"lang"`
			Stale  bool   `json:"stale_translation"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已匯入 %d 句（%s，%s），成為新的 edited 版本。\n", res.Cues, res.Format, res.Lang)
		if res.Stale {
			fmt.Fprintf(s.stdout, "%s；可以執行 notesteme items rerun %s --from translate。\n", pipeline.MsgSubtitlesUpdated, arg)
		}
		return exitOK, nil
	}}, nil
}

// readSubtitleFile 讀取字幕檔：只收一般檔案，大小不能超過 server 的上限（讀完整份再送出會浪費傳輸）。
func readSubtitleFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("無法開啟 %s：%w", path, errUnwrapPath(err))
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, errUnwrapPath(err))
	}
	if !fi.Mode().IsRegular() {
		return nil, usagef("%s 不是一般檔案。", path)
	}
	if fi.Size() > pipeline.MaxImportBytes {
		return nil, usagef("%s 超過 %d MiB 的上限。", path, pipeline.MaxImportBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, pipeline.MaxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, errUnwrapPath(err))
	}
	if len(data) > pipeline.MaxImportBytes {
		return nil, usagef("%s 超過 %d MiB 的上限。", path, pipeline.MaxImportBytes>>20)
	}
	return data, nil
}

// errUnwrapPath 取出 *PathError 內部的錯誤，訊息才不會再重複一次路徑。
func errUnwrapPath(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}
