package steps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

// registerFiles 註冊「檔案」的步驟，句型見 docs/specs/steps.md。
func registerFiles(r *Registry, s *Scenario) {
	r.Step(`the file {string} contains:`, s.fileContainsDoc)
	r.Step(`the file {string} exists`, s.fileExists)
	r.Step(`the file {string} does not exist`, s.fileDoesNotExist)
	r.Step(`the file {string} contains {string}`, s.fileContainsText)
	r.Step(`the file {string} does not contain {string}`, s.fileDoesNotContainText)
	r.Step(`the data directory does not contain the created token`, func() error { return godog.ErrPending })
	r.Step(`the file {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the file {string} has the mode {string}`, s.fileHasMode)
	r.Step(`the cookie file given to yt-dlp has the mode {string}`, s.cookieFileHasMode)
	r.Step(`no cookie file remains in {string}`, s.noCookieFileRemains)
	r.Step(`the data directory does not contain {string}`, s.dataDirDoesNotContain)
	r.Step(`the data directory has no files of {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the media cache has no files of {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the logs do not contain {string}`, func(string) error { return godog.ErrPending })
}

// dataDirDoesNotContain 掃描 /data 下所有檔案（包括 DB 與 WAL），確認都不含 text。
// server 還在執行時 WAL 裡可能有尚未 checkpoint 的內容，也一併掃描。
func (s *Scenario) dataDirDoesNotContain(text string) error {
	needle := []byte(text)
	if len(needle) == 0 {
		return errors.New("要搜尋的文字不能是空的")
	}
	root := s.World.DataDir()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // 掃描期間被刪除的暫存檔
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
		if bytes.Contains(b, needle) {
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(filepath.Join(s.World.Unexpand(root), rel)))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("掃描資料目錄：%w", err)
	}
	if len(found) > 0 {
		return fmt.Errorf("資料目錄中的這些檔案含有 %q：%v", text, found)
	}
	return nil
}

// fileExists 確認檔案存在；虛擬路徑的改寫與 fileDoesNotExist 相同。
func (s *Scenario) fileExists(path string) error {
	_, err := os.Stat(s.World.Expand(path))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("檔案 %s 不存在，預期存在", path)
	default:
		return fmt.Errorf("檢查檔案 %s：%w", path, err)
	}
}

// fileDoesNotExist 確認檔案不存在；虛擬路徑（/data、/config）先改寫成場景根目錄下的實際路徑。
func (s *Scenario) fileDoesNotExist(path string) error {
	real := s.World.Expand(path)
	_, err := os.Stat(real)
	switch {
	case err == nil:
		return fmt.Errorf("檔案 %s 存在，預期不存在", path)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("檢查檔案 %s：%w", path, err)
	}
}

// fileHasMode 確認檔案的權限（例如 "0600"）；虛擬路徑的改寫與 fileExists 相同。
func (s *Scenario) fileHasMode(path, want string) error {
	fi, err := os.Stat(s.World.Expand(path))
	if err != nil {
		return fmt.Errorf("檢查檔案 %s：%w", path, err)
	}
	if got := fmt.Sprintf("%04o", fi.Mode().Perm()); got != want {
		return fmt.Errorf("檔案 %s 的權限是 %s，預期 %s", path, got, want)
	}
	return nil
}

// cookieCalls 回傳帶 --cookies 的假 yt-dlp 呼叫。
func (s *Scenario) cookieCalls() ([]faketool.Call, error) {
	calls, err := s.World.ToolCalls()
	if err != nil {
		return nil, fmt.Errorf("讀取假工具的呼叫紀錄：%w", err)
	}
	var out []faketool.Call
	for _, c := range calls {
		if c.Tool == faketool.YtDlp && c.CookiesFile != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// cookiePath 回傳呼叫收到的 cookie 檔的絕對路徑。
func cookiePath(c faketool.Call) string {
	if filepath.IsAbs(c.CookiesFile) {
		return c.CookiesFile
	}
	return filepath.Join(c.Cwd, c.CookiesFile)
}

// cookieFileHasMode 確認目前執行中、帶 --cookies 的假 yt-dlp 呼叫所收到的 cookie 檔存在，而且權限相符。
// 「執行中」以呼叫紀錄的程序 ID 判斷（被 gate 擋住的程序仍存活，見 `the worker starts one round`）；
// 暫存檔每次呼叫一個檔名（SEC-R7），所以不寫死路徑。沒有這樣的呼叫時失敗：沒有東西可以驗證。
func (s *Scenario) cookieFileHasMode(want string) error {
	calls, err := s.cookieCalls()
	if err != nil {
		return err
	}
	running := 0
	for _, c := range calls {
		if !faketool.PidAlive(c.Pid) {
			continue
		}
		running++
		p := cookiePath(c)
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("執行中的 yt-dlp（pid %d）收到的 cookie 檔 %s 不存在：%w", c.Pid, s.World.Unexpand(p), err)
		}
		if got := fmt.Sprintf("%04o", fi.Mode().Perm()); got != want {
			return fmt.Errorf("執行中的 yt-dlp（pid %d）收到的 cookie 檔 %s 權限是 %s，預期 %s", c.Pid, s.World.Unexpand(p), got, want)
		}
	}
	if running == 0 {
		return fmt.Errorf("沒有執行中、帶 --cookies 的 yt-dlp 呼叫（帶 --cookies 的呼叫共 %d 次）", len(calls))
	}
	return nil
}

// noCookieFileRemains 確認 dir（場景路徑，對應到測試資料目錄）中沒有 cookie 檔：
//   - 曾交給假 yt-dlp 的 cookie 檔都已不存在，而且至少有一個放在這個目錄（否則斷言驗證不到任何東西）。
//   - 目錄中沒有名稱含 cookie 的檔案（cookie 暫存檔的命名）。
func (s *Scenario) noCookieFileRemains(dir string) error {
	real := s.World.Expand(dir)
	calls, err := s.cookieCalls()
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return errors.New("沒有任何帶 --cookies 的 yt-dlp 呼叫，沒有 cookie 檔可以驗證")
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		return fmt.Errorf("檢查目錄 %s：%w", dir, err)
	}
	inDir := false
	for _, c := range calls {
		p := cookiePath(c)
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("cookie 檔 %s 還在（yt-dlp 結束後應立即刪除，SEC-R7）", s.World.Unexpand(p))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("檢查 cookie 檔 %s：%w", s.World.Unexpand(p), err)
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil && parent == want {
			inDir = true
		}
	}
	if !inDir {
		return fmt.Errorf("交給 yt-dlp 的 cookie 檔都不在 %s（SEC-R7 要求放在這個目錄）", dir)
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return fmt.Errorf("讀取目錄 %s：%w", dir, err)
	}
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name()), "cookie") {
			return fmt.Errorf("目錄 %s 中還有 cookie 檔 %s", dir, e.Name())
		}
	}
	return nil
}

// filePath 把場景路徑換成實際路徑：虛擬路徑（/data、/config、/media、/backups）照 World.Expand，
// 其他 /tmp 底下的路徑放到場景根目錄的 tmp/ 之下，不會寫到真正的 /tmp。
func (s *Scenario) filePath(path string) string {
	real := s.World.Expand(path)
	if strings.HasPrefix(real, "/tmp/") && !strings.HasPrefix(real, s.World.Root+string(filepath.Separator)) {
		return filepath.Join(s.World.Root, "tmp", strings.TrimPrefix(real, "/tmp/"))
	}
	return real
}

// fileContainsDoc 建立檔案：內容照字面寫入，上層目錄不存在時自動建立。
func (s *Scenario) fileContainsDoc(path string, doc *godog.DocString) error {
	real := s.filePath(path)
	if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
		return fmt.Errorf("建立 %s 的上層目錄：%w", path, err)
	}
	if err := os.WriteFile(real, []byte(doc.Content), 0o600); err != nil {
		return fmt.Errorf("寫入檔案 %s：%w", path, err)
	}
	if s.fixtureFiles == nil {
		s.fixtureFiles = map[string][]byte{}
	}
	s.fixtureFiles[real] = []byte(doc.Content)
	return nil
}

// maxArchiveBytes 是展開壓縮檔時內容總量的上限（防止測試中的壓縮炸彈）。
const maxArchiveBytes = 256 << 20

// fileBlobs 讀出要搜尋的內容：`.tar.gz`（與 `.tgz`）先 gunzip 再逐一取出每個 tar 項目的內容（檔名不算），
// `.gz` 只 gunzip，其他檔案原樣。壓縮檔解不開就回傳錯誤，不退回掃描壓縮後的原始位元組
// （壓縮後的內容看不到明文，退回去會讓「不含」變成假通過）。
func fileBlobs(real string) ([][]byte, error) {
	raw, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	switch {
	case isTarArchive(real):
		return untar(raw)
	case strings.HasSuffix(strings.ToLower(real), ".gz"):
		b, err := gunzip(raw)
		if err != nil {
			return nil, err
		}
		return [][]byte{b}, nil
	}
	return [][]byte{raw}, nil
}

// isTarArchive 判斷檔名是否為 `.tar.gz` 或 `.tgz`。
func isTarArchive(real string) bool {
	lower := strings.ToLower(real)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

// errEmptyArchive 是封存檔解開後沒有任何一般檔案項目：空的封存檔不能證明「含有」或「不含」任何東西。
func errEmptyArchive(path string) error {
	return fmt.Errorf("封存檔 %s 沒有任何檔案，不能用來判斷內容", path)
}

func gunzip(raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	b, err := io.ReadAll(io.LimitReader(zr, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxArchiveBytes {
		return nil, errors.New("展開後的內容超過上限")
	}
	return b, nil
}

// untar 取出 tar.gz 中每個一般檔案項目的內容。
func untar(raw []byte) ([][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var out [][]byte
	total := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxArchiveBytes+1))
		if err != nil {
			return nil, err
		}
		if total += len(b); total > maxArchiveBytes {
			return nil, errors.New("展開後的內容超過上限")
		}
		out = append(out, b)
	}
}

// fileContainsText 實作 `the file {string} contains {string}`；壓縮檔先解開，任何一個項目包含就成立。
func (s *Scenario) fileContainsText(path, text string) error {
	if text == "" {
		return errors.New("要搜尋的文字不能是空的")
	}
	real := s.filePath(path)
	blobs, err := fileBlobs(real)
	if err != nil {
		return fmt.Errorf("讀取檔案 %s：%w", path, err)
	}
	if isTarArchive(real) && len(blobs) == 0 {
		return errEmptyArchive(path)
	}
	for _, b := range blobs {
		if bytes.Contains(b, []byte(text)) {
			return nil
		}
	}
	return fmt.Errorf("檔案 %s 不含 %q（共 %d 個內容）", path, text, len(blobs))
}

// fileDoesNotContainText 實作 `the file {string} does not contain {string}`；壓縮檔先解開，所有項目都不包含才成立。
// 檔案必須存在：不存在的檔案不能證明任何事。
func (s *Scenario) fileDoesNotContainText(path, text string) error {
	if text == "" {
		return errors.New("要搜尋的文字不能是空的")
	}
	real := s.filePath(path)
	blobs, err := fileBlobs(real)
	if err != nil {
		return fmt.Errorf("讀取檔案 %s：%w", path, err)
	}
	if isTarArchive(real) && len(blobs) == 0 {
		return errEmptyArchive(path)
	}
	for i, b := range blobs {
		if bytes.Contains(b, []byte(text)) {
			return fmt.Errorf("檔案 %s 的第 %d 個內容含有 %q", path, i+1, text)
		}
	}
	return nil
}

// backupBlobs 讀出 /backups 中一個檔案要掃描的內容：場景自己寫入且沒被改過的 fixture 照字面掃描，
// 其他檔案用嚴格模式解開（見 fileBlobs）。
func (s *Scenario) backupBlobs(path string) ([][]byte, error) {
	if want, ok := s.fixtureFiles[path]; ok {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(raw, want) {
			return [][]byte{raw}, nil
		}
	}
	return fileBlobs(path)
}

// checkBackupSecrets 在 After hook 掃描 /backups（OPS-R1）：每個檔案（`.tar.gz` 先解開）的內容都不能含有 master key 的值。
func (s *Scenario) checkBackupSecrets() error {
	needles := map[string]bool{harness.TestMasterKey: true}
	if v := s.World.Env["NOTESTEME_MASTER_KEY"]; len(v) >= 4 {
		needles[v] = true
	}
	root := s.World.BackupsDir()
	var errs []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil // 目錄與 symlink（latest 指向的檔案會另外掃到）
		}
		blobs, err := s.backupBlobs(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			// 解不開的備份是 harness 錯誤：不能退回掃描壓縮後的位元組，否則 master key 外洩也看不到。
			errs = append(errs, fmt.Errorf("harness 錯誤：備份 %s 解不開，無法掃描 master key：%w", s.World.Unexpand(path), err))
			return nil
		}
		for _, b := range blobs {
			for n := range needles {
				if bytes.Contains(b, []byte(n)) {
					errs = append(errs, fmt.Errorf("備份 %s 含有 master key 的值（OPS-R1、NFR-SEC-3）", s.World.Unexpand(path)))
				}
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("掃描 /backups：%w", err))
	}
	return errors.Join(errs...)
}
