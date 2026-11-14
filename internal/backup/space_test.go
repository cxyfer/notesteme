package backup

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// 目的目錄剩餘空間不足標頭宣告的大小時，寫入資料庫之前就拒絕，不留下任何檔案。
func TestExtractRefusesWhenDiskTooSmall(t *testing.T) {
	entries := []entry{{name: ManifestName, body: goodManifest}, {name: DBName, body: strings.Repeat("x", 100)}}
	dir := t.TempDir()
	var asked string
	_, err := Extract(buildTar(t, entries...), dir, Limits{FreeSpace: func(d string) (uint64, error) {
		asked = d
		return 50, nil
	}})
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("錯誤 = %v，預期 ErrInsufficientSpace", err)
	}
	if asked != dir {
		t.Errorf("檢查的目錄 = %q，預期 %q", asked, dir)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("不該留下檔案：%v", es)
	}
}

// 剩餘空間剛好等於宣告的大小時不夠（需要「大於」）；多一個位元組就可以。
func TestExtractFreeSpaceBoundary(t *testing.T) {
	entries := []entry{{name: ManifestName, body: goodManifest}, {name: DBName, body: strings.Repeat("x", 100)}}
	free := func(n uint64) Limits { return Limits{FreeSpace: func(string) (uint64, error) { return n, nil }} }
	if _, err := Extract(buildTar(t, entries...), t.TempDir(), free(100)); !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("剛好相等的錯誤 = %v", err)
	}
	if _, err := Extract(buildTar(t, entries...), t.TempDir(), free(101)); err != nil {
		t.Errorf("空間足夠卻失敗：%v", err)
	}
}

// 無法得知剩餘空間（例如非 unix 平台）時略過檢查，照常解開。
func TestExtractSkipsCheckWhenSpaceUnknown(t *testing.T) {
	entries := []entry{{name: ManifestName, body: goodManifest}, {name: DBName, body: "db"}}
	lim := Limits{FreeSpace: func(string) (uint64, error) { return 0, ErrSpaceUnknown }}
	if _, err := Extract(buildTar(t, entries...), t.TempDir(), lim); err != nil {
		t.Errorf("無法得知空間時不該失敗：%v", err)
	}
}

// 預設的檢查在真實的檔案系統上可以運作（unix 上有答案）。
func TestDefaultFreeSpace(t *testing.T) {
	free, err := freeSpace(t.TempDir())
	if errors.Is(err, ErrSpaceUnknown) {
		t.Skip("這個平台無法查詢剩餘空間")
	}
	if err != nil || free == 0 {
		t.Errorf("freeSpace = %d, %v", free, err)
	}
}
