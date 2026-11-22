package output

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	good, err := Parse(map[string]any{"target": "sidecar", "formats": []any{"srt", "ass"}, "lang": []any{"en", "zh-Hant-TW"}, "on_user_change": "skip"})
	if err != nil || good.Policy() != PolicySkip || len(good.FormatList()) != 2 {
		t.Fatalf("Parse = %+v，%v", good, err)
	}
	def, err := Parse(map[string]any{"target": "folder", "path": "/srv/subs"})
	if err != nil || def.Policy() != PolicySaveAsNew || def.FormatList()[0] != "srt" {
		t.Fatalf("預設值：%+v，%v", def, err)
	}
	bad := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"未知欄位", map[string]any{"target": "sidecar", "pth": "x"}, "pth"},
		{"vault 尚未支援", map[string]any{"target": "vault"}, "M3"},
		{"未知目標", map[string]any{"target": "s3"}, "s3"},
		{"格式", map[string]any{"target": "sidecar", "formats": []any{"docx"}}, "docx"},
		{"語言", map[string]any{"target": "sidecar", "lang": []any{"not a lang"}}, "lang"},
		{"政策", map[string]any{"target": "sidecar", "on_user_change": "overwrite"}, "on_user_change"},
		{"folder 缺 path", map[string]any{"target": "folder"}, "path"},
		{"sidecar 不可有 path", map[string]any{"target": "sidecar", "path": "/x"}, "path"},
	}
	for _, c := range bad {
		if _, err := Parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：err = %v，預期含 %q", c.name, err, c.want)
		}
	}
}
