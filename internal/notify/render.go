package notify

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// 內容上限（NOTIFY-R9）。
const (
	maxTitleRunes   = 100
	maxNtfyBytes    = 4000
	maxDiscordRunes = 2000
	maxTelegramRune = 4096
	// MaxRequestBody 是請求 body 的上限（NOTIFY-R7、R9 的 webhook 64 KiB）。
	MaxRequestBody = 64 << 10
	truncatedNote  = "…（內容過長，已截斷）"
)

// TestTitle 是測試通知的標題（NOTIFY-R12）。
const TestTitle = "Notesteme 測試通知"

// CleanTitle 清理不可信的標題（NOTIFY-R9）：移除控制字元、把連續空白合併成一個、去掉頭尾空白，
// 超過 100 字時保留前 99 字並加「…」。
func CleanTitle(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxTitleRunes {
		out = string([]rune(out)[:maxTitleRunes-1]) + "…"
	}
	return out
}

// cleanText 清理多行的不可信文字：移除控制字元（保留換行），每一行去掉頭尾空白。
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, l)
		out = append(out, strings.TrimSpace(l))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// itemTitle 是影音的顯示標題；沒有標題時用代號。
func itemTitle(it *Item) string {
	if it == nil {
		return ""
	}
	if t := CleanTitle(it.Title); t != "" {
		return t
	}
	return CleanTitle(it.Ref)
}

// Title 是文字管道（ntfy、Discord、Telegram）的標題（NOTIFY-R4）。
func Title(ev Event) string {
	switch ev.Name {
	case EventItemDone:
		return "完成：" + itemTitle(ev.Item)
	case EventItemFailed:
		return "失敗：" + itemTitle(ev.Item)
	case EventItemBlocked:
		return "需要你處理：" + itemTitle(ev.Item)
	case EventBetterAvailable:
		return "有更好的字幕來源：" + itemTitle(ev.Item)
	case EventAlert:
		summary := ev.Summary
		if ev.Alert != nil && ev.Alert.Summary != "" {
			summary = ev.Alert.Summary
		}
		if ev.Alert != nil && ev.Alert.Resolved {
			return "Notesteme 已恢復：" + CleanTitle(summary)
		}
		return "Notesteme 告警：" + CleanTitle(summary)
	case EventBudgetWarning:
		return "Notesteme 預算提醒：" + CleanTitle(ev.Summary)
	case EventDigest:
		// DIG-R10：Summary 已是完整的標題（「Notesteme 每日彙整：2026-11-24（3 部）」）。
		if t := CleanTitle(ev.Summary); t != "" {
			return t
		}
		return "Notesteme 彙整"
	case "test":
		return TestTitle
	}
	return "Notesteme：" + CleanTitle(ev.Summary)
}

// Body 是文字管道的內文：UI 的狀態訊息（失敗、需要你處理時是步驟的訊息與動作），有 public_url 時附上 UI 連結。
func Body(ev Event, link string) string {
	var lines []string
	switch ev.Name {
	case EventItemDone:
		lines = append(lines, "所有步驟都已完成。")
	case EventItemFailed, EventItemBlocked:
		if ev.Stage != "" {
			lines = append(lines, "步驟："+ev.Stage)
		}
		if m := cleanText(ev.Message); m != "" {
			lines = append(lines, m)
		}
		if len(ev.Actions) > 0 {
			lines = append(lines, "動作："+cleanText(strings.Join(ev.Actions, "、")))
		}
	case EventBetterAvailable:
		lines = append(lines, "這部影音有更好的字幕來源。")
	case "test":
		lines = append(lines, "這是一則測試通知；收到代表設定正確。")
	default:
		if m := cleanText(ev.Message); m != "" {
			lines = append(lines, m)
		} else if s := cleanText(ev.Summary); s != "" {
			lines = append(lines, s)
		}
	}
	if link != "" {
		lines = append(lines, link)
	}
	return strings.Join(lines, "\n")
}

// ItemURL 是影音的 UI 連結；沒有 server.public_url 時是空字串。
func ItemURL(publicURL string, it *Item) string {
	if publicURL == "" || it == nil || it.ID == "" {
		return ""
	}
	return publicURL + "/items/" + url.PathEscape(it.ID)
}

// eventLink 是通知附上的連結：影音的頁面，或沒有影音時的 UI 首頁。
func eventLink(publicURL string, ev Event) string {
	if u := ItemURL(publicURL, ev.Item); u != "" {
		return u
	}
	// 彙整的內文已附上每部影音的連結（DIG-R10），不再加 UI 首頁。
	if publicURL != "" && ev.Name != "test" && ev.Name != EventDigest {
		return publicURL + "/"
	}
	return ""
}

// truncateLines 在行的邊界截斷到 limit（以 measure 計算），超過時加上截斷說明（NOTIFY-R9）。
// 第一行就超過時在字元的邊界截斷。
func truncateLines(s string, limit int, measure func(string) int) string {
	if measure(s) <= limit {
		return s
	}
	budget := limit - measure("\n"+truncatedNote)
	lines := strings.Split(s, "\n")
	var kept []string
	for _, l := range lines {
		cand := strings.Join(append(kept, l), "\n")
		if measure(cand) > budget {
			if len(kept) == 0 {
				r := []rune(l)
				for len(r) > 0 && measure(string(r)) > budget {
					r = r[:len(r)-1]
				}
				kept = append(kept, string(r))
			}
			break
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n") + "\n" + truncatedNote
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }
func byteLen(s string) int { return len(s) }

// discordEscaper 跳脫 Discord Markdown 的字元（NOTIFY-R9）。
var discordEscaper = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`,
	`[`, `\[`, `]`, `\]`, `(`, `\(`, `)`, `\)`,
)

// EscapeDiscord 跳脫不可信文字中的 Discord Markdown 字元。
func EscapeDiscord(s string) string { return discordEscaper.Replace(s) }

// webhookBody 是 webhook 的 JSON（NOTIFY-R4）。
type webhookBody struct {
	ID         string          `json:"id"`
	Event      string          `json:"event"`
	CreatedAt  time.Time       `json:"created_at"`
	Instance   *string         `json:"instance"`
	Item       *webhookItem    `json:"item,omitempty"`
	Stage      string          `json:"stage,omitempty"`
	ErrorClass string          `json:"error_class,omitempty"`
	Message    string          `json:"message,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Digest     json.RawMessage `json:"digest,omitempty"`
	Items      json.RawMessage `json:"items,omitempty"`
	Alert      *Alert          `json:"alert,omitempty"`
	// Budget 是 budget.warning 的數字（COST-R10；NOTIFY-R4 沒有列，COST 待決問題記錄）。
	Budget json.RawMessage `json:"budget,omitempty"`
}

type webhookItem struct {
	ID      string  `json:"id"`
	Ref     string  `json:"ref"`
	Title   string  `json:"title"`
	Channel string  `json:"channel"`
	URL     *string `json:"url"`
}

// render 依種類組出請求的 body（不含 webhook 的簽章，簽章在每次嘗試時加上）。
func render(kind string, cfg Config, ev Event, id string, created time.Time, publicURL string) ([]byte, error) {
	link := eventLink(publicURL, ev)
	title := Title(ev)
	switch kind {
	case KindNtfy:
		msg := truncateLines(Body(ev, link), maxNtfyBytes, byteLen)
		if msg == "" {
			msg = title
		}
		body := map[string]any{"topic": cfg["topic"], "title": title, "message": msg, "priority": atoiOr(cfg["priority"], defaultNtfyPriority)}
		if link != "" {
			body["click"] = link
		}
		return json.Marshal(body)
	case KindDiscord:
		content := "**" + EscapeDiscord(title) + "**"
		if b := Body(ev, ""); b != "" {
			content += "\n" + EscapeDiscord(b)
		}
		if link != "" {
			content += "\n" + link
		}
		content = truncateLines(content, maxDiscordRunes, runeLen)
		return json.Marshal(map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}})
	case KindTelegram:
		text := title
		if b := Body(ev, link); b != "" {
			text += "\n" + b
		}
		text = truncateLines(text, maxTelegramRune, runeLen)
		return json.Marshal(map[string]any{"chat_id": cfg["chat_id"], "text": text, "link_preview_options": map[string]any{"is_disabled": true}})
	}
	// webhook
	wb := webhookBody{
		ID: id, Event: ev.Name, CreatedAt: created.UTC(), Stage: ev.Stage, ErrorClass: ev.ErrorClass,
		Message: cleanText(ev.Message), Summary: cleanText(ev.Summary), Digest: ev.Digest, Items: ev.Items, Alert: ev.Alert,
		Budget: ev.Budget,
	}
	if ev.Name == "test" {
		wb.Message = TestTitle
	}
	if publicURL != "" {
		p := publicURL
		wb.Instance = &p
	}
	if it := ev.Item; it != nil {
		wi := &webhookItem{ID: it.ID, Ref: it.Ref, Title: CleanTitle(it.Title), Channel: CleanTitle(it.Channel)}
		if u := ItemURL(publicURL, it); u != "" {
			wi.URL = &u
		}
		wb.Item = wi
	}
	b, err := json.Marshal(wb)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxRequestBody {
		// 只有彙整這類大內容會超過：去掉明細，保留其他欄位與截斷說明。
		wb.Digest, wb.Items = nil, nil
		wb.Message = truncatedNote
		return json.Marshal(wb)
	}
	return b, nil
}

func atoiOr(s string, def int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		if n > 1000 {
			return def
		}
	}
	if s == "" {
		return def
	}
	return n
}
