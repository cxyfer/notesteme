package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/configio"
	"github.com/cxyfer/notesteme/internal/validate"
)

// parseConfig 解析 `notesteme config export | import`（SEC-R10）。
//
// 兩者都經由 API（和其他子命令一樣，獨立、本機 client、--server 三種模式都可用）：
// export 需要 read、import 需要 admin（本機模式是 owner，兩者都有；--server 用 token 的 scope）。
// YAML 的解析與驗證都在 server 端，CLI 只讀檔、送出與顯示。
func parseConfig(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("config 需要子命令：export 或 import。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "export":
		return parseConfigExport(rest)
	case "import":
		return parseConfigImport(rest)
	default:
		return nil, usagef("不認得的子命令 config %s。", sub)
	}
}

func parseConfigExport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("config export", g)
	var out string
	// --out 與 export、backup 一致；--output 是同一個旗標的別名。
	fs.StringVar(&out, "out", "", "輸出到檔案；沒有指定時輸出到 stdout")
	fs.StringVar(&out, "output", "", "同 --out")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("config export 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/config/export", nil)
		if err != nil {
			return 0, err
		}
		var body struct {
			Content  string   `json:"content"`
			Profiles []string `json:"profiles"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		if out == "" {
			if g.json {
				s.printRaw(raw)
				return exitOK, nil
			}
			// stdout 只有 YAML 內容，可以直接轉向到檔案。
			_, err := io.WriteString(s.stdout, body.Content)
			return exitOK, err
		}
		if err := writeFile(out, strings.NewReader(body.Content)); err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		fmt.Fprintf(s.stdout, "已匯出 %d 個處理方案到 %s\n", len(body.Profiles), out)
		return exitOK, nil
	}}, nil
}

func parseConfigImport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("config import", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || strings.TrimSpace(pos[0]) == "" {
		return nil, usagef("config import 需要一個 <file>。")
	}
	path := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		data, err := readImportFile(path)
		if err != nil {
			return 0, err
		}
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/config/import", map[string]string{"content": string(data)})
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity && ae.Code == errCodeConfigInvalid {
				return 0, newImportRejected(ae, raw)
			}
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var res struct {
			Created        []string `json:"created"`
			Updated        []string `json:"updated"`
			Unchanged      []string `json:"unchanged"`
			DefaultProfile string   `json:"default_profile"`
			DefaultChanged bool     `json:"default_changed"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已匯入：建立 %d、更新 %d、未變更 %d 個處理方案。\n", len(res.Created), len(res.Updated), len(res.Unchanged))
		for _, grp := range []struct {
			label string
			names []string
		}{{"建立", res.Created}, {"更新", res.Updated}, {"未變更", res.Unchanged}} {
			if len(grp.names) > 0 {
				fmt.Fprintf(s.stdout, "  %s：%s\n", grp.label, strings.Join(grp.names, "、"))
			}
		}
		switch {
		case res.DefaultProfile == "":
		case res.DefaultChanged:
			fmt.Fprintf(s.stdout, "預設處理方案：已設為 %s\n", res.DefaultProfile)
		default:
			fmt.Fprintf(s.stdout, "預設處理方案：%s（沒有變更）\n", res.DefaultProfile)
		}
		return exitOK, nil
	}}, nil
}

// readImportFile 讀取要匯入的檔案。大小上限同 server（configio.MaxBytes），超過就不送出：
// 多讀 1 個位元組判斷，不會把任意大的檔案讀進記憶體。
func readImportFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err // 訊息已經有路徑，不重複
		}
		return nil, fmt.Errorf("無法讀取 %s：%w", path, err)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("%s 是資料夾，不是 YAML 檔案。", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, configio.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, err)
	}
	if len(data) > configio.MaxBytes {
		return nil, fmt.Errorf("%s 太大，上限是 %d 位元組（1 MiB）。", path, configio.MaxBytes)
	}
	return data, nil
}

// importRejected 是 server 判定匯入內容不正確（422）：整份都沒有套用，Fields 列出每個錯誤的欄位路徑。
//
// 結束碼是 1，不是 CLI-R6 對 API 422 規定的 2：檔案的內容錯誤不是命令列參數的錯誤，
// 而且 SEC-R10 的場景要求 1（見 SEC 規格的待決問題）。
type importRejected struct {
	Message string
	Fields  []validate.FieldError
}

// newImportRejected 從 422 回應解出逐欄位的錯誤；格式不是預期的也不失敗，退回只有訊息。
func newImportRejected(ae *apiError, raw []byte) *importRejected {
	r := &importRejected{Message: ae.Message}
	var body struct {
		Errors []validate.FieldError `json:"errors"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &body) == nil {
		r.Fields = body.Errors
	}
	return r
}

// Error 逐行列出錯誤。欄位路徑含有檔案中的鍵（不可信的輸入），輸出到終端機之前一律跳脫控制字元：
// 否則 `"\e]52;…": 1` 這樣的鍵可以把 ANSI／OSC 序列送進使用者的終端機，含換行的鍵也能偽造出另一行錯誤。
func (e *importRejected) Error() string {
	var b strings.Builder
	b.WriteString(escapeControl(e.Message))
	for _, f := range e.Fields {
		b.WriteString("\n  ")
		if f.Path != "" {
			b.WriteString(escapeControl(f.Path))
			b.WriteString("：")
		}
		b.WriteString(escapeControl(f.Message))
	}
	return b.String()
}

// escapeControl 把控制字元（C0、DEL、C1）與格式字元（雙向覆寫、零寬字元等）換成 \xNN、\uNNNN。
func escapeControl(s string) string {
	if !strings.ContainsFunc(s, isUnsafeRune) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case !isUnsafeRune(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

func isUnsafeRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

// errCodeConfigInvalid 與 API 的 CodeConfigInvalid 相同。
const errCodeConfigInvalid = "config_invalid"
