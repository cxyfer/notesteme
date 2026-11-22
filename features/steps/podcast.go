package steps

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakepodcast"
	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// 這個檔案放 Podcast（ING-04、SRC-R14、R15）的步驟：fake feed、音檔與 <podcast:transcript> 字幕。
// fixture 在 features/fixtures/podcast/，說明見 fixtures/README.md。

// podcastTranscriptID 是 <podcast:transcript> 字幕來源的 ID。用字串常數，不依賴 production 的符號。
const podcastTranscriptID = "podcast-transcript"

// feedNamePattern 是 feed 名稱的格式：小寫英文、數字與 -。
var feedNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// genericFeedNames 是不能當 feed 名稱的泛用名稱：production 取 feed 代號時會略過它們（往前取一段路徑），
// 場景若用它們，代號就不是名稱本身。
var genericFeedNames = map[string]bool{
	"rss": true, "feed": true, "feeds": true, "podcast": true, "podcasts": true,
	"atom": true, "index": true, "itunes": true, "xml": true,
}

// podcastFeed 建立 fake 的 feed：表頭是 guid、title、audio。
// audio 是檔名時，音檔在 https://media.example.com/<名稱>/<檔名>；是完整網址時放在那個網址。
// 兩者的 fixture 都是 podcast/<檔名>，必須存在而且是假媒體檔。
func (s *Scenario) podcastFeed(name string, table *godog.Table) error {
	switch {
	case !feedNamePattern.MatchString(name):
		return fmt.Errorf("feed 名稱 %q 不正確：只能是小寫英文、數字與 -（例如 show、my-show）", name)
	case len(name) > 40:
		return fmt.Errorf("feed 名稱 %q 太長（上限 40 個字元）", name)
	case genericFeedNames[name]:
		return fmt.Errorf("feed 名稱 %q 是泛用名稱，產品取 feed 代號時會略過它，請換一個名稱", name)
	}
	if table == nil || len(table.Rows) == 0 {
		return fmt.Errorf("feed %q 需要表格（表頭 guid、title、audio）", name)
	}
	header := make([]string, len(table.Rows[0].Cells))
	for i, c := range table.Rows[0].Cells {
		header[i] = strings.TrimSpace(c.Value)
	}
	if len(header) != 3 || header[0] != "guid" || header[1] != "title" || header[2] != "audio" {
		return fmt.Errorf("表頭應該是 guid、title、audio，收到 %v", header)
	}

	feed := fakepodcast.Feed{Name: name}
	type route struct {
		url string
		ref string
	}
	var routes []route
	seen := map[string]bool{}
	for i, row := range table.Rows[1:] {
		if len(row.Cells) != 3 {
			return fmt.Errorf("表格第 %d 列應有 guid、title、audio 三欄", i+1)
		}
		guid, title, audio := strings.TrimSpace(row.Cells[0].Value), strings.TrimSpace(row.Cells[1].Value), strings.TrimSpace(row.Cells[2].Value)
		if guid == "" || audio == "" {
			return fmt.Errorf("表格第 %d 列的 guid 與 audio 不可為空", i+1)
		}
		if seen[guid] {
			return fmt.Errorf("feed %q 的 guid %q 重複", name, guid)
		}
		seen[guid] = true

		audioURL, fixture, err := podcastAudioTarget(name, audio)
		if err != nil {
			return fmt.Errorf("guid %q：%w", guid, err)
		}
		b, err := s.fixtureFile("podcast", fixture)
		if err != nil {
			return err
		}
		if _, ok := faketool.ParseMedia(b); !ok {
			return fmt.Errorf("fixture podcast/%s 不是假媒體檔（第一行要是 NOTESTEME-FAKE-MEDIA 標記）", fixture)
		}
		feed.Episodes = append(feed.Episodes, fakepodcast.Episode{GUID: guid, Title: title, AudioURL: audioURL, Audio: b})
		routes = append(routes, route{url: audioURL, ref: fakepodcast.Ref(name, guid)})
	}
	if err := s.World.Podcasts.Put(feed); err != nil {
		return err
	}
	// feed 與音檔登記成 Route：只加路徑，不動主機的解析序列（rebind 主機先被 `resolves to … and then …` 設定）。
	net := s.World.Net
	if err := net.Route(fakepodcast.FeedURL(name), s.World.Podcasts.FeedHandler(name)); err != nil {
		return err
	}
	for _, r := range routes {
		if err := net.Route(r.url, s.World.Podcasts.AudioHandler(r.ref)); err != nil {
			return err
		}
	}
	return nil
}

// podcastAudioTarget 把表格的 audio 欄位換成音檔網址與 fixture 的檔名。
func podcastAudioTarget(feed, audio string) (audioURL, fixture string, err error) {
	if strings.Contains(audio, "://") {
		u, perr := url.Parse(audio)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", "", fmt.Errorf("audio %q 不是 http(s) 網址", audio)
		}
		fixture = path.Base(u.Path)
		if fixture == "." || fixture == "/" {
			return "", "", fmt.Errorf("audio %q 的路徑沒有檔名，無法對應 fixture", audio)
		}
		return audio, fixture, nil
	}
	if audio != filepath.Base(audio) || strings.ContainsAny(audio, `/\?#`) || strings.HasPrefix(audio, ".") {
		return "", "", fmt.Errorf("audio %q 不正確：只能是 features/fixtures/podcast/ 內的檔名，或完整網址", audio)
	}
	return fakepodcast.MediaURL(feed, audio), audio, nil
}

// podcastTranscriptTypes 是字幕檔副檔名對應的 Content-Type：只支援 vtt、srt、json。
var podcastTranscriptTypes = map[string]string{
	".vtt":  "text/vtt",
	".srt":  "application/x-subrip",
	".json": "application/json",
}

// podcastTranscriptReturns 讓這一集的 feed 帶有 <podcast:transcript>，字幕內容是 podcast/<fixture>。
// feed 或集不存在、副檔名不是 .vtt、.srt、.json、fixture 不存在時失敗。
func (s *Scenario) podcastTranscriptReturns(fixture, ref string) error {
	name, _, ok := fakepodcast.SplitRef(ref)
	if !ok {
		return fmt.Errorf("%q 不是 Podcast 的代號（pod:<feed>/<guid>）", ref)
	}
	if _, ok := s.World.Podcasts.Episode(ref); !ok {
		return fmt.Errorf("%s 不存在：先用 `a podcast feed … with the episodes:` 建立 feed 與這一集", ref)
	}
	ctype, ok := podcastTranscriptTypes[strings.ToLower(path.Ext(fixture))]
	if !ok {
		return fmt.Errorf("字幕 fixture %q 的副檔名不支援：只能是 .vtt、.srt、.json", fixture)
	}
	if fixture != filepath.Base(fixture) || strings.HasPrefix(fixture, ".") {
		return fmt.Errorf("字幕 fixture 名稱 %q 不正確：只能是 features/fixtures/podcast/ 內的檔名", fixture)
	}
	body, err := s.fixtureFile("podcast", fixture)
	if err != nil {
		return err
	}
	t := &fakepodcast.Transcript{URL: fakepodcast.MediaURL(name, fixture), Type: ctype, Body: body}
	if err := s.World.Podcasts.SetTranscript(ref, t); err != nil {
		return err
	}
	return s.World.Net.Route(t.URL, s.World.Podcasts.TranscriptHandler(ref))
}

// podcastTranscriptNothing 讓這一集的 feed 沒有字幕標籤。feed 或集不存在時失敗。
func (s *Scenario) podcastTranscriptNothing(ref string) error {
	if _, _, ok := fakepodcast.SplitRef(ref); !ok {
		return fmt.Errorf("%q 不是 Podcast 的代號（pod:<feed>/<guid>）", ref)
	}
	if _, ok := s.World.Podcasts.Episode(ref); !ok {
		return fmt.Errorf("%s 不存在：先用 `a podcast feed … with the episodes:` 建立 feed 與這一集", ref)
	}
	return s.World.Podcasts.SetTranscript(ref, nil)
}

// podcastIngestBody 是 pod:<feed>/<guid> 對應的 POST /api/items body：feed 網址加上要新增的集。
// 不是合法的 Podcast 代號時退回把整個字串當作網址，讓請求自己回報錯誤。
func podcastIngestBody(ref string) map[string]any {
	name, guid, ok := fakepodcast.SplitRef(ref)
	if !ok {
		return map[string]any{"url": ref}
	}
	return map[string]any{"url": fakepodcast.FeedURL(name), "episodes": []string{guid}}
}
