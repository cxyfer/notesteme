package config

import (
	"slices"
	"strings"
	"testing"
)

func TestLoadMCPOriginsAndHosts(t *testing.T) {
	p := writeFile(t, "mcp:\n  allowed_origins:\n    - HTTPS://App.Example.com:443\n    - http://10.0.0.5:8080/\n  allowed_hosts:\n    - Notes.Example.com\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.MCPAllowedOrigins, []string{"https://app.example.com", "http://10.0.0.5:8080"}) {
		t.Errorf("origins = %v", cfg.MCPAllowedOrigins)
	}
	if !slices.Equal(cfg.MCPAllowedHosts, []string{"notes.example.com"}) {
		t.Errorf("hosts = %v", cfg.MCPAllowedHosts)
	}
}

func TestLoadRejectsInvalidMCP(t *testing.T) {
	for body, want := range map[string]string{
		"mcp:\n  allowed_origins:\n    - notes.example.com\n":          "mcp.allowed_origins[0]",
		"mcp:\n  allowed_origins:\n    - https://a.example.com/path\n": "mcp.allowed_origins[0]",
		"mcp:\n  allowed_origins:\n    - https://u@a.example.com\n":    "mcp.allowed_origins[0]",
		"mcp:\n  allowed_hosts:\n    - notes.example.com:8080\n":       "mcp.allowed_hosts[0]",
		"mcp:\n  allowed_hosts:\n    - \"*.example.com\"\n":            "mcp.allowed_hosts[0]",
		"mcp:\n  allowed_host:\n    - x\n":                             "allowed_host",
	} {
		_, err := Load(Sources{File: writeFile(t, body), Env: map[string]string{}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q：err = %v，應指出 %s", body, err, want)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:80": "http://localhost",
		"https://[::1]:8443":  "https://[::1]:8443",
		"HTTP://EXAMPLE.com":  "http://example.com",
		"null":                "",
		"file:///etc/passwd":  "",
		"https://a.com?x=1":   "",
		"https://a.com#frag":  "",
	} {
		got, ok := NormalizeOrigin(in)
		if (want == "") == ok || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q %v，預期 %q", in, got, ok, want)
		}
	}
}
