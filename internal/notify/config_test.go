package notify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/validate"
)

const (
	testBotToken = "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789"
	testDiscord  = "https://discord.com/api/webhooks/1234/abcd-token-xyz"
)

func firstErr(errs []validate.FieldError) validate.FieldError {
	if len(errs) == 0 {
		return validate.FieldError{}
	}
	return errs[0]
}

func TestValidateConfigAcceptsEachKind(t *testing.T) {
	pol := HostPolicy{AllowHosts: []string{"tg-proxy.example.com"}}
	cases := []struct {
		kind string
		raw  map[string]any
		want Config
	}{
		{KindNtfy, map[string]any{"topic": "notesteme-owner"}, Config{"server": DefaultNtfyServer, "topic": "notesteme-owner", "priority": "3"}},
		{KindNtfy, map[string]any{"topic": "t", "server": "https://ntfy.example.org/", "priority": float64(5), "token": "tk_1"},
			Config{"server": "https://ntfy.example.org", "topic": "t", "priority": "5", "token": "tk_1"}},
		{KindDiscord, map[string]any{"webhook_url": testDiscord}, Config{"webhook_url": testDiscord}},
		{KindDiscord, map[string]any{"webhook_url": "https://discordapp.com/api/webhooks/1/x"}, Config{"webhook_url": "https://discordapp.com/api/webhooks/1/x"}},
		{KindTelegram, map[string]any{"bot_token": testBotToken, "chat_id": "42"}, Config{"bot_token": testBotToken, "chat_id": "42"}},
		{KindTelegram, map[string]any{"bot_token": testBotToken, "chat_id": float64(-100123), "api_base": "https://tg-proxy.example.com/"},
			Config{"bot_token": testBotToken, "chat_id": "-100123", "api_base": "https://tg-proxy.example.com"}},
		{KindWebhook, map[string]any{"url": "https://hooks.example.com/x?token=1", "secret": "whsec-0123456789abcdef"},
			Config{"url": "https://hooks.example.com/x?token=1", "secret": "whsec-0123456789abcdef"}},
	}
	for _, c := range cases {
		got, errs := ValidateConfig(c.kind, c.raw, pol)
		if len(errs) > 0 || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v → %v %v，預期 %v", c.kind, c.raw, got, errs, c.want)
		}
	}
}

func TestValidateConfigRejects(t *testing.T) {
	allowLAN := HostPolicy{AllowHTTP: func(h string) bool { return h == "nas.lan" }}
	cases := []struct {
		name string
		kind string
		raw  map[string]any
		path string
		msg  string
	}{
		{"discord 其他主機", KindDiscord, map[string]any{"webhook_url": "https://evil.example/api/webhooks/1/abc"}, "config.webhook_url", msgDiscordHost},
		{"discord 官方主機但路徑不對", KindDiscord, map[string]any{"webhook_url": "https://discord.com/evil"}, "config.webhook_url", msgDiscordHost},
		{"discord 官方主機的子網域", KindDiscord, map[string]any{"webhook_url": "https://discord.com.evil.example/api/webhooks/1/abc"}, "config.webhook_url", msgDiscordHost},
		{"discord 帶帳號密碼", KindDiscord, map[string]any{"webhook_url": "https://u:p@discord.com/api/webhooks/1/abc"}, "config.webhook_url", ""},
		{"discord http", KindDiscord, map[string]any{"webhook_url": "http://discord.com/api/webhooks/1/abc"}, "config.webhook_url", ""},
		{"discord 非預設 port", KindDiscord, map[string]any{"webhook_url": "https://discord.com:8443/api/webhooks/1/abc"}, "config.webhook_url", msgDiscordHost},
		{"telegram 其他主機", KindTelegram, map[string]any{"bot_token": testBotToken, "chat_id": "42", "api_base": "https://evil.example"}, "config.api_base", msgTelegramHost},
		{"telegram api_base 帶路徑", KindTelegram, map[string]any{"bot_token": testBotToken, "chat_id": "42", "api_base": "https://api.telegram.org/x"}, "config.api_base", ""},
		{"telegram token 格式", KindTelegram, map[string]any{"bot_token": "abc", "chat_id": "42"}, "config.bot_token", ""},
		{"telegram 缺 chat_id", KindTelegram, map[string]any{"bot_token": testBotToken}, "config.chat_id", msgRequired},
		{"ntfy topic", KindNtfy, map[string]any{"topic": "has space"}, "config.topic", ""},
		{"ntfy priority", KindNtfy, map[string]any{"topic": "t", "priority": float64(9)}, "config.priority", ""},
		{"ntfy token 換行", KindNtfy, map[string]any{"topic": "t", "token": "a\r\nX-Evil: 1"}, "config.token", ""},
		{"ntfy file scheme", KindNtfy, map[string]any{"topic": "t", "server": "file:///etc/passwd"}, "config.server", ""},
		{"webhook http 不在 allow_private", KindWebhook, map[string]any{"url": "http://hooks.example.com/x", "secret": "0123456789abcdef"}, "config.url", ""},
		{"webhook secret 太短", KindWebhook, map[string]any{"url": "https://hooks.example.com/x", "secret": "short"}, "config.secret", ""},
		{"webhook 不認得的欄位", KindWebhook, map[string]any{"url": "https://hooks.example.com/x", "secret": "0123456789abcdef", "headers": "x"}, "config.headers", msgUnknownField},
		{"webhook 值不是字串", KindWebhook, map[string]any{"url": float64(1), "secret": "0123456789abcdef"}, "config.url", msgString},
	}
	for _, c := range cases {
		_, errs := ValidateConfig(c.kind, c.raw, allowLAN)
		e := firstErr(errs)
		if e.Path != c.path || (c.msg != "" && e.Message != c.msg) {
			t.Errorf("%s：%v，預期 %s %q", c.name, errs, c.path, c.msg)
		}
	}
	// http:// 只在主機列於 network.allow_private 時接受。
	if _, errs := ValidateConfig(KindWebhook, map[string]any{"url": "http://nas.lan/hook", "secret": "0123456789abcdef"}, allowLAN); len(errs) > 0 {
		t.Errorf("allow_private 的 http 應接受：%v", errs)
	}
}

func TestPublicShowsOnlyLastFour(t *testing.T) {
	cfg := Config{"url": "https://hooks.example.com/notesteme", "secret": "whsec-0123456789abcdef"}
	got := cfg.Public(KindWebhook)
	want := map[string]any{"url_host": "hooks.example.com", "url_last4": "teme", "secret_last4": "cdef"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Public = %v", got)
	}
	tg := Config{"bot_token": testBotToken, "chat_id": "42"}.Public(KindTelegram)
	if !reflect.DeepEqual(tg, map[string]any{"chat_id": "42", "bot_token_last4": "6789"}) {
		t.Errorf("telegram Public = %v", tg)
	}
	nt := Config{"server": DefaultNtfyServer, "topic": "t", "priority": "3", "token": "ab"}.Public(KindNtfy)
	if nt["priority"] != 3 || nt["token_last4"] != "ab" || nt["token"] != nil {
		t.Errorf("ntfy Public = %v", nt)
	}
	for _, v := range []map[string]any{got, tg, nt} {
		for k, x := range v {
			if s, ok := x.(string); ok && (strings.Contains(s, "whsec") || strings.Contains(s, "ABCdef")) {
				t.Errorf("Public 不可含密鑰：%s=%s", k, s)
			}
		}
	}
	if back := nonSecret(KindNtfy, map[string]any{"server": "s", "topic": "t", "priority": float64(4), "token_last4": "ab"}); !reflect.DeepEqual(back, Config{"server": "s", "topic": "t", "priority": "4"}) {
		t.Errorf("nonSecret = %v", back)
	}
}

func TestValidateEventsAndID(t *testing.T) {
	if errs := ValidateEvents([]string{"item.done", "item.deleted"}); len(errs) != 1 || errs[0].Path != "events.1" || errs[0].Message != "不認得的事件：item.deleted。" {
		t.Errorf("不認得的事件：%v", errs)
	}
	if errs := ValidateEvents([]string{"alert", "alert"}); len(errs) != 1 || errs[0].Path != "events.1" {
		t.Errorf("重複：%v", errs)
	}
	if errs := ValidateEvents(nil); len(errs) != 1 || errs[0].Path != "events" {
		t.Errorf("空的：%v", errs)
	}
	if errs := ValidateEvents([]string{EventDropped}); len(errs) != 1 {
		t.Errorf("notify.dropped 不能訂閱：%v", errs)
	}
	if errs := ValidateEvents(Events); len(errs) != 0 {
		t.Errorf("全部事件：%v", errs)
	}
	for id, ok := range map[string]bool{"phone": true, "a": true, "0-x": true, "-x": false, "Phone": false, "": false, strings.Repeat("a", 41): false, "a/b": false} {
		if ValidID(id) != ok {
			t.Errorf("ValidID(%q) 應為 %v", id, ok)
		}
	}
}
