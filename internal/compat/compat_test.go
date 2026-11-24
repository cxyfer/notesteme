package compat

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		client, server string
		want           Verdict
	}{
		{"0.5.0", "0.4.9", Compatible}, // 新 CLI 連舊一個小版本的 serve（NFR-CMP-4）
		{"0.4.0", "0.5.2", Compatible}, // 反之亦然
		{"v0.5.1", "0.5.0", Compatible},
		{"0.6.0", "0.4.0", ServerTooOld},
		{"0.3.0", "0.5.0", ClientTooOld},
		{"1.0.0", "0.9.0", ServerTooOld},
		{"1.2.0", "2.0.0", ClientTooOld},
		{"0.5.0-rc.1", "0.3.0", ServerTooOld},
		{"0.5.0-3-gabc123", "0.5.0", Compatible},
		{"1.2", "1.4.0", ClientTooOld},
		{"dev", "0.1.0", Compatible}, // 開發版不擋
		{"0.1.0", "dev", Compatible},
		{"", "0.9.0", Compatible},
		{"0.x.1", "0.9.0", Compatible},
		{"1.2.3.4", "9.0.0", Compatible},
		{"-1.0", "9.0.0", Compatible},
		{"0.1.0-" + strings.Repeat("x", 64), "0.9.0", Compatible},
	} {
		if got := Check(c.client, c.server); got != c.want {
			t.Errorf("Check(%q, %q) = %v, want %v", c.client, c.server, got, c.want)
		}
	}
}

func TestMessage(t *testing.T) {
	if got := Message(ClientTooOld, "0.3.0", "0.5.0"); got != "CLI 版本太舊（CLI 0.3.0，serve 0.5.0），請升級 CLI。" {
		t.Errorf("got %q", got)
	}
	if got := Message(ServerTooOld, "0.6.0", "0.4.1"); got != "serve 版本太舊（serve 0.4.1，CLI 0.6.0），請升級 serve，或換成 0.4 版的 CLI。" {
		t.Errorf("got %q", got)
	}
	if Message(Compatible, "a", "b") != "" {
		t.Error("compatible")
	}
}
