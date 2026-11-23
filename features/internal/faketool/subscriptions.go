package faketool

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 訂閱（SUB，M4）用到的 yt-dlp 清單：頻道的 /videos、@handle 的解析、Bilibili 合集，以及 --playlist-end。

var (
	channelVideosPath = regexp.MustCompile(`^/channel/(UC[A-Za-z0-9_-]{22})/videos/?$`)
	handlePath        = regexp.MustCompile(`^/(@[^/]+)/?$`)
	spaceListsPath    = regexp.MustCompile(`^/([0-9]+)/lists/([0-9]+)/?$`)
	spaceLegacyPath   = regexp.MustCompile(`^/([0-9]+)/channel/collectiondetail/?$`)
)

// listTarget 是 --flat-playlist 的清單網址（SUB）：kind 是 channel、handle 或 collection。
type listTarget struct{ kind, id string }

// parseListURL 辨識頻道的 /videos、@handle 與 Bilibili 合集的網址。
func parseListURL(raw string) (listTarget, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return listTarget{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "youtube.com", "m.youtube.com":
		if m := channelVideosPath.FindStringSubmatch(u.Path); m != nil {
			return listTarget{kind: "channel", id: m[1]}, true
		}
		if m := handlePath.FindStringSubmatch(u.Path); m != nil {
			return listTarget{kind: "handle", id: strings.ToLower(m[1])}, true
		}
	case "space.bilibili.com":
		if m := spaceListsPath.FindStringSubmatch(u.Path); m != nil && u.Query().Get("type") == "season" {
			return listTarget{kind: "collection", id: m[1] + ":" + m[2]}, true
		}
		if m := spaceLegacyPath.FindStringSubmatch(u.Path); m != nil && u.Query().Get("sid") != "" {
			return listTarget{kind: "collection", id: m[1] + ":" + u.Query().Get("sid")}, true
		}
	}
	return listTarget{}, false
}

// CollectionKey 是 Bilibili 合集網址（兩種格式）的 "<mid>:<sid>"；不是合集網址時 ok 為 false。
func CollectionKey(raw string) (string, bool) {
	t, ok := parseListURL(raw)
	if !ok || t.kind != "collection" {
		return "", false
	}
	return t.id, true
}

// ChannelVideosID 是頻道 /videos 網址的頻道 ID；不是時 ok 為 false。
func ChannelVideosID(raw string) (string, bool) {
	t, ok := parseListURL(raw)
	if !ok || t.kind != "channel" {
		return "", false
	}
	return t.id, true
}

// HandleOf 是頻道名稱對應的 handle：轉小寫並去掉空白（Fixture Channel → @fixturechannel）。
func HandleOf(channel string) string {
	return "@" + strings.ToLower(strings.Join(strings.Fields(channel), ""))
}

// listExtractors 是清單網址的 extractor 名稱（--use-extractors 的判斷）。
func listExtractors(t listTarget) []string {
	if t.kind == "collection" {
		return []string{"BilibiliCollectionList"}
	}
	return []string{"youtube:tab"}
}

// ytList 輸出頻道 /videos、@handle 或合集的 --flat-playlist -J；end 是 --playlist-end（0 代表沒有限制）。
func ytList(e env, st State, t listTarget, end int) int {
	switch t.kind {
	case "handle":
		id, name := resolveHandle(st, t.id)
		if id == "" {
			fmt.Fprintf(e.stderr, "ERROR: [youtube:tab] %s: This channel does not exist.\n", t.id)
			return 1
		}
		return writeJSON(e, map[string]any{"_type": "playlist", "id": id, "channel_id": id, "channel": name, "title": name, "entries": []any{}})
	case "collection":
		name, ok := st.Collections[t.id]
		if !ok {
			fmt.Fprintf(e.stderr, "ERROR: [BilibiliCollectionList] %s: The playlist does not exist.\n", strings.ReplaceAll(t.id, ":", "_"))
			return 1
		}
		return writeFixture(e, st, name, end)
	}
	if name, ok := st.ChannelLists[t.id]; ok {
		return writeFixture(e, st, name, end)
	}
	type entry struct {
		Type       string `json:"_type"`
		IEKey      string `json:"ie_key"`
		ID         string `json:"id"`
		URL        string `json:"url"`
		Title      string `json:"title"`
		Duration   *int   `json:"duration"`
		LiveStatus string `json:"live_status"`
	}
	type vid struct {
		id  string
		v   *Video
		pub time.Time
	}
	var vs []vid
	for id, v := range st.Videos {
		if v == nil || v.ChannelID != t.id || v.Shorts || strings.Contains(id, "/") {
			continue
		}
		pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if p, err := time.Parse(time.RFC3339, v.PublishedAt); err == nil {
			pub = p
		}
		vs = append(vs, vid{id: id, v: v, pub: pub})
	}
	sort.Slice(vs, func(i, j int) bool {
		if !vs[i].pub.Equal(vs[j].pub) {
			return vs[i].pub.After(vs[j].pub)
		}
		return vs[i].id < vs[j].id
	})
	if end > 0 && len(vs) > end {
		vs = vs[:end]
	}
	entries := make([]entry, 0, len(vs))
	name := ""
	for _, x := range vs {
		d := x.v.Duration
		entries = append(entries, entry{Type: "url", IEKey: "Youtube", ID: x.id, URL: "https://www.youtube.com/watch?v=" + x.id,
			Title: x.v.Title, Duration: &d, LiveStatus: orDefault(x.v.LiveStatus, "not_live")})
		if name == "" {
			name = x.v.Channel
		}
	}
	return writeJSON(e, map[string]any{"_type": "playlist", "id": t.id, "channel_id": t.id, "channel": name, "title": name + " - Videos", "entries": entries})
}

// resolveHandle 找出 handle 的頻道：先看 State.Channels，再看影片的頻道名稱。
func resolveHandle(st State, handle string) (id, name string) {
	if id := st.Channels[handle]; id != "" {
		for _, v := range st.Videos {
			if v != nil && v.ChannelID == id && v.Channel != "" {
				return id, v.Channel
			}
		}
		return id, strings.TrimPrefix(handle, "@")
	}
	for _, v := range st.Videos {
		if v != nil && v.ChannelID != "" && v.Channel != "" && HandleOf(v.Channel) == handle {
			return v.ChannelID, v.Channel
		}
	}
	return "", ""
}

// writeFixture 輸出 FixturesDir/ytdlp/<name>，遵守 --playlist-end。
func writeFixture(e env, st State, name string, end int) int {
	if st.FixturesDir == "" {
		fmt.Fprintf(e.stderr, "ERROR: fake state has no fixtures_dir; cannot read playlist fixture %q\n", name)
		return 1
	}
	b, err := os.ReadFile(filepath.Join(st.FixturesDir, "ytdlp", filepath.Clean("/"+name)))
	if err != nil {
		fmt.Fprintf(e.stderr, "ERROR: playlist fixture %q: %v\n", name, err)
		return 1
	}
	if end <= 0 {
		fmt.Fprintln(e.stdout, strings.TrimSpace(string(b)))
		return 0
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		fmt.Fprintf(e.stderr, "ERROR: playlist fixture %q: %v\n", name, err)
		return 1
	}
	if es, ok := doc["entries"].([]any); ok && len(es) > end {
		doc["entries"] = es[:end]
	}
	return writeJSON(e, doc)
}

func writeJSON(e env, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(e.stderr, "ERROR:", err)
		return 1
	}
	fmt.Fprintln(e.stdout, string(b))
	return 0
}
