package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "notesteme.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	cfg, err := Load(Sources{File: filepath.Join(t.TempDir(), "none.yaml"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.Data != DefaultData {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.SourceOf(FieldListen) != SourceDefault || cfg.SourceOf(FieldData) != SourceDefault {
		t.Errorf("origin = %v", cfg.Origin)
	}
}

func TestLoadPrecedence(t *testing.T) {
	p := writeFile(t, "listen: 127.0.0.1:8080\ndata: /srv/data\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{EnvListen: "127.0.0.1:9090"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9090" || cfg.SourceOf(FieldListen) != SourceEnv {
		t.Errorf("listen = %q (%s)", cfg.Listen, cfg.SourceOf(FieldListen))
	}
	if cfg.Data != "/srv/data" || cfg.SourceOf(FieldData) != SourceConfigFile {
		t.Errorf("data = %q (%s)", cfg.Data, cfg.SourceOf(FieldData))
	}
}

func TestLoadUsesEnvConfigPath(t *testing.T) {
	p := writeFile(t, "listen: 127.0.0.1:7000\n")
	cfg, err := Load(Sources{Env: map[string]string{EnvConfig: p}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7000" || cfg.ConfigFile != p {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadRejectsDatabaseManagedFields(t *testing.T) {
	p := writeFile(t, "profiles:\n  standard: { language: { targets: [en] } }\n")
	_, err := Load(Sources{File: p})
	if err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("err = %v, want it to name profiles", err)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("listen = %q", cfg.Listen)
	}
}
