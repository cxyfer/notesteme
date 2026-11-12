package steps

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// registerFiles 註冊「檔案」的步驟，句型見 docs/specs/steps.md。
func registerFiles(r *Registry, s *Scenario) {
	r.Step(`the file {string} contains:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the file {string} exists`, func(string) error { return godog.ErrPending })
	r.Step(`the file {string} does not exist`, s.fileDoesNotExist)
	r.Step(`the file {string} contains {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the file {string} does not contain {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`the data directory does not contain the created token`, func() error { return godog.ErrPending })
	r.Step(`the file {string} is:`, func(string, *godog.DocString) error { return godog.ErrPending })
	r.Step(`the file {string} has the mode {string}`, func(string, string) error { return godog.ErrPending })
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
