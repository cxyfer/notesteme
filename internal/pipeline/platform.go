package pipeline

import (
	"context"
	"fmt"
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
}

// platformAdapter 是步驟用到的平台功能；依 items.platform 分派（見 Pipeline.adapterFor）。
type platformAdapter interface {
	probe(ctx context.Context, it store.Item) (*probed, error)
	// fetchCues 下載一條字幕軌（暫存檔寫在 dir）並解析成 cue。
	fetchCues(ctx context.Context, it store.Item, t transcript.Track, dir string) ([]subtitle.Cue, error)
	// version 回傳 yt-dlp 的版本，寫進來源紀錄。
	version(ctx context.Context) (string, error)
}

// adapterFor 回傳平台的 adapter。沒有註冊的平台（還沒實作，或依賴沒有設定）是 permanent 錯誤：重試也不會成功。
func (p *Pipeline) adapterFor(platform string) (platformAdapter, error) {
	switch {
	case platform == source.PlatformYouTube && p.yt != nil:
		return youtubeAdapter{p.yt}, nil
	case platform == source.PlatformBilibili && p.bili != nil:
		return bilibiliAdapter{p.bili}, nil
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

func (a youtubeAdapter) version(ctx context.Context) (string, error) { return a.yt.Version(ctx) }

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

func (a bilibiliAdapter) version(ctx context.Context) (string, error) { return a.b.Version(ctx) }

// bilibiliPart 是 Bilibili 的網址辨識結果轉成的分P Target。
func bilibiliPart(bvid string, part int) source.Target {
	return source.Target{
		Platform: source.PlatformBilibili, Kind: source.KindVideo, ExternalID: bvid, Part: part, URL: bilibili.VideoURL(bvid, part),
	}
}
