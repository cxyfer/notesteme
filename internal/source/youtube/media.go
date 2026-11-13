package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// audioTimeout 是下載音訊的逾時；音檔比字幕大得多。
const audioTimeout = 30 * time.Minute

// audioBase 是下載的音訊檔在 dir 中的固定檔名（不含副檔名）。
const audioBase = "audio"

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
	args := append(baseArgs(), "-f", "bestaudio",
		// -o 是 yt-dlp 的輸出模板，目錄中的 % 要跳脫成 %%；檔案實際寫在原本的 dir。
		"-o", filepath.Join(strings.ReplaceAll(dir, "%", "%%"), audioBase+".%(ext)s"), "--", WatchURL(videoID))
	if _, err := c.run(ctx, audioTimeout, args); err != nil {
		return "", err
	}
	files := audioFiles(dir, false)
	if len(files) == 0 {
		return "", &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Err: ErrNoAudioFile}
	}
	return files[0], nil
}

// audioFiles 列出 dir 中的 audio.* 檔案（依檔名排序）；includePartial 為 false 時略過 yt-dlp 的暫存檔。
func audioFiles(dir string, includePartial bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, audioBase+".") {
			continue
		}
		if !includePartial && (strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") || strings.HasSuffix(name, ".temp")) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}
