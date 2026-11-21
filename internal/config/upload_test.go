package config

import (
	"strings"
	"testing"
)

// 沒有設定時上傳上限為 4 GiB，來源是預設值。
func TestLoadUploadMaxBytesDefault(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:1\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UploadMaxBytes != 4294967296 || DefaultUploadMaxBytes != 4294967296 {
		t.Errorf("UploadMaxBytes = %d，預期 4294967296", cfg.UploadMaxBytes)
	}
	if cfg.SourceOf(FieldUploadMaxBytes) != SourceDefault {
		t.Errorf("來源 = %s", cfg.SourceOf(FieldUploadMaxBytes))
	}
}

// 設定檔可以覆寫上傳上限，且不影響同區段的其他欄位。
func TestLoadUploadMaxBytesFromFile(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "library:\n  upload_max_bytes: 1048576\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UploadMaxBytes != 1048576 || cfg.SourceOf(FieldUploadMaxBytes) != SourceConfigFile {
		t.Errorf("UploadMaxBytes = %d（%s）", cfg.UploadMaxBytes, cfg.SourceOf(FieldUploadMaxBytes))
	}
	if cfg.TrashRetentionDays != DefaultTrashRetentionDays {
		t.Errorf("TrashRetentionDays = %d，預期維持預設", cfg.TrashRetentionDays)
	}
}

// 小於等於 0 或不是整數時拒絕載入，訊息指出欄位。
func TestLoadRejectsInvalidUploadMaxBytes(t *testing.T) {
	for _, v := range []string{"0", "-1", "abc", "1.5", "[1]", "99999999999999999999"} {
		_, err := Load(Sources{File: writeFile(t, "library:\n  upload_max_bytes: "+v+"\n"), Env: map[string]string{}})
		if err == nil || !strings.Contains(err.Error(), "library.upload_max_bytes") {
			t.Errorf("值 %s：err = %v，預期指出 library.upload_max_bytes", v, err)
		}
	}
}
