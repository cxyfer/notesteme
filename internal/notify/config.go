package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/validate"
)

// notifier 的種類（NOTIFY-R1）。
const (
	KindNtfy     = "ntfy"
	KindDiscord  = "discord"
	KindTelegram = "telegram"
	KindWebhook  = "webhook"
)

// Kinds 是支援的種類，依規格的順序。
var Kinds = []string{KindNtfy, KindDiscord, KindTelegram, KindWebhook}

// 預設值（NOTIFY-R2）。
const (
	DefaultNtfyServer      = "https://ntfy.sh"
	DefaultTelegramAPIBase = "https://api.telegram.org"
	defaultNtfyPriority    = 3
)

// 驗證訊息（場景依賴其中幾句，見 features/NOTIFY-notifications.feature）。
const (
	msgDiscordHost  = "Discord webhook 只能指向 discord.com，或設定檔 notify.allow_hosts 中列出的主機。"
	msgTelegramHost = "Telegram 只能連到 api.telegram.org，或設定檔 notify.allow_hosts 中列出的主機。"
	msgRequired     = "必填。"
	msgUnknownField = "不認得的欄位。"
	msgString       = "必須是字串。"
)

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	topicPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	botTokenPattern = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{30,}$`)
	chatIDPattern   = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z0-9_]{5,32})$`)
	// discordPath 是 Discord webhook 的路徑：/api/webhooks/<數字>/<token>。
	discordPath = regexp.MustCompile(`^/api/webhooks/\d{1,25}/[A-Za-z0-9_-]{1,200}$`)
)

// discordHosts 是 Discord webhook 的官方主機（NOTIFY-R2）。
var discordHosts = []string{"discord.com", "discordapp.com"}

// secretFields 是各種類的密鑰欄位（NOTIFY-R6）：API 只回傳末四碼，webhook 另外回傳 url_host。
var secretFields = map[string][]string{
	KindNtfy:     {"token"},
	KindDiscord:  {"webhook_url"},
	KindTelegram: {"bot_token"},
	KindWebhook:  {"url", "secret"},
}

// publicFields 是各種類的非機密欄位，原樣出現在 API 回應。
var publicFields = map[string][]string{
	KindNtfy:     {"server", "topic", "priority"},
	KindDiscord:  {},
	KindTelegram: {"chat_id", "api_base"},
	KindWebhook:  {},
}

// HostPolicy 是設定檔決定的主機規則（只能寫在設定檔，避免 API 把請求導到任意主機）。
type HostPolicy struct {
	// AllowHosts 是 notify.allow_hosts：Telegram、Discord 可以使用的非官方主機（小寫）。
	AllowHosts []string
	// AllowHTTP 回報 http:// 的主機是否列在 network.allow_private（NOTIFY-R2 的 webhook）；nil 代表一律不接受 http。
	AllowHTTP func(host string) bool
}

func (p HostPolicy) allowed(host string) bool {
	return slices.Contains(p.AllowHosts, strings.ToLower(host))
}

// Config 是驗證過、補上預設值的 notifier 設定；整份加密存放（含非機密欄位），API 只看 Public。
type Config map[string]string

// fieldErrs 累積逐欄位的錯誤，路徑加上 config. 前綴。
type fieldErrs []validate.FieldError

func (e *fieldErrs) add(path, msg string) {
	*e = append(*e, validate.FieldError{Path: path, Message: msg})
}

// ValidateConfig 驗證 kind 的 config（NOTIFY-R2），回傳正規化的設定；錯誤的 path 是 config.<欄位>。
// raw 的值必須是字串（priority 可以是數字，chat_id 可以是整數）。
func ValidateConfig(kind string, raw map[string]any, pol HostPolicy) (Config, []validate.FieldError) {
	var errs fieldErrs
	known := append(slices.Clone(secretFields[kind]), publicFields[kind]...)
	cfg := Config{}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !slices.Contains(known, k) {
			errs.add("config."+k, msgUnknownField)
			continue
		}
		v, ok := stringValue(k, raw[k])
		if !ok {
			errs.add("config."+k, msgString)
			continue
		}
		cfg[k] = strings.TrimSpace(v)
	}
	switch kind {
	case KindNtfy:
		validateNtfy(cfg, pol, &errs)
	case KindDiscord:
		validateDiscord(cfg, pol, &errs)
	case KindTelegram:
		validateTelegram(cfg, pol, &errs)
	case KindWebhook:
		validateWebhook(cfg, pol, &errs)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return cfg, nil
}

// stringValue 取出字串值；priority 與 chat_id 也接受 JSON 的整數。
func stringValue(key string, v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		if (key == "priority" || key == "chat_id") && x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), true
		}
	case json.Number:
		if key == "priority" || key == "chat_id" {
			if n, err := x.Int64(); err == nil {
				return strconv.FormatInt(n, 10), true
			}
		}
	}
	return "", false
}

// parseEndpoint 驗證 http(s) 網址：不含帳號密碼與 fragment；http 只在 AllowHTTP 接受時可用。
func parseEndpoint(raw string, pol HostPolicy, allowQuery bool) (*url.URL, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (!allowQuery && u.RawQuery != "") {
		return nil, "必須是 https:// 開頭的網址，不含帳號密碼與 #。"
	}
	switch u.Scheme {
	case "https":
	case "http":
		if pol.AllowHTTP == nil || !pol.AllowHTTP(strings.ToLower(u.Hostname())) {
			return nil, "必須是 https://；http:// 只在主機列於設定檔 network.allow_private 時接受。"
		}
	default:
		return nil, "必須是 https:// 開頭的網址，不含帳號密碼與 #。"
	}
	return u, ""
}

func validateNtfy(cfg Config, pol HostPolicy, errs *fieldErrs) {
	if cfg["server"] == "" {
		cfg["server"] = DefaultNtfyServer
	}
	if u, msg := parseEndpoint(cfg["server"], pol, false); u == nil {
		errs.add("config.server", msg)
	} else {
		cfg["server"] = strings.TrimRight(u.String(), "/")
	}
	if t := cfg["topic"]; t == "" {
		errs.add("config.topic", msgRequired)
	} else if !topicPattern.MatchString(t) {
		errs.add("config.topic", "topic 只能使用英文字母、數字、_ 與 -，最多 64 字元。")
	}
	if cfg["priority"] == "" {
		cfg["priority"] = strconv.Itoa(defaultNtfyPriority)
	}
	if n, err := strconv.Atoi(cfg["priority"]); err != nil || n < 1 || n > 5 {
		errs.add("config.priority", "priority 必須是 1～5 的整數。")
	}
	if t, ok := cfg["token"]; ok && (t == "" || !printable(t, 512)) {
		errs.add("config.token", "token 格式不正確。")
	}
}

func validateDiscord(cfg Config, pol HostPolicy, errs *fieldErrs) {
	raw := cfg["webhook_url"]
	if raw == "" {
		errs.add("config.webhook_url", msgRequired)
		return
	}
	u, msg := parseEndpoint(raw, pol, false)
	if u == nil {
		errs.add("config.webhook_url", msg)
		return
	}
	host := strings.ToLower(u.Hostname())
	official := slices.Contains(discordHosts, host) && u.Scheme == "https" && u.Port() == "" && discordPath.MatchString(u.EscapedPath())
	if !official && !pol.allowed(host) {
		errs.add("config.webhook_url", msgDiscordHost)
	}
}

func validateTelegram(cfg Config, pol HostPolicy, errs *fieldErrs) {
	if t := cfg["bot_token"]; t == "" {
		errs.add("config.bot_token", msgRequired)
	} else if !botTokenPattern.MatchString(t) || len(t) > 200 {
		errs.add("config.bot_token", "bot_token 格式不正確（<數字>:<至少 30 個字元>）。")
	}
	if c := cfg["chat_id"]; c == "" {
		errs.add("config.chat_id", msgRequired)
	} else if !chatIDPattern.MatchString(c) {
		errs.add("config.chat_id", "chat_id 必須是數字，或 @ 開頭的頻道名稱。")
	}
	base, ok := cfg["api_base"]
	if !ok || base == "" {
		delete(cfg, "api_base")
		return
	}
	u, msg := parseEndpoint(base, pol, false)
	if u == nil {
		errs.add("config.api_base", msg)
		return
	}
	if u.Path != "" && u.Path != "/" {
		errs.add("config.api_base", "api_base 只能是主機，不含路徑。")
		return
	}
	host := strings.ToLower(u.Hostname())
	if !(host == "api.telegram.org" && u.Scheme == "https" && u.Port() == "") && !pol.allowed(host) {
		errs.add("config.api_base", msgTelegramHost)
		return
	}
	cfg["api_base"] = strings.TrimRight(u.String(), "/")
}

func validateWebhook(cfg Config, pol HostPolicy, errs *fieldErrs) {
	if raw := cfg["url"]; raw == "" {
		errs.add("config.url", msgRequired)
	} else if u, msg := parseEndpoint(raw, pol, true); u == nil {
		errs.add("config.url", msg)
	}
	if s := cfg["secret"]; s == "" {
		errs.add("config.secret", msgRequired)
	} else if n := utf8.RuneCountInString(s); n < 16 || n > 256 || !printable(s, 1024) {
		errs.add("config.secret", "secret 必須是 16～256 個字元。")
	}
}

// printable 回報 s 沒有控制字元、長度不超過 max bytes（token 會放進標頭，不能有換行）。
func printable(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Public 回傳 API 顯示的設定：非機密欄位原樣，密鑰只有 <欄位>_last4，webhook 另外有 url_host（NOTIFY-R6）。
func (c Config) Public(kind string) map[string]any {
	out := map[string]any{}
	for _, k := range publicFields[kind] {
		v, ok := c[k]
		if !ok {
			continue
		}
		if k == "priority" {
			n, _ := strconv.Atoi(v)
			out[k] = n
			continue
		}
		out[k] = v
	}
	for _, k := range secretFields[kind] {
		if v, ok := c[k]; ok && v != "" {
			out[k+"_last4"] = last4(v)
		}
	}
	if kind == KindWebhook {
		if u, err := url.Parse(c["url"]); err == nil {
			out["url_host"] = strings.ToLower(u.Hostname())
		}
	}
	return out
}

// nonSecret 回傳 Public 中可以還原成設定的欄位（密鑰被重設之後，PATCH 以它為基礎重新設定）。
func nonSecret(kind string, public map[string]any) Config {
	c := Config{}
	for _, k := range publicFields[kind] {
		switch v := public[k].(type) {
		case string:
			c[k] = v
		case float64:
			c[k] = strconv.FormatInt(int64(v), 10)
		}
	}
	return c
}

// last4 回傳最後四個字元（不足四個時全部）。
func last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}

// ValidID 回報 notifier id 是否符合 NOTIFY-R1。
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ValidateEvents 驗證 events（NOTIFY-R3）：1～7 個不重複的事件名稱；錯誤的 path 是 events 或 events.<索引>。
func ValidateEvents(events []string) []validate.FieldError {
	var errs fieldErrs
	if len(events) == 0 || len(events) > len(Events) {
		errs.add("events", fmt.Sprintf("要選 1～%d 個事件。", len(Events)))
		return errs
	}
	for i, e := range events {
		path := "events." + strconv.Itoa(i)
		switch {
		case !slices.Contains(Events, e):
			errs.add(path, "不認得的事件："+e+"。")
		case slices.Index(events, e) != i:
			errs.add(path, "重複的事件："+e+"。")
		}
	}
	return errs
}
