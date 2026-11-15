package steps

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案放刪除與垃圾桶（DEL）的步驟：垃圾桶的內容、媒體快取、資料目錄中的殘留檔案與成本帳本。
// 步驟的註冊是 registerDelete，句型見 docs/specs/steps.md 4.4 與 4.5。

// registerDelete 註冊刪除與垃圾桶的步驟。
func registerDelete(r *Registry, s *Scenario) {
	r.Step(`the media of {string} is cached`, s.mediaIsCached)
	r.Step(`the trash contains {string}`, s.trashContains)
	r.Step(`the trash does not contain {string}`, s.trashDoesNotContain)
	r.Step(`the trash contains {int} items`, s.trashContainsCount)
	r.Step(`the cost ledger has an entry of {string} USD for {string}`, s.costLedgerHasEntryFor)
	r.Step(`the cost ledger has an entry of {string} USD titled {string}`, s.costLedgerHasEntryTitled)
	r.Step(`the media cache has no files of {string}`, s.mediaCacheHasNoFilesOf)
	r.Step(`the data directory has no files of {string}`, s.dataDirHasNoFilesOf)
}

// ---- 垃圾桶 ----

// trashRefs 讀 GET /api/trash，回傳垃圾桶中的影音代號。API 回 501 時是 pending（路由屬於還沒做的切片）。
func (s *Scenario) trashRefs() ([]string, error) {
	var out struct {
		Items []struct {
			Ref string `json:"ref"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := s.apiJSON(http.MethodGet, "/api/trash", nil, &out); err != nil {
		return nil, err
	}
	refs := make([]string, len(out.Items))
	for i, it := range out.Items {
		refs[i] = it.Ref
	}
	return refs, nil
}

func (s *Scenario) trashContains(ref string) error {
	refs, err := s.trashRefs()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r == ref {
			return nil
		}
	}
	if s.previousRequestNotImplemented() {
		return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
	}
	return fmt.Errorf("垃圾桶沒有影音 %q（共 %d 部：%v）", ref, len(refs), refs)
}

func (s *Scenario) trashDoesNotContain(ref string) error {
	refs, err := s.trashRefs()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if r == ref {
			if s.previousRequestNotImplemented() {
				return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
			}
			return fmt.Errorf("垃圾桶還有影音 %q", ref)
		}
	}
	return nil
}

func (s *Scenario) trashContainsCount(want int) error {
	refs, err := s.trashRefs()
	if err != nil {
		return err
	}
	if len(refs) != want {
		if s.previousRequestNotImplemented() {
			return fmt.Errorf("最近的請求還沒有實作：%w", godog.ErrPending)
		}
		return fmt.Errorf("垃圾桶有 %d 部影音，預期 %d：%v", len(refs), want, refs)
	}
	return nil
}

// ---- 媒體快取與資料目錄 ----

// mediaIsCached 在影音的媒體快取目錄（pipeline.MediaCacheDir）放一個檔案，模擬已下載的媒體。
func (s *Scenario) mediaIsCached(ref string) error {
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	dir := pipeline.MediaCacheDir(s.World.DataDir(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "media.mp4"), []byte("cached media of "+ref), 0o644)
}

// externalIDOf 取出影音代號中的平台 ID：yt:<id>、bili:<bvid>/p<n> 的 BV 號、pod:<feed>/<guid> 的 guid、local:<檔名>。
func externalIDOf(ref string) string {
	_, rest, _ := strings.Cut(ref, ":")
	switch {
	case strings.HasPrefix(ref, "bili:"):
		bvid, _, _ := strings.Cut(rest, "/p")
		return bvid
	case strings.HasPrefix(ref, "pod:"):
		_, guid, _ := strings.Cut(rest, "/")
		return guid
	}
	return rest
}

// idOfRef 回傳影音代號對應的影音 ID：影音永久刪除之後 API 查不到，所以用新增時記下的對照表（World.Refs）。
// 沒有記下就失敗：不知道影音的 ID，就不能證明沒有它的檔案，不能空洞地通過。
func (s *Scenario) idOfRef(ref string) (string, error) {
	id, ok := s.World.Refs[ref]
	if !ok || id == "" {
		return "", fmt.Errorf("場景沒有記下影音 %q 的 ID（新增時的回應），無法確認它的檔案已經清除", ref)
	}
	return id, nil
}

// leftovers 掃描 root 之下的檔案與目錄，回傳相對路徑含有任何一個 needle 的項目。root 不存在時沒有項目。
func leftovers(root string, needles ...string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // 目錄不存在，或掃描期間被刪除
			}
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		for _, n := range needles {
			if n != "" && strings.Contains(rel, n) {
				found = append(found, rel)
				break
			}
		}
		return nil
	})
	return found, err
}

// mediaCacheHasNoFilesOf 確認 <data>/media 之下沒有路徑含這部影音的 ID 或平台 ID 的項目。
func (s *Scenario) mediaCacheHasNoFilesOf(ref string) error {
	id, err := s.idOfRef(ref)
	if err != nil {
		return err
	}
	found, err := leftovers(filepath.Join(s.World.DataDir(), "media"), id, externalIDOf(ref))
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("媒體快取還有 %s 的項目：%v", ref, found)
	}
	return nil
}

// dataDirDeletedSubdirs 是永久刪除後不能留下影音檔案的資料目錄（DEL-R6）。
var dataDirDeletedSubdirs = []string{"artifacts", "media", "uploads", "tmp"}

// dataDirHasNoFilesOf 確認 artifacts、media、uploads、tmp 之下沒有路徑含這部影音 ID 的項目。
func (s *Scenario) dataDirHasNoFilesOf(ref string) error {
	id, err := s.idOfRef(ref)
	if err != nil {
		return err
	}
	var all []string
	for _, sub := range dataDirDeletedSubdirs {
		found, err := leftovers(filepath.Join(s.World.DataDir(), sub), id)
		if err != nil {
			return err
		}
		for _, f := range found {
			all = append(all, filepath.Join(sub, f))
		}
	}
	if len(all) > 0 {
		return fmt.Errorf("資料目錄還有 %s 的項目：%v", ref, all)
	}
	return nil
}

// ---- 成本帳本 ----

// parseUSD 解析金額，例如 "0.42"。
func parseUSD(v string) (float64, error) {
	usd, err := strconv.ParseFloat(v, 64)
	if err != nil || usd < 0 {
		return 0, fmt.Errorf("金額 %q 不是不小於 0 的數字", v)
	}
	return usd, nil
}

// pendingIfNotImplemented 把 production 的 *pipeline.NotImplementedError（功能屬於還沒做的切片）轉成 pending。
func pendingIfNotImplemented(err error) error {
	var ni *pipeline.NotImplementedError
	if errors.As(err, &ni) {
		return fmt.Errorf("%v：%w", err, godog.ErrPending)
	}
	return err
}

// costLedgerHasEntryFor 在成本帳本寫入一筆這部影音實際花費的金額（透過 app.Server 的測試用 hook，ADR-0015）。
func (s *Scenario) costLedgerHasEntryFor(amount, ref string) error {
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	return pendingIfNotImplemented(s.server.SeedCost(s.ctx, id, usd, "actual"))
}

// costLedgerHasEntryTitled 確認成本帳本中有一筆這個金額、item_title 為指定標題的紀錄。
func (s *Scenario) costLedgerHasEntryTitled(amount, title string) error {
	usd, err := parseUSD(amount)
	if err != nil {
		return err
	}
	if err := s.prepareOwner(); err != nil {
		return err
	}
	entries, err := s.server.CostEntries(s.ctx)
	if err != nil {
		return pendingIfNotImplemented(err)
	}
	for _, e := range entries {
		if math.Abs(e.USD-usd) < 1e-9 && e.ItemTitle == title {
			return nil
		}
	}
	return fmt.Errorf("成本帳本沒有「%s USD、標題 %q」的紀錄（共 %d 筆：%+v）", amount, title, len(entries), entries)
}
