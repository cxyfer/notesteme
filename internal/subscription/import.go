package subscription

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// 匯入的上限（SUB-R7）。
const (
	MaxImportBytes   = 1 << 20
	MaxImportEntries = 500
	// maxImportDepth 是 OPML 元素巢狀深度的上限（同 SUB-R11 的 feed）。
	maxImportDepth = 64
)

// 匯入整份拒絕時的錯誤碼與訊息（SUB-R7，逐字）。
const (
	CodeImportTooLarge  = "too_large"
	CodeImportTooMany   = "too_many"
	CodeImportFormat    = "invalid_format"
	msgImportTooLarge   = "匯入檔超過大小上限（1 MiB）。"
	msgImportTooMany    = "匯入檔最多 500 個訂閱，這個檔案有 %d 個。"
	msgImportFormat     = "無法辨識匯入檔的格式。支援 OPML 與 Google Takeout 的 subscriptions.csv。"
	msgImportBadRow     = "這一列的欄位數不正確。"
	msgImportURLMissing = "這一列沒有網址。"
)

// takeoutHeader 是 Google Takeout 的 subscriptions.csv 的表頭。
var takeoutHeader = []string{"Channel Id", "Channel Url", "Channel Title"}

// ImportError 是整份檔案被拒絕（大小、數量、格式）；這時不建立任何訂閱。
type ImportError struct{ Code, Message string }

func (e *ImportError) Error() string { return e.Message }

// ImportFailure 是無法建立的一列。
type ImportFailure struct {
	Line    int    `json:"line" doc:"檔案中的行號（從 1 起算）"`
	URL     string `json:"url" doc:"檔案中的網址（原文）"`
	Message string `json:"message"`
}

// ImportResult 是匯入的結果（SUB-R7）。
type ImportResult struct {
	Created    int             `json:"created" doc:"建立的訂閱數"`
	Duplicates []string        `json:"duplicates" doc:"檔案內重複或已經訂閱的網址（原文，依檔案順序）"`
	Failed     []ImportFailure `json:"failed"`
}

// importRow 是檔案中的一個訂閱。url 是建立用的網址；shown 是回報用的原文。
type importRow struct {
	line        int
	url, shown  string
	title       string
	failMessage string
}

// parseImport 依內容判斷格式（OPML 或 Google Takeout 的 subscriptions.csv）並取出每一列；
// 整份檔案的錯誤（大小、格式、數量）在這裡判定，回傳 *ImportError。
func parseImport(data []byte) ([]importRow, error) {
	if len(data) > MaxImportBytes {
		return nil, ImportTooLarge()
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return nil, formatError()
	}
	var (
		rows []importRow
		err  error
	)
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) > 0 && t[0] == '<' {
		rows, err = parseOPML(data)
	} else {
		rows, err = parseTakeout(data)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxImportEntries {
		return nil, &ImportError{Code: CodeImportTooMany, Message: fmt.Sprintf(msgImportTooMany, len(rows))}
	}
	return rows, nil
}

// ImportTooLarge 是匯入檔超過大小上限的錯誤（API 在讀完本體之前就超過時也用它）。
func ImportTooLarge() *ImportError {
	return &ImportError{Code: CodeImportTooLarge, Message: msgImportTooLarge}
}

func formatError() error { return &ImportError{Code: CodeImportFormat, Message: msgImportFormat} }

// parseOPML 取出每個有 xmlUrl 的 outline（含分類 outline 底下的）。任何 directive（<!DOCTYPE>、<!ENTITY>）
// 整份拒絕、不展開 entity（SUB-R11）；根元素必須是 opml；巢狀深度上限 64；只接受 UTF-8。
func parseOPML(data []byte) ([]importRow, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	var (
		rows  []importRow
		depth int
		root  bool
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, formatError()
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, formatError()
		case xml.StartElement:
			depth++
			if depth > maxImportDepth {
				return nil, formatError()
			}
			if depth == 1 {
				if t.Name.Local != "opml" || root {
					return nil, formatError()
				}
				root = true
				continue
			}
			if t.Name.Local != "outline" {
				continue
			}
			var r importRow
			for _, a := range t.Attr {
				switch strings.ToLower(a.Name.Local) {
				case "xmlurl":
					r.url = strings.TrimSpace(a.Value)
				case "text":
					if r.title == "" {
						r.title = a.Value
					}
				case "title":
					r.title = a.Value
				}
			}
			if r.url == "" {
				continue
			}
			r.shown = r.url
			r.line, _ = d.InputPos()
			rows = append(rows, r)
		case xml.EndElement:
			depth--
		}
	}
	if !root {
		return nil, formatError()
	}
	return rows, nil
}

// parseTakeout 讀 Google Takeout 的 subscriptions.csv：表頭必須是 Channel Id,Channel Url,Channel Title，
// 每列是一個 YouTube 頻道；網址以 Channel Id 組成（不採用檔案中的 http 網址）。
func parseTakeout(data []byte) ([]importRow, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	head, err := r.Read()
	if err != nil || len(head) != len(takeoutHeader) {
		return nil, formatError()
	}
	for i, h := range head {
		if strings.TrimSpace(h) != takeoutHeader[i] {
			return nil, formatError()
		}
	}
	var rows []importRow
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, formatError()
		}
		line, _ := r.FieldPos(0)
		row := importRow{line: line}
		if len(rec) != len(takeoutHeader) {
			row.failMessage = msgImportBadRow
			if len(rec) > 1 {
				row.shown = strings.TrimSpace(rec[1])
			}
			rows = append(rows, row)
			continue
		}
		id, u, title := strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1]), rec[2]
		row.shown, row.title = u, title
		switch {
		case id != "":
			row.url = "https://www.youtube.com/channel/" + id
			if row.shown == "" {
				row.shown = row.url
			}
		case u != "":
			row.url = u
		default:
			row.failMessage = msgImportURLMissing
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Import 匯入 OPML 或 Google Takeout 的訂閱（SUB-R7）：整份檔案的錯誤不建立任何訂閱；之後逐列以 Create 建立
// （同樣的辨識與重複檢查），一律 backfill: none、預設處理方案與預設過濾，標題暫用檔案中的標題。
// 檔案內重複與已經訂閱的列在 duplicates，無法辨識的列在 failed，不影響其他列，已建立的不回滾。
// 匯入時不連外：內部網路的 feed 網址在檢查時才由 netguard 擋下（SUB-R11）。
func (s *Service) Import(ctx context.Context, data []byte) (ImportResult, error) {
	rows, err := parseImport(data)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Duplicates: []string{}, Failed: []ImportFailure{}}
	seen := map[string]bool{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if row.failMessage != "" {
			res.Failed = append(res.Failed, ImportFailure{Line: row.line, URL: row.shown, Message: row.failMessage})
			continue
		}
		var src Source
		err := error(&URLError{Code: CodeUnsupportedURL, Message: msgURLTooLong})
		if len(row.url) <= MaxURLLen {
			src, err = Recognize(row.url)
		}
		if err != nil {
			res.Failed = append(res.Failed, ImportFailure{Line: row.line, URL: row.shown, Message: failureMessage(err)})
			continue
		}
		key := src.Kind + "|" + src.Platform + "|" + src.ExternalID
		if seen[key] {
			res.Duplicates = append(res.Duplicates, row.shown)
			continue
		}
		seen[key] = true
		_, err = s.Create(ctx, CreateInput{URL: row.url, title: CleanText(row.title)})
		var (
			dup *DuplicateError
			ue  *URLError
			fe  *FieldsError
		)
		switch {
		case err == nil:
			res.Created++
		case errors.As(err, &dup):
			res.Duplicates = append(res.Duplicates, row.shown)
		case errors.As(err, &ue), errors.As(err, &fe):
			res.Failed = append(res.Failed, ImportFailure{Line: row.line, URL: row.shown, Message: failureMessage(err)})
		default:
			return res, err
		}
	}
	return res, nil
}

// failureMessage 是單列失敗的訊息：只用固定的訊息，不回顯檔案的內容。
func failureMessage(err error) string {
	var (
		ue *URLError
		fe *FieldsError
	)
	switch {
	case errors.As(err, &ue):
		return ue.Message
	case errors.As(err, &fe) && len(fe.Fields) > 0:
		return fe.Fields[0].Message
	}
	return msgUnsupportedURL
}
