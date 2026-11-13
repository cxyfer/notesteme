package config

import (
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadAllowPrivate(t *testing.T) {
	p := writeFile(t, "network:\n  allow_private: [rsshub.lan, 192.168.1.0/24, 10.0.0.5, NAS.Home.]\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"rsshub.lan", "nas.home"}; !slices.Equal(cfg.AllowPrivate.Hosts, want) {
		t.Errorf("hosts = %v, want %v", cfg.AllowPrivate.Hosts, want)
	}
	want := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("10.0.0.5/32")}
	if !slices.Equal(cfg.AllowPrivate.Prefixes, want) {
		t.Errorf("prefixes = %v, want %v", cfg.AllowPrivate.Prefixes, want)
	}
}

func TestLoadAllowPrivateDefaultsToEmpty(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:1\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowPrivate.Hosts) != 0 || len(cfg.AllowPrivate.Prefixes) != 0 {
		t.Errorf("AllowPrivate = %+v", cfg.AllowPrivate)
	}
}

// GRD-R3：metadata 位址列進 allowlist 時載入失敗，訊息指出欄位與位置。
func TestLoadRejectsMetadataInAllowlist(t *testing.T) {
	p := writeFile(t, "network:\n  allow_private: [rsshub.lan, 169.254.169.254]\n")
	_, err := Load(Sources{File: p, Env: map[string]string{}})
	want := "設定檔 " + p + " 的欄位 network.allow_private[1]：169.254.169.254 是雲端 metadata 位址，一律封鎖，不能加入 allowlist。"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

// 網段只是包含 metadata 位址時可以設定（dial 時仍然擋下）。
func TestLoadAcceptsRangeContainingMetadata(t *testing.T) {
	p := writeFile(t, "network:\n  allow_private: [169.254.0.0/16]\n")
	if _, err := Load(Sources{File: p, Env: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsInvalidAllowPrivateEntry(t *testing.T) {
	p := writeFile(t, "network:\n  allow_private: ['*.lan']\n")
	_, err := Load(Sources{File: p, Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "network.allow_private[0]") || !strings.Contains(err.Error(), "*.lan") {
		t.Fatalf("err = %v", err)
	}
}

// network 的子欄位用 KnownFields 檢查，打錯字不能被悄悄忽略。
func TestLoadRejectsUnknownNetworkField(t *testing.T) {
	p := writeFile(t, "network:\n  allow_privat: [rsshub.lan]\n")
	_, err := Load(Sources{File: p, Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "allow_privat") {
		t.Fatalf("err = %v", err)
	}
}

// 佔位的 outbound 已被 network 取代，不再接受。
func TestLoadRejectsOldOutboundPlaceholder(t *testing.T) {
	_, err := Load(Sources{File: writeFile(t, "outbound:\n  allow: []\n"), Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "outbound") {
		t.Fatalf("err = %v", err)
	}
}

// 「未知欄位」與「由資料庫管理的欄位」是不同的訊息。
func TestLoadDistinguishesUnknownFromDatabaseManaged(t *testing.T) {
	_, err := Load(Sources{File: writeFile(t, "profiles: {}\n"), Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "由資料庫管理") || strings.Contains(err.Error(), "不認得") {
		t.Errorf("DB 管理的欄位：err = %v", err)
	}
	_, err = Load(Sources{File: writeFile(t, "listn: 127.0.0.1:1\n"), Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "listn") || !strings.Contains(err.Error(), "不認得") || strings.Contains(err.Error(), "資料庫") {
		t.Errorf("未知欄位：err = %v", err)
	}
}

func TestInboxDefaultsToDataDir(t *testing.T) {
	cfg, err := Load(Sources{File: filepath.Join(t.TempDir(), "none.yaml"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(DefaultData, "inbox"); cfg.Inbox != want {
		t.Errorf("inbox = %q, want %q", cfg.Inbox, want)
	}
}

// 預設的 inbox 要在環境變數覆寫 data 之後才計算，設定檔的 data 也一樣。
func TestInboxFollowsOverriddenData(t *testing.T) {
	p := writeFile(t, "data: /srv/from-file\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{EnvData: "/srv/from-env"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inbox != "/srv/from-env/inbox" {
		t.Errorf("inbox = %q", cfg.Inbox)
	}
	cfg, err = Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inbox != "/srv/from-file/inbox" {
		t.Errorf("inbox = %q", cfg.Inbox)
	}
}

func TestInboxFromConfigFile(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "inbox: /media/inbox/\n"), Env: map[string]string{EnvData: "/srv/data"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inbox != "/media/inbox" || cfg.SourceOf(FieldInbox) != SourceConfigFile {
		t.Errorf("inbox = %q (%s)", cfg.Inbox, cfg.SourceOf(FieldInbox))
	}
}

func TestMediaRootsAreCleaned(t *testing.T) {
	p := writeFile(t, "media_roots: [/media, /srv/../mnt/videos/, /a//b]\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/media", "/mnt/videos", "/a/b"}; !slices.Equal(cfg.MediaRoots, want) {
		t.Errorf("media_roots = %v, want %v", cfg.MediaRoots, want)
	}
}

func TestMediaRootsDefaultToEmpty(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, ""), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MediaRoots) != 0 {
		t.Errorf("media_roots = %v", cfg.MediaRoots)
	}
}

func TestRejectsRelativePaths(t *testing.T) {
	for body, field := range map[string]string{
		"media_roots: [/media, media/videos]\n": "media_roots[1]",
		"media_roots: ['']\n":                   "media_roots[0]",
		"inbox: inbox\n":                        "inbox",
	} {
		_, err := Load(Sources{File: writeFile(t, body), Env: map[string]string{}})
		if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "絕對路徑") {
			t.Errorf("%q：err = %v，應指出 %s 必須是絕對路徑", body, err, field)
		}
	}
}
