package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// audioTimeout 是下載音訊的逾時；音檔比字幕大得多。
const audioTimeout = 30 * time.Minute

// audioBase 是下載的音訊檔在 dir 中的固定檔名（不含副檔名）。
const audioBase = "audio"

// tooLargeLine 比對 yt-dlp 因超過 --max-filesize 而放棄下載的訊息（"File is larger than max-filesize (… bytes > … bytes)"）。
// yt-dlp 可能以結束碼 0 略過下載，也可能當成錯誤結束；字串依 yt-dlp 原始碼，尚未對真實環境驗證。
var tooLargeLine = regexp.MustCompile(`(?i)File is larger than max-filesize`)

// ErrAudioTooLarge 表示音檔超過 GRD-R5 的音檔上限（netguard.LimitAudio），yt-dlp 沒有下載。
var ErrAudioTooLarge = errors.New("音檔超過大小上限")

// ErrNoAudioFile 表示 yt-dlp 結束碼為 0，卻沒有寫出音訊檔。
var ErrNoAudioFile = errors.New("yt-dlp 沒有寫出音訊檔")

// DownloadAudio 下載影片的最佳音訊到 dir，回傳實際寫出的檔案路徑（副檔名由 yt-dlp 依格式決定，
// 例如 audio.m4a、audio.webm）。檔案留在 dir，讀完後由呼叫端清除 dir。
// 錯誤分類沿用 classify；yt-dlp 沒有寫出檔案時為 retryable（ErrNoAudioFile）。
func (c *Client) DownloadAudio(ctx context.Context, videoID, dir string) (string, error) {
	// 清掉上次殘留的同名檔，避免 yt-dlp 沒寫出檔案時誤用舊檔。
	for _, p := range audioFiles(dir, true) {
		_ = os.Remove(p)
	}
	// --max-filesize 套用 GRD-R5 的音檔上限：yt-dlp 的下載不經過 netguard，要靠它自己擋；用 bytes 避免單位（G／GB）的歧義。
	args := append(baseArgs(), "-f", "bestaudio", "--max-filesize", strconv.FormatInt(netguard.LimitAudio, 10),
		// -o 是 yt-dlp 的輸出模板，目錄中的 % 要跳脫成 %%；檔案實際寫在原本的 dir。
		"-o", filepath.Join(strings.ReplaceAll(dir, "%", "%%"), audioBase+".%(ext)s"), "--", WatchURL(videoID))
	res, err := c.run(ctx, audioTimeout, args)
	if err != nil {
		return "", err
	}
	files := audioFiles(dir, false)
	if len(files) == 0 {
		// 超過上限時 yt-dlp 可能以結束碼 0 略過下載；重試也不會變小，不是暫時性錯誤。
		if tooLargeLine.Match(res.Stdout) || tooLargeLine.Match(res.Stderr) {
			return "", &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Err: ErrAudioTooLarge}
		}
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: ErrNoAudioFile}
	}
	return files[0], nil
}

// audioFiles 列出 dir 中的 audio.* 檔案。
func audioFiles(dir string, includePartial bool) []string {
	return mediaFiles(dir, audioBase, includePartial)
}

// mediaFiles 列出 dir 中的 <base>.* 檔案（依檔名排序）；includePartial 為 false 時略過 yt-dlp 的暫存檔。
func mediaFiles(dir, base string, includePartial bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, base+".") {
			continue
		}
		if !includePartial && (strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") || strings.HasSuffix(name, ".temp")) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

// videoBase 是下載的影片檔在 dir 中的固定檔名（不含副檔名）。
const videoBase = "video"

// VideoFormat 是關鍵畫面下載的格式：不超過 1920×1080 的最佳影像串流（FRM-R8），只要影像，不要音訊。
const VideoFormat = "bv*[height<=1080][width<=1920]/b[height<=1080][width<=1920]"

// ErrNoVideoFile 表示 yt-dlp 結束碼為 0，卻沒有寫出影片檔。
var ErrNoVideoFile = errors.New("yt-dlp 沒有寫出影片檔")

// DownloadVideo 下載不超過 1080p 的最佳影像到 dir（FRM-R8），回傳實際寫出的檔案路徑（video.<ext>）。
// 上限沿用 GRD-R5 的音檔上限（netguard.LimitAudio）：yt-dlp 的下載不經過 netguard。
func (c *Client) DownloadVideo(ctx context.Context, videoID, dir string) (string, error) {
	for _, p := range mediaFiles(dir, videoBase, true) {
		_ = os.Remove(p)
	}
	args := append(baseArgs(), "-f", VideoFormat, "--max-filesize", strconv.FormatInt(netguard.LimitAudio, 10),
		"-o", filepath.Join(strings.ReplaceAll(dir, "%", "%%"), videoBase+".%(ext)s"), "--", WatchURL(videoID))
	res, err := c.run(ctx, audioTimeout, args)
	if err != nil {
		return "", err
	}
	files := mediaFiles(dir, videoBase, false)
	if len(files) == 0 {
		if tooLargeLine.Match(res.Stdout) || tooLargeLine.Match(res.Stderr) {
			return "", &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Err: ErrAudioTooLarge,
				Message: "影片超過大小上限，無法擷取關鍵畫面。"}
		}
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: ErrNoVideoFile}
	}
	return files[0], nil
}
