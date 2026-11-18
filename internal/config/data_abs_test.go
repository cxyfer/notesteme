package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pathguard"
)

// 相對的資料目錄（NOTESTEME_DATA=./data）要在載入時轉成絕對路徑，
// 否則預設的 inbox 也是相對路徑，會被 pathguard 靜默忽略，inbox 內的檔案一律 403。
func TestRelativeDataBecomesAbsolute(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:1\n"), Env: map[string]string{EnvData: "./data"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "data"); cfg.Data != want {
		t.Errorf("Data = %q，應為 %q", cfg.Data, want)
	}
	if want := filepath.Join(wd, "data", "inbox"); cfg.Inbox != want {
		t.Errorf("Inbox = %q，應為 %q", cfg.Inbox, want)
	}
	// 預設 inbox 必須真的被 pathguard 接受為根目錄。
	if err := pathguard.New([]string{cfg.Inbox}).Check(filepath.Join(cfg.Inbox, "a.mp3")); err != nil {
		t.Errorf("inbox 底下的檔案應通過：%v", err)
	}
}

// 設定檔中的相對 data 同樣轉成絕對路徑；來源標記不變。
func TestRelativeDataInFileBecomesAbsolute(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Sources{File: writeFile(t, "data: rel/data\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "rel", "data"); cfg.Data != want {
		t.Errorf("Data = %q，應為 %q", cfg.Data, want)
	}
	if cfg.SourceOf(FieldData) != SourceConfigFile {
		t.Errorf("來源 = %s", cfg.SourceOf(FieldData))
	}
}

// 檔案中指定的 inbox 仍然要求絕對路徑。
func TestRelativeInboxStillRejected(t *testing.T) {
	if _, err := Load(Sources{File: writeFile(t, "inbox: rel/inbox\n"), Env: map[string]string{}}); err == nil {
		t.Fatal("相對的 inbox 應被拒絕")
	}
}
