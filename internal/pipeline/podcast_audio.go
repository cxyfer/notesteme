package pipeline

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/store"
)

// audioFetchTimeout 是下載一集音檔的逾時（大小上限是 netguard.LimitAudio，GRD-R5）。
const audioFetchTimeout = 2 * time.Hour

// episodeFilePattern 是下載後的音檔檔名：episode.<副檔名>，或撞名時的 episode-<8 碼 16 進位>.<副檔名>（sha256 前 8 碼或隨機）；
// 媒體端點只開啟符合這個格式的檔名。
var episodeFilePattern = regexp.MustCompile(`^episode(-[0-9a-f]{8})?\.[a-z0-9]{1,5}$`)

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
	// created 為 true 代表 File 是這次下載建立的新檔（不是沿用快照記錄的檔名）；
	// 結果被丟棄時只能刪這種檔案。不進 metadata_json。
	created bool
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
	removeStaleParts(dir, time.Now())
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
	sum := hex.EncodeToString(h.Sum(nil))
	// 這次的結果要等 apply 的交易 commit 才生效，所以 commit 之前不能蓋掉別的檔案：
	// 影音現在指向的快照檔（任務被取消、過期被丟棄或寫回失敗時仍要播放它），
	// 以及同時進行的另一個 metadata 任務已經暫存、之後可能 commit 的檔案。
	// 預設檔名 episode.<ext> 用 O_EXCL 保留（檔名已存在就失敗），撞名時改用 episode-<sha256 前 8 碼>.<ext>，再撞名用隨機的檔名；
	// commit 之後快照指向最後用的檔名，其他檔由 runPodcastMetadata 的 afterCommit 刪除。
	// name 非空（ASR 重新下載）時沿用快照記錄的檔名，直接覆蓋。
	created := false
	if name != "" {
		if err := recoverEpisodeFile(part, dir, name); err != nil {
			return nil, err
		}
	} else {
		var err error
		if name, err = stageEpisodeFile(part, dir, audioExt(finalURL, enc.Type), sum); err != nil {
			return nil, err
		}
		created = true
	}
	done = true
	return &episodeAudio{
		File: name, URL: finalURL, Bytes: n, SHA256: sum,
		DurationS: info.DurationS, mediaKind: info.Kind(), created: created,
	}, nil
}

// recoverEpisodeFile 把 ASR 重新下載的 part 放回快照記錄的檔名。這個檔名若已被同時進行的 metadata 任務暫存
// （stagedEpisodes 有登記），不能蓋掉它：那個任務 commit 的 sha 與長度要跟檔案內容一致（Codex P1）；
// 回傳可以重試的錯誤，等那個任務 commit 或丟棄之後再試。檢查與 rename 在同一把鎖之內，
// 之後才開始暫存的 metadata 任務會因為 O_EXCL 發現檔案已存在而改用別的檔名。
// 目的檔已存在時一律不蓋掉，沿用現有的檔案。
func recoverEpisodeFile(part, dir, name string) error {
	stagedEpisodes.Lock()
	defer stagedEpisodes.Unlock()
	if _, inFlight := stagedEpisodes.paths[stagedKey(dir, name)]; inFlight {
		return &errclass.Error{Class: errclass.ClassRetryable, Source: "podcast", Message: "音檔正由另一個 metadata 任務更新，稍後重試。"}
	}
	dst := filepath.Join(dir, name)
	// 檔案現在已經存在（例如下載期間另一個 metadata 任務 commit 了同名的新檔，登記已經放掉）：
	// 不能蓋掉它，沿用現有的檔案，丟掉這次下載（Codex P1）。
	if _, err := os.Lstat(dst); err == nil {
		_ = os.Remove(part)
		return nil
	}
	return os.Rename(part, dst)
}

// stageEpisodeFile 把下載完成的 part 放到 dir，而且不蓋掉已存在的檔案：依序用 O_EXCL 保留 episode<ext>、
// episode-<sha8><ext>，都已存在時改用隨機的 episode-<8 碼><ext>（已存在就失敗），再 rename 蓋過自己剛建立的空檔。
// 不共用別的下載已經建立的檔案：那個檔案仍屬於另一個還沒 commit 的執行，它的結果被丟棄時會刪掉它。
// O_EXCL 在任何檔案系統上都有效，不依賴 hard link。回傳的檔案一定是這次建立的。
// 保留與 rename 之間若程序崩潰會留下空檔，之後由 afterCommit 的清理刪除（程序重啟後 stagedEpisodes 是空的）。
// 建立好的檔案登記在 stagedEpisodes，直到它的結果 commit（releaseStagedEpisode）或被丟棄（discardStagedEpisode）。
func stageEpisodeFile(part, dir, ext, sum string) (string, error) {
	candidates := []string{"episode" + ext, "episode-" + sum[:8] + ext}
	for range 8 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		candidates = append(candidates, "episode-"+hex.EncodeToString(b[:])+ext)
	}
	// 建立與登記在同一把鎖之內：清理（removeStaleEpisodes）不會在兩者之間看到一個還沒登記的新檔。
	stagedEpisodes.Lock()
	defer stagedEpisodes.Unlock()
	for _, n := range candidates {
		dst := filepath.Join(dir, n)
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		switch {
		case err == nil:
			if err := f.Close(); err != nil {
				_ = os.Remove(dst)
				return "", err
			}
			if err := os.Rename(part, dst); err != nil {
				_ = os.Remove(dst)
				return "", err
			}
			stagedEpisodes.paths[stagedKey(dir, n)] = struct{}{}
			return n, nil
		case errors.Is(err, fs.ErrExist):
			continue
		default:
			return "", err
		}
	}
	return "", errors.New("暫存音檔失敗：找不到可用的檔名")
}

// stalePartAge 是 .part 檔多久沒有寫入就視為崩潰遺留的殘檔。下載中的檔案持續在寫入；
// 設一段寬限，避免誤刪同時進行中的另一次下載。
const stalePartAge = 10 * time.Minute

// removeStaleParts 刪除程序崩潰遺留的 .episode-*.part：修改時間早於 stalePartAge 的才刪。
func removeStaleParts(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, ".episode-") || !strings.HasSuffix(name, ".part") {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > stalePartAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// stagedEpisodes 記錄這個程序裡已經暫存、結果還沒 commit 也還沒被丟棄的音檔（絕對路徑）。
// 清理不能刪它們：檔名一旦被釋放，之後的下載可能又保留到同一個名稱，而原本那個執行稍後的 afterDiscard
// 仍會來刪這個名稱，結果刪到別人的檔案（Codex P1）。只有檔案自己的執行（commit 或丟棄）才能把它從這裡拿掉。
// 程序重啟後這份記錄是空的，崩潰遺留的檔案就由清理刪除。
var stagedEpisodes = struct {
	sync.Mutex
	paths map[string]struct{}
}{paths: map[string]struct{}{}}

// stagedKey 是 stagedEpisodes 的鍵：媒體目錄的絕對路徑加檔名。
func stagedKey(dir, name string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, name)
}

// releaseStagedEpisode 在結果 commit 之後把檔案從 stagedEpisodes 拿掉：它從此是快照指向的檔案，不再是暫存檔。
func releaseStagedEpisode(dir, name string) {
	stagedEpisodes.Lock()
	defer stagedEpisodes.Unlock()
	delete(stagedEpisodes.paths, stagedKey(dir, name))
}

// discardStagedEpisode 在結果沒有寫入時刪除這次暫存的檔案並解除登記。刪除與解除登記在同一把鎖之內，
// 檔名要等檔案真的不在了才會被別的下載保留。
func discardStagedEpisode(dir, name string) {
	stagedEpisodes.Lock()
	defer stagedEpisodes.Unlock()
	_ = os.Remove(filepath.Join(dir, name))
	delete(stagedEpisodes.paths, stagedKey(dir, name))
}

// removeStaleEpisodes 刪除先前下載留下的其他 episode.* 檔（副檔名可能不同），keep 中的檔名保留，
// 其他執行已暫存、還沒 commit 的檔案（stagedEpisodes）也保留。
func removeStaleEpisodes(dir string, keep ...string) {
	stagedEpisodes.Lock()
	defer stagedEpisodes.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if slices.Contains(keep, e.Name()) || !episodeFilePattern.MatchString(e.Name()) {
			continue
		}
		if _, inFlight := stagedEpisodes.paths[stagedKey(dir, e.Name())]; inFlight {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
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
