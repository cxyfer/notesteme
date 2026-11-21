package pipeline

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/store"
)

// audioFetchTimeout 是下載一集音檔的逾時（大小上限是 netguard.LimitAudio，GRD-R5）。
const audioFetchTimeout = 2 * time.Hour

// episodeFilePattern 是下載後的音檔檔名：episode.<副檔名>；媒體端點只開啟符合這個格式的檔名。
var episodeFilePattern = regexp.MustCompile(`^episode\.[a-z0-9]{1,5}$`)

// audioExts 是音檔副檔名的白名單；其他一律 .bin。
var audioExts = map[string]bool{
	".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true,
	".flac": true, ".mp4": true, ".webm": true, ".mov": true,
}

// audioTypeExts 在網址看不出副檔名時，依 enclosure 的 type 決定副檔名。
var audioTypeExts = map[string]string{
	"audio/mpeg": ".mp3", "audio/mp3": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/m4a": ".m4a",
	"audio/aac": ".aac", "audio/ogg": ".ogg", "audio/opus": ".opus", "audio/wav": ".wav", "audio/x-wav": ".wav",
	"audio/flac": ".flac", "video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
}

// episodeAudio 是下載完成的音檔，也是 metadata_json 的 audio 區段。
type episodeAudio struct {
	File      string  `json:"file"`
	URL       string  `json:"url"` // 轉址之後的網址
	Bytes     int64   `json:"bytes"`
	SHA256    string  `json:"sha256"`
	DurationS float64 `json:"duration_s"`

	mediaKind string // ffprobe 判斷的 audio 或 video；不進 metadata_json
}

// audioDownloader 下載 Podcast 單集的音檔。metadata 步驟與 ASR 的音訊來源共用它。
type audioDownloader struct {
	http    *http.Client
	media   *media.Tool
	dataDir string
	version string
	// st 用來在寫入最後的檔案之前確認影音還在媒體庫中（垃圾桶與永久刪除會清掉媒體目錄）；nil 代表不檢查。
	st *store.Store
}

// downloadEpisodeAudio 下載這一集的音檔到 <data>/media/<id>/（DEL-R2）；每次 metadata 都重新下載（沒有磁碟空間檢查，技術債）。
// 失敗時錯誤只用 %w 包裝，netguard.PolicyMessage 才認得出被防護擋下的原因（classify 先判斷它）。
func (p *Pipeline) downloadEpisodeAudio(ctx context.Context, it store.Item, enc podcast.Enclosure) (*episodeAudio, error) {
	return p.audioDownloader().download(ctx, it, enc, "")
}

// alive 確認影音還在媒體庫中：已不存在或在垃圾桶中回傳 errItemGone。
func (d audioDownloader) alive(ctx context.Context, itemID string) error {
	if d.st == nil {
		return nil
	}
	cur, err := d.st.Read().ItemByID(ctx, itemID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errItemGone
	case err != nil:
		return err
	case cur.DeletedAt != nil:
		return errItemGone
	}
	return nil
}

func (p *Pipeline) audioDownloader() audioDownloader {
	return audioDownloader{http: p.http, media: p.media, dataDir: p.dataDir, version: p.version, st: p.st}
}

// download 下載 enc 並存成 episode<ext>（name 非空時用它當檔名，ASR 重新下載時沿用快照記錄的檔名）。
// 流程：寫到 .part、同時算 sha256、fsync、ffprobe 取得長度，通過之後才 rename；任何失敗都刪除 .part。
func (d audioDownloader) download(ctx context.Context, it store.Item, enc podcast.Enclosure, name string) (*episodeAudio, error) {
	if d.http == nil {
		return nil, errPermanent("還不支援 podcast 的處理")
	}
	dir, err := filepath.Abs(MediaCacheDir(d.dataDir, it.ID))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, audioFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, enc.URL, nil)
	if err != nil {
		return nil, podcastPermanent("音檔網址不正確。", err)
	}
	req.Header.Set("Accept", "audio/*, video/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "Notesteme/"+cmp.Or(d.version, "dev"))
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下載音檔：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if err := statusError(resp.StatusCode, "音檔"); err != nil {
			return nil, err
		}
		// 其他 2xx（例如 204、206）不是完整的音檔。
		return nil, podcastPermanent(fmt.Sprintf("音檔下載失敗（HTTP %d）。", resp.StatusCode), nil)
	}

	f, err := os.CreateTemp(dir, ".episode-*.part")
	if err != nil {
		return nil, err
	}
	part := f.Name()
	done := false
	defer func() {
		if !done {
			_ = f.Close()
			_ = os.Remove(part)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), netguard.LimitBody(resp, netguard.LimitAudio))
	if err != nil {
		return nil, fmt.Errorf("下載音檔：%w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	if d.media == nil {
		return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "ffprobe", Message: msgNoMediaTool, Actions: []string{"安裝 ffprobe"}}
	}
	info, err := d.media.Probe(ctx, part)
	if err != nil {
		return nil, err
	}
	if err := d.alive(ctx, it.ID); err != nil {
		return nil, err
	}

	finalURL := enc.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if name == "" {
		name = "episode" + audioExt(finalURL, enc.Type)
	}
	if err := os.Rename(part, filepath.Join(dir, name)); err != nil {
		return nil, err
	}
	done = true
	removeStaleEpisodes(dir, name)
	return &episodeAudio{
		File: name, URL: finalURL, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil)),
		DurationS: info.DurationS, mediaKind: info.Kind(),
	}, nil
}

// removeStaleEpisodes 刪除先前下載留下的其他 episode.* 檔（副檔名可能不同）。
func removeStaleEpisodes(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep && episodeFilePattern.MatchString(e.Name()) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// audioExt 決定音檔的副檔名：先看轉址後網址的副檔名，再看 enclosure 的 type；都不在白名單就是 .bin。
func audioExt(rawURL, mediaType string) string {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		p = u.Path
	}
	if ext := strings.ToLower(path.Ext(p)); audioExts[ext] {
		return ext
	}
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if ext, ok := audioTypeExts[mt]; ok {
		return ext
	}
	return ".bin"
}

// podcastAudioSource 是 Podcast 的 ASR 音訊來源：metadata 下載的音檔還在就直接用，
// 否則（被清掉）用快照記錄的 enclosure 重新下載。
type podcastAudioSource struct{ dl audioDownloader }

func (s podcastAudioSource) fetch(ctx context.Context, it store.Item, _ string) (string, map[string]string, error) {
	m, ok := readPodcastMeta(it.Metadata)
	if !ok {
		return "", nil, podcastPermanent(msgPodcastNoSnapshot, nil)
	}
	dir, err := filepath.Abs(MediaCacheDir(s.dl.dataDir, it.ID))
	if err != nil {
		return "", nil, err
	}
	name := m.Audio.File
	if !episodeFilePattern.MatchString(name) {
		return "", nil, podcastPermanent(msgPodcastNoSnapshot, nil)
	}
	cached := filepath.Join(dir, name)
	// 檔案驗證完成後才 rename 進來，所以存在且是一般檔案就可以沿用；不比對大小，
	// 否則動態廣告讓大小改變之後，每次轉錄都會重新下載。
	if fi, err := os.Stat(cached); err == nil && fi.Mode().IsRegular() {
		return cached, nil, nil
	}
	enc := podcast.Enclosure{URL: m.Podcast.Enclosure.URL, Type: m.Podcast.Enclosure.Type, Length: m.Podcast.Enclosure.Length}
	got, err := s.dl.download(ctx, it, enc, name)
	if err != nil {
		return "", nil, &downloadError{err}
	}
	return filepath.Join(dir, got.File), nil, nil
}
