package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// playlistTimeout 是展開播放清單的上限：清單很長時，yt-dlp 要分頁取得，比單一影片久。
const playlistTimeout = 5 * time.Minute

// ErrPlaylistGone 表示播放清單不存在或不是公開的；重試也不會成功。
var ErrPlaylistGone = errors.New("播放清單不存在或不是公開的")

// playlistGoneLine 比對 yt-dlp 回報「播放清單不存在或無法檢視」的錯誤行：
// youtube:tab extractor 在 stderr 輸出 "ERROR: [youtube:tab] <id>: <原因>"。
// 原因的依據是 yt-dlp 的 YouTube extractor 原始碼，沒有在此環境對真實的 yt-dlp 驗證（測試不連外網）。
var playlistGoneLine = regexp.MustCompile(
	`(?im)^ERROR: \[youtube:tab\] \S+: (?:The playlist does not exist|This playlist is private|This playlist type is unviewable)`)

// PlaylistURL 回傳播放清單的正規網址；呼叫 yt-dlp 時使用它，不直接使用使用者貼上的字串。
func PlaylistURL(id string) string {
	return "https://www.youtube.com/playlist?list=" + url.QueryEscape(id)
}

// ValidVideoID 回報 id 是否是 11 個字元的影片 ID（ING-R1）。
func ValidVideoID(id string) bool { return videoID.MatchString(id) }

// PlaylistEntry 是播放清單中的一部影片。
type PlaylistEntry struct{ ID, Title string }

// Playlist 以 yt-dlp --flat-playlist -J 展開播放清單，只取得各影片的 ID 與標題，不逐一抓取影片資訊。
//
// 清單的內容是不可信的資料：沒有合法 11 字元 ID 的項目（已刪除、不可用）會略過。
// 不帶 --no-playlist：那個旗標會讓 yt-dlp 不列出清單內容。
// 錯誤：播放清單不存在或私人時是 permanent，並且 errors.Is(err, ErrPlaylistGone)；
// 找不到 yt-dlp 是 needs_action；其他失敗與無法解析的輸出是 retryable。
func (c *Client) Playlist(ctx context.Context, playlistID string) ([]PlaylistEntry, error) {
	// yt-dlp 以 fullmatch 比對 --use-extractors 的 IE_NAME；播放清單由 youtube:tab 與 youtube:playlist 處理，
	// 只寫 youtube 時，真實的 yt-dlp 會回報 Unsupported URL。
	args := []string{"--ignore-config", "--use-extractors", "youtube:tab,youtube:playlist", "--no-warnings", "--flat-playlist", "-J", "--", PlaylistURL(playlistID)}
	res, err := c.run(ctx, playlistTimeout, args)
	if err != nil {
		if res.ExitCode != 0 && playlistGoneLine.Match(res.Stderr) {
			return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", ExitCode: res.ExitCode,
				Detail: tail(res.Stderr, 300), Err: ErrPlaylistGone}
		}
		return nil, err
	}
	var raw struct {
		// 指標：已刪除的項目在 yt-dlp 的輸出中可能是 null。
		Entries []*struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(res.Stdout, &raw); err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "無法解析播放清單的輸出", Err: err}
	}
	out := make([]PlaylistEntry, 0, len(raw.Entries))
	for _, e := range raw.Entries {
		if e != nil && ValidVideoID(e.ID) {
			out = append(out, PlaylistEntry{ID: e.ID, Title: e.Title})
		}
	}
	return out, nil
}
