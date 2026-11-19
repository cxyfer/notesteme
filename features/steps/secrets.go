package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 這個檔案放 cookie 的 secret 掃描（NFR-SEC-3、SEC-R7）：場景用 POST /api/cookies 送出的 cookie 值，
// 不能出現在 server 的 log、假工具的呼叫參數，以及 server 關閉後的 /data（包括 DB）。
// 例外：cookie 暫存檔會被刪除，不在掃描之列；執行中的暫存檔由 `the cookie file given to yt-dlp …` 與
// `no cookie file remains in …` 兩個步驟把關。

// minBareCookieValue 是單獨掃描 cookie 值的最短長度。
// 「fixture」這類測試值很短、也是常見字，單獨掃描容易誤報；所以值太短時只掃「名稱加值」的組合
// （`SESSDATA=fixture`、`SESSDATA<tab>fixture`），值夠長（例如 `secret-sid`）時連單獨的值也掃。
const minBareCookieValue = 8

// cookieNeedles 從 POST /api/cookies 的請求內容取出每個 cookie 的名稱與值，回傳要掃描的字串：
//   - `name=value` 與 `name<tab>value`（標頭格式與 Netscape 格式 cookie 檔的寫法）。
//   - 值本身，但只限長度不小於 minBareCookieValue 的值。
//
// content 可以是 Netscape 格式（tab 分隔的 7 個欄位，#HttpOnly_ 開頭的行也算），
// 或 `name=value; name2=value2` 的標頭格式。body 不是預期的 JSON 時回傳 nil。
func cookieNeedles(body []byte) []string {
	var req struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	var out []string
	add := func(name, value string) {
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name == "" || value == "" {
			return
		}
		out = append(out, name+"="+value, name+"\t"+value)
		if len(value) >= minBareCookieValue {
			out = append(out, value)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(req.Content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if f := strings.Split(strings.TrimPrefix(line, "#HttpOnly_"), "\t"); len(f) == 7 {
			add(f[5], f[6])
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, seg := range strings.Split(line, ";") {
			if name, value, ok := strings.Cut(seg, "="); ok {
				add(name, value)
			}
		}
	}
	return out
}

// noteRequest 記下送給 POST /api/cookies 的 cookie 值，供之後的 secret 掃描使用。
func (s *Scenario) noteRequest(method, path string, body []byte) {
	if method != http.MethodPost || path != "/api/cookies" {
		return
	}
	for _, n := range cookieNeedles(body) {
		if s.cookieSecrets == nil {
			s.cookieSecrets = map[string]bool{}
		}
		s.cookieSecrets[n] = true
	}
}

// checkDataDirSecrets 在 server 關閉之後掃描 /data 的所有檔案（包括 DB 與 WAL），
// 確認沒有場景送出的 cookie 值。
func (s *Scenario) checkDataDirSecrets() error {
	if len(s.cookieSecrets) == 0 {
		return nil
	}
	needles := make([]string, 0, len(s.cookieSecrets))
	for n := range s.cookieSecrets {
		needles = append(needles, n)
	}
	sort.Strings(needles)
	root := s.World.DataDir()
	var errs []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, n := range needles {
			if bytes.Contains(b, []byte(n)) {
				rel, _ := filepath.Rel(root, path)
				errs = append(errs, fmt.Errorf("server 關閉後，資料目錄的 %s 含有 cookie 值 %q（SEC-R7、NFR-SEC-3）", filepath.ToSlash(filepath.Join(s.World.Unexpand(root), rel)), n))
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("掃描資料目錄：%w", err))
	}
	return errors.Join(errs...)
}
