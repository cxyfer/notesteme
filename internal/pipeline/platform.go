package pipeline

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// probed 是各平台 Probe 的共同結果：步驟只依賴它，不認得平台自己的型別。
type probed struct {
	Title        string
	DurationS    *int
	Language     string // 平台沒有語言中繼資料（Bilibili）時為空
	Availability string
	// Live 為 true 代表還在直播或尚未首播（SRC-R7）。
	Live        bool
	LiveStatus  string
	Description string
	Chapters    []youtube.Chapter
	// Channel* 是頻道（YouTube 的頻道、Bilibili 的 UP 主）；ChannelID 為空代表沒有。
	ChannelID, ChannelTitle, ChannelURL string
	PublishedAt                         *time.Time
	PublishedAtApprox                   bool
	// Tracks 是平台字幕軌，已轉成與平台無關的形狀（Bilibili 的彈幕已排除）。
	Tracks []transcript.Track
	// NeedsLogin 為 true 代表平台說明「登入後才看得到字幕」（Bilibili，SRC-R8）。
	NeedsLogin bool
	// MediaKind 是本機影音的種類（video 或 audio）；其他平台為空。
	MediaKind string
	// ContentSHA256 是本機檔案內容的 sha256（影音上已有的，或這次剛算出的）；其他平台為空。
	ContentSHA256 string
	// ComputedSHA256 只在這次探測才算出 sha256 時有值：metadata 步驟把它寫進影音（DEL-R11 需要它）。
	ComputedSHA256 string
	// LocalStat 是非上傳的本機檔案目前的 size 與 mtime；其他情況為 nil。
	// 它記在 metadata_json 的 local，下次探測時用來判斷檔案有沒有被替換。
	LocalStat *localStat
	// AudioDurationS 是 Podcast 音檔的實際長度（metadata 下載時 ffprobe 量到的）；0 代表未知。SRC-R15 的偏移檢查用它。
	AudioDurationS float64
}

// platformAdapter 是步驟用到的平台功能；依 items.platform 分派（見 Pipeline.adapterFor）。
type platformAdapter interface {
	probe(ctx context.Context, it store.Item) (*probed, error)
	// fetchCues 下載一條字幕軌（暫存檔寫在 dir）並解析成 cue。
	fetchCues(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]subtitle.Cue, error)
	// toolVersions 回傳字幕來源用到的外部工具版本（YouTube 與 Bilibili 是 yt-dlp，本機是 ffmpeg 與 ffprobe），
	// 寫進來源紀錄的 tool_versions。
	toolVersions(ctx context.Context) (map[string]string, error)
}

// adapterFor 回傳平台的 adapter。沒有註冊的平台（還沒實作，或依賴沒有設定）是 permanent 錯誤：重試也不會成功。
func (p *Pipeline) adapterFor(platform string) (platformAdapter, error) {
	switch {
	case platform == source.PlatformYouTube && p.yt != nil:
		return youtubeAdapter{p.yt}, nil
	case platform == source.PlatformBilibili && p.bili != nil:
		return bilibiliAdapter{p.bili}, nil
	case platform == source.PlatformLocal && p.files != nil:
		return localAdapter{files: p.files, media: p.media, dataDir: p.dataDir}, nil
	case platform == source.PlatformPodcast && p.http != nil:
		return podcastAdapter{http: p.http}, nil
	}
	return nil, errPermanent(fmt.Sprintf("還不支援 %s 的處理", platform))
}

type youtubeAdapter struct{ yt YouTube }

func (a youtubeAdapter) probe(ctx context.Context, it store.Item) (*probed, error) {
	info, err := a.yt.Probe(ctx, it.ExternalID)
	if err != nil {
		return nil, err
	}
	out := &probed{
		Title: info.Title, DurationS: info.DurationS, Language: info.Language, Availability: info.Availability,
		Live: info.Live(), LiveStatus: info.LiveStatus, Description: info.Description, Chapters: info.Chapters,
		PublishedAt: info.PublishedAt, PublishedAtApprox: info.PublishedAtApprox, ChannelTitle: info.Channel,
	}
	if info.ChannelID != "" {
		out.ChannelID, out.ChannelURL = info.ChannelID, "https://www.youtube.com/channel/"+info.ChannelID
	}
	for _, t := range info.Tracks {
		out.Tracks = append(out.Tracks, transcript.Track{Lang: t.Lang, Kind: t.Kind})
	}
	return out, nil
}

func (a youtubeAdapter) fetchCues(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]subtitle.Cue, error) {
	data, err := a.yt.FetchCaptions(ctx, it.ExternalID, youtube.Track{Lang: t.Lang, Kind: t.Kind}, dir)
	if err != nil {
		return nil, err
	}
	cues, err := subtitle.ParseVTT(data)
	if err != nil {
		// 平台給的檔案格式不對，重試也不會變好。
		return nil, errPermanent(fmt.Sprintf("字幕檔無法解析：%v", err))
	}
	return cues, nil
}

func (a youtubeAdapter) toolVersions(ctx context.Context) (map[string]string, error) {
	v, err := a.yt.Version(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"yt-dlp": v}, nil
}

type bilibiliAdapter struct{ b Bilibili }

func (a bilibiliAdapter) probe(ctx context.Context, it store.Item) (*probed, error) {
	info, err := a.b.Probe(ctx, it.ExternalID, it.Part)
	if err != nil {
		return nil, err
	}
	out := &probed{
		Title: info.Title, DurationS: info.DurationS, Availability: info.Availability, Description: info.Description,
		PublishedAt: info.PublishedAt, Tracks: transcript.BilibiliTracks(info.Tracks), NeedsLogin: info.NeedsLogin,
	}
	out.ChannelTitle = info.Uploader
	if info.UploaderID != "" {
		out.ChannelID, out.ChannelURL = info.UploaderID, "https://space.bilibili.com/"+info.UploaderID
	}
	return out, nil
}

func (a bilibiliAdapter) fetchCues(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]subtitle.Cue, error) {
	data, err := a.b.FetchCaptions(ctx, it.ExternalID, it.Part, transcript.BilibiliKey(t), dir)
	if err != nil {
		return nil, err
	}
	cues, err := subtitle.ParseSRT(data)
	if err != nil {
		return nil, errPermanent(fmt.Sprintf("字幕檔無法解析：%v", err))
	}
	return cues, nil
}

func (a bilibiliAdapter) toolVersions(ctx context.Context) (map[string]string, error) {
	v, err := a.b.Version(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"yt-dlp": v}, nil
}

// cacheProvider 是快取鍵的 provider：本機影音是 local，Podcast 是 podcast，其餘平台透過 yt-dlp。
func cacheProvider(platform string) string {
	switch platform {
	case source.PlatformLocal:
		return "local"
	case source.PlatformPodcast:
		return "podcast"
	}
	return "yt-dlp"
}

// selectTracks 依平台的規則選字幕軌：本機影音與 Podcast 有自己的規則（SRC-R14），其餘平台用 caption_priority。
// metadata 判斷資源類別與 transcript 選軌共用它，兩邊才會對同一部影音得到相同的結論。
func selectTracks(platform string, tracks []transcript.Track, originalLang string, lang Language) (*transcript.Track, []transcript.Decision) {
	switch platform {
	case source.PlatformLocal:
		return transcript.SelectLocal(tracks, originalLang, lang.CaptionPriority)
	case source.PlatformPodcast:
		return transcript.SelectPodcast(tracks, originalLang, lang.CaptionPriority)
	}
	return transcript.SelectFor(platform, tracks, originalLang, lang.CaptionPriority, lang.AllowPlatformTranslated)
}

// bilibiliPart 是 Bilibili 的網址辨識結果轉成的分P Target。
func bilibiliPart(bvid string, part int) source.Target {
	return source.Target{
		Platform: source.PlatformBilibili, Kind: source.KindVideo, ExternalID: bvid, Part: part, URL: bilibili.VideoURL(bvid, part),
	}
}

// inferOriginalLang 回傳影音的原文語言：current 已有值就用它。
//   - Bilibili 沒有語言中繼資料，看得到 AI 字幕軌時用它的語言推斷（inferred 為 true）。
//   - 本機影音沒有語言中繼資料（容器的音訊語言標記不採用）：用選中的本機字幕的語言推斷。
//   - Podcast 的 feed 沒有 <language> 時：用選中的 <podcast:transcript> 的語言推斷。
//
// metadata 判斷資源類別與 transcript 選軌共用這個推斷，兩邊才會對同一部影音得到相同的結論。
func inferOriginalLang(platform, current string, tracks []transcript.Track, lang Language) (string, bool) {
	if current != "" {
		return current, false
	}
	switch platform {
	case source.PlatformBilibili:
		if i := slices.IndexFunc(tracks, func(t transcript.Track) bool { return t.Kind == transcript.KindAuto }); i >= 0 {
			return tracks[i].Lang, true
		}
	case source.PlatformLocal:
		if chosen, _ := transcript.SelectLocal(tracks, "", lang.CaptionPriority); chosen != nil && chosen.Lang != "" {
			return transcript.NormalizeLang(chosen.Lang), true
		}
	case source.PlatformPodcast:
		if chosen, _ := transcript.SelectPodcast(tracks, "", lang.CaptionPriority); chosen != nil && chosen.Lang != "" {
			return transcript.NormalizeLang(chosen.Lang), true
		}
	}
	return "", false
}
