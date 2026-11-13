package steps

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	r.Step(`the auto captions of {string} are not ready yet`, s.autoCaptionsNotReady)
	r.Step(`the source {string} returns {string} for {string}`, s.sourceReturns)
	r.Step(`the source {string} has nothing for {string}`, s.sourceHasNothing)
	r.Step(`the source {string} requires the cookie {string}`, s.sourceRequiresCookie)
	r.Step(`the source {string} accepts files up to {string}`, s.sourceAcceptsUpTo)
	r.Step(`the audio of {string} is {string}`, func(string, string) error { return godog.ErrPending })
	r.Step(`yt-dlp fails for {string} with {string}`, s.ytdlpFails)
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
		// 沒有指定語言的影片，假 yt-dlp 回報英文：原文語言不明時，caption_priority 的 original 項目沒有符合，
		// 只有 titled 與字幕軌的場景（例如 CLI 的「Waiting until the transcript is ready」）就選不到字幕。
		// 這個預設值不算場景宣告的語言（見 declaredLanguage）；要宣告語言用 `a YouTube video … with:`。
		if v.Language == "" {
			v.Language = "en"
			if s.defaultedLang == nil {
				s.defaultedLang = map[string]bool{}
			}
			s.defaultedLang[ref] = true
		}
		return nil
	})
}

// declaredLanguage 回傳場景替影片宣告的原文語言：`titled` 補上的預設值不算。
// hasTranscriptSource 用它判斷場景有沒有準備字幕來源，這樣預設值不會改變 DEL 等場景目前的 pending
// （它們要等 fixtures/items/ 與各自的功能，見 pendingWithoutItemFixture）。
func (s *Scenario) declaredLanguage(ref string, v *faketool.Video) string {
	if s.defaultedLang[ref] {
		return ""
	}
	return v.Language
}

// youtubeVideoWith 依表格（field、value）設定影片的欄位。不認識的欄位讓步驟失敗，避免場景被默默忽略。
func (s *Scenario) youtubeVideoWith(ref string, table *godog.Table) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		for _, row := range table.Rows {
			if len(row.Cells) == 2 && row.Cells[0].Value == "language" {
				delete(s.defaultedLang, ref) // 場景自己宣告了語言
			}
		}
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

// autoCaptionsNotReady 讓影片的自動字幕（與平台翻譯字幕）尚未生成：假 yt-dlp 不列出、也不下載（SRC-R6）。
func (s *Scenario) autoCaptionsNotReady(ref string) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.AutoNotReady = true
		return nil
	})
}

// ytdlpFails 讓假 yt-dlp 對這部影片的所有操作（info、字幕、媒體）都以結束碼 1 失敗，並輸出指定的 stderr。
// 只接受 yt:<id>；Bilibili 等其他平台的假 yt-dlp 失敗還沒有場景用到。
func (s *Scenario) ytdlpFails(ref, stderr string) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Fail = &faketool.Failure{Stderr: stderr, ExitCode: 1}
		return nil
	})
}

func (s *Scenario) hasNoCaptions(ref string) error {
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Tracks = nil
		return nil
	})
}

// platformCaptionKind 是平台字幕來源對應的假 yt-dlp 字幕軌種類。
var platformCaptionKind = map[string]string{
	"youtube-manual": faketool.KindManual,
	"youtube-auto":   faketool.KindAuto,
}

// scriptableSource 確認場景能腳本化這個來源：產品還沒實作的來源（source.Implemented）
// 讓場景停在這個 Given，標為 pending；場景預期的行為不可能出現，不是靠事後把失敗轉成 pending。
// 平台字幕（youtube-*）由假 yt-dlp 提供，腳本化時寫進假 yt-dlp 的字幕軌狀態，不經過 fakesource。
func scriptableSource(id string) error {
	if !source.Implemented(id) {
		return fmt.Errorf("來源 %q 還沒有實作：%w", id, godog.ErrPending)
	}
	return nil
}

// platformTrackLang 決定腳本化平台字幕時使用的語言：這個種類既有的字幕軌（只能有一條），
// 沒有就用影片的語言；兩者都沒有時，場景沒有說明要用哪個語言，讓步驟失敗。
func platformTrackLang(v *faketool.Video, kind string) (string, error) {
	var langs []string
	for _, t := range v.Tracks {
		if t.Kind == kind {
			langs = append(langs, t.Lang)
		}
	}
	switch {
	case len(langs) == 1:
		return langs[0], nil
	case len(langs) > 1:
		return "", fmt.Errorf("影片有多條 %s 字幕軌（%v），無法判斷要腳本化哪一條", kind, langs)
	case v.Language != "":
		return v.Language, nil
	}
	return "", fmt.Errorf("影片沒有 %s 字幕軌，也沒有指定語言，無法決定腳本化字幕的語言（先用 `has … captions in …` 加入字幕軌）", kind)
}

// platformReturns 讓平台來源對影音回傳指定的 fixture：把 captions/ 內的檔案寫進假 yt-dlp 對應種類的字幕軌。
func (s *Scenario) platformReturns(id, fixture, ref string) error {
	kind := platformCaptionKind[id]
	if _, err := os.Stat(filepath.Join(s.World.FixturesDir, "captions", filepath.Clean("/"+fixture))); err != nil {
		return fmt.Errorf("字幕 fixture %q 不存在（features/fixtures/captions/）：%w", fixture, err)
	}
	return s.editVideo(ref, func(v *faketool.Video) error {
		lang, err := platformTrackLang(v, kind)
		if err != nil {
			return err
		}
		for i := range v.Tracks {
			if v.Tracks[i].Kind == kind && v.Tracks[i].Lang == lang {
				v.Tracks[i].Fixture, v.Tracks[i].Cues = fixture, 0
				return nil
			}
		}
		v.Tracks = append(v.Tracks, faketool.Track{Kind: kind, Lang: lang, Fixture: fixture})
		return nil
	})
}

// platformNothing 讓平台來源對影音沒有結果：移除假 yt-dlp 中這個種類的字幕軌。
func (s *Scenario) platformNothing(id, ref string) error {
	kind := platformCaptionKind[id]
	return s.editVideo(ref, func(v *faketool.Video) error {
		v.Tracks = slices.DeleteFunc(v.Tracks, func(t faketool.Track) bool { return t.Kind == kind })
		return nil
	})
}

// sourceReturns 讓來源對影音回傳指定的 fixture（ASR、Supadata 是腳本化的 fake origin，見 fakesource；
// 平台字幕寫進假 yt-dlp 的字幕軌）。
func (s *Scenario) sourceReturns(id, fixture, ref string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	if _, platform := platformCaptionKind[id]; platform {
		return s.platformReturns(id, fixture, ref)
	}
	s.World.Sources.Returns(id, ref, fixture)
	return nil
}

// sourceHasNothing 讓來源對影音沒有結果。
func (s *Scenario) sourceHasNothing(id, ref string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	if _, platform := platformCaptionKind[id]; platform {
		return s.platformNothing(id, ref)
	}
	s.World.Sources.Nothing(id, ref)
	return nil
}

// sourceRequiresCookie 讓來源要求指定的 cookie 才能取得。平台字幕由 yt-dlp 的 cookie 檔處理，
// 假 yt-dlp 還不能腳本化它：失敗而不是 pending，場景要用就先補上假工具的行為。
func (s *Scenario) sourceRequiresCookie(id, cookie string) error {
	if err := scriptableSource(id); err != nil {
		return err
	}
	if _, platform := platformCaptionKind[id]; platform {
		return fmt.Errorf("假 yt-dlp 還不能腳本化平台字幕 %q 的 cookie 需求", id)
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
