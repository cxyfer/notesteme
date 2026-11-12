package steps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/source"
)

// registerSources 註冊「平台與來源的 fixture」的步驟，句型見 docs/specs/steps.md。
func registerSources(r *Registry, s *Scenario) {
	r.Step(`a YouTube video {string} titled {string}`, s.youtubeVideoTitled)
	r.Step(`a YouTube video {string} with:`, s.youtubeVideoWith)
	r.Step(`a Bilibili video {string} with {int} parts`, func(string, int) error { return godog.ErrPending })
	r.Step(`a podcast feed {string} with the episodes:`, func(string, *godog.Table) error { return godog.ErrPending })
	r.Step(`a local file {string} in the inbox`, func(string) error { return godog.ErrPending })
	r.Step(`{string} has {word} captions in {string}`, s.hasCaptions)
	r.Step(`{string} has no caption tracks`, s.hasNoCaptions)
	r.Step(`the auto captions of {string} are not ready yet`, func(string) error { return godog.ErrPending })
	r.Step(`the source {string} returns {string} for {string}`, s.sourceReturns)
	r.Step(`the source {string} has nothing for {string}`, s.sourceHasNothing)
	r.Step(`the source {string} requires the cookie {string}`, s.sourceRequiresCookie)
	r.Step(`the source {string} accepts files up to {string}`, s.sourceAcceptsUpTo)
	r.Step(`the audio of {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`yt-dlp fails for {string} with {string}`, func(string, string) error { return godog.ErrPending })
}

// youtubeID 取出 yt:<id> 代號中的平台 ID。
func youtubeID(ref string) (string, error) {
	id, ok := strings.CutPrefix(ref, "yt:")
	if !ok || id == "" {
		return "", fmt.Errorf("%q 不是 YouTube 影片的代號（yt:<id>）", ref)
	}
	return id, nil
}

// editVideo 修改假工具狀態中的影片（必要時建立），成功後寫入狀態檔。
func (s *Scenario) editVideo(ref string, edit func(v *faketool.Video) error) error {
	id, err := youtubeID(ref)
	if err != nil {
		return err
	}
	if err := edit(s.World.Video(id)); err != nil {
		return err
	}
	return s.World.SyncTools()
}

func (s *Scenario) youtubeVideoTitled(ref, title string) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Title = title
		return nil
	})
}

// youtubeVideoWith 依表格（field、value）設定影片的欄位。不認識的欄位讓步驟失敗，避免場景被默默忽略。
func (s *Scenario) youtubeVideoWith(ref string, table *godog.Table) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		for i, row := range table.Rows {
			if len(row.Cells) != 2 {
				return fmt.Errorf("表格第 %d 列應有 field 與 value 兩欄", i+1)
			}
			field, value := row.Cells[0].Value, row.Cells[1].Value
			if i == 0 && field == "field" && value == "value" {
				continue // 表頭
			}
			if err := setVideoField(v, field, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func setVideoField(v *faketool.Video, field, value string) error {
	switch field {
	case "title":
		v.Title = value
	case "description":
		v.Description = value
	case "language":
		v.Language = value
	case "availability":
		v.Availability = value
	case "live_status":
		v.LiveStatus = value
	case "channel":
		v.Channel = value
	case "channel_id":
		v.ChannelID = value
	case "duration":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("duration %q 不是秒數", value)
		}
		v.Duration = n
	case "published_at":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("published_at %q 不是 RFC 3339：%w", value, err)
		}
		v.PublishedAt = value
	case "chapters":
		chapters, err := parseChapters(value, v.Duration)
		if err != nil {
			return err
		}
		v.Chapters = chapters
	default:
		return fmt.Errorf("YouTube 影片不支援欄位 %q", field)
	}
	return nil
}

// parseChapters 解析 "00:00 Intro; 05:00 Main; 12:00 Q&A"：每章的結束是下一章的開始，最後一章到片尾。
func parseChapters(spec string, durationSec int) ([]faketool.Chapter, error) {
	var out []faketool.Chapter
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		stamp, title, ok := strings.Cut(part, " ")
		if !ok {
			return nil, fmt.Errorf("章節 %q 應是「時間 標題」", part)
		}
		start, err := parseClock(stamp)
		if err != nil {
			return nil, err
		}
		out = append(out, faketool.Chapter{Title: strings.TrimSpace(title), Start: start})
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].End = out[i+1].Start
		} else {
			out[i].End = max(float64(durationSec), out[i].Start)
		}
	}
	return out, nil
}

// parseClock 解析 mm:ss 或 hh:mm:ss。
func parseClock(s string) (float64, error) {
	var total float64
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("時間 %q 應是 mm:ss 或 hh:mm:ss", s)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("時間 %q 不正確", s)
		}
		total = total*60 + float64(n)
	}
	return total, nil
}

// hasCaptions 加入一條平台字幕軌；同一語言與種類已存在時取代。
// 沒有對應的 fixture 時，假 yt-dlp 產生 120 句的字幕（steps.md 第 4 節）。
func (s *Scenario) hasCaptions(ref, kind, lang string) error {
	switch kind {
	case faketool.KindManual, faketool.KindAuto, faketool.KindPlatformTranslated:
	default:
		return fmt.Errorf("字幕軌的種類 %q 不正確，應是 manual、auto 或 platform-translated", kind)
	}
	return s.editVideo(ref, func(v *faketool.Video) error {
		for i, t := range v.Tracks {
			if t.Kind == kind && t.Lang == lang {
				v.Tracks = append(v.Tracks[:i], v.Tracks[i+1:]...)
				break
			}
		}
		v.Tracks = append(v.Tracks, faketool.Track{Kind: kind, Lang: lang})
		return nil
	})
}

func (s *Scenario) hasNoCaptions(ref string) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Tracks = nil
		return nil
	})
}

// scriptableSource 確認場景能腳本化這個來源：
//   - 產品還沒實作的來源（source.Implemented）讓場景停在這個 Given，標為 pending；
//     場景預期的行為不可能出現，不是靠事後把失敗轉成 pending。
//   - 平台字幕（youtube-*）由假 yt-dlp 提供，不經過 fakesource，這個步驟無法腳本化它們。
func scriptableSource(id string) error {
	if !source.Implemented(id) {
		return fmt.Errorf("來源 %q 還沒有實作：%w", id, godog.ErrPending)
	}
	if _, platform := platformCaptionFlag[id]; platform {
		return fmt.Errorf("平台字幕 %q 由假 yt-dlp 提供，這個步驟還不能腳本化它：%w", id, godog.ErrPending)
	}
	return nil
}

// sourceReturns 讓來源對影音回傳指定的 fixture（腳本化的 fake origin，見 fakesource）。
func (s *Scenario) sourceReturns(id, fixture, ref string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	s.World.Sources.Returns(id, ref, fixture)
	return nil
}

// sourceHasNothing 讓來源對影音沒有結果。
func (s *Scenario) sourceHasNothing(id, ref string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	s.World.Sources.Nothing(id, ref)
	return nil
}

// sourceRequiresCookie 讓來源要求指定的 cookie 才能取得。
func (s *Scenario) sourceRequiresCookie(id, cookie string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	s.World.Sources.RequiresCookie(id, cookie)
	return nil
}

var sizePattern = regexp.MustCompile(`^(\d+)\s*(KB|MB|GB)$`)

// sourceAcceptsUpTo 設定來源的單檔大小上限，例如 "25MB"。
func (s *Scenario) sourceAcceptsUpTo(id, size string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	m := sizePattern.FindStringSubmatch(size)
	if m == nil {
		return fmt.Errorf("大小 %q 應是數字加 KB、MB 或 GB", size)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	unit := map[string]int64{"KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30}[m[2]]
	s.World.Sources.AcceptsUpTo(id, n*unit)
	return nil
}
