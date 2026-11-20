package config

import (
	"fmt"
	"strings"
	"testing"
)

// 沒有 library 區段時用預設值 30 天。
func TestLoadTrashRetentionDefault(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:1\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrashRetentionDays != DefaultTrashRetentionDays || DefaultTrashRetentionDays != 30 {
		t.Errorf("TrashRetentionDays = %d，預期 30", cfg.TrashRetentionDays)
	}
	if cfg.SourceOf(FieldTrashRetentionDays) != SourceDefault {
		t.Errorf("來源 = %s", cfg.SourceOf(FieldTrashRetentionDays))
	}
}

// library 不是「由資料庫管理的欄位」，也不是未知欄位：設定檔可以寫。邊界值 1 與 3650 都接受。
func TestLoadTrashRetentionFromFile(t *testing.T) {
	for _, days := range []int{1, 7, 3650} {
		cfg, err := Load(Sources{File: writeFile(t, fmt.Sprintf("library:\n  trash_retention_days: %d\n", days)), Env: map[string]string{}})
		if err != nil {
			t.Fatalf("%d 天：%v", days, err)
		}
		if cfg.TrashRetentionDays != days || cfg.SourceOf(FieldTrashRetentionDays) != SourceConfigFile {
			t.Errorf("%d 天：TrashRetentionDays = %d（%s）", days, cfg.TrashRetentionDays, cfg.SourceOf(FieldTrashRetentionDays))
		}
	}
}

// 超出範圍或型別不對時拒絕載入，訊息指出欄位。
func TestLoadRejectsInvalidTrashRetention(t *testing.T) {
	for _, v := range []string{"0", "-1", "3651", "abc", "1.5", "[30]"} {
		_, err := Load(Sources{File: writeFile(t, "library:\n  trash_retention_days: "+v+"\n"), Env: map[string]string{}})
		if err == nil || !strings.Contains(err.Error(), "library.trash_retention_days") {
			t.Errorf("值 %s：err = %v，預期指出 library.trash_retention_days", v, err)
		}
	}
}

// library 的子欄位打錯字時不能被悄悄忽略。
func TestLoadRejectsUnknownLibraryField(t *testing.T) {
	_, err := Load(Sources{File: writeFile(t, "library:\n  trash_retention_day: 30\n"), Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "trash_retention_day") {
		t.Fatalf("err = %v", err)
	}
}
