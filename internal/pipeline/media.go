package pipeline

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"syscall"

	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 媒體端點（LIB 規格的本機媒體串流）用到的錯誤；API 對應成 404 的 code。
var (
	// ErrNoMedia 表示這部影音沒有可以串流的媒體：不是本機影音，也沒有快取的媒體。
	ErrNoMedia = errors.New("這部影音沒有可以播放的媒體。")
	// ErrMediaMissing 表示本機檔案已經不在原處（被移動或刪除）。
	ErrMediaMissing = errors.New("找不到這部影音的檔案，可能已被移動或刪除。")
)

// MediaFile 開啟影音的媒體檔，供 API 以 http.ServeContent 串流。呼叫端必須關閉回傳的檔案。
//
// 錯誤：store.ErrNotFound（沒有這部影音）、*InTrashError（在垃圾桶中，DEL-R1）、ErrNoMedia、
// ErrMediaMissing，以及檔案在 root 之外的 pathguard.ErrOutsideRoots（API 回 403）。
// Podcast 單集：串流 metadata 步驟下載的音檔（<data>/media/<id>/episode.<ext>）；其他非本機影音沒有媒體快取，一律是 ErrNoMedia。
func (p *Pipeline) MediaFile(ctx context.Context, id string) (*os.File, os.FileInfo, store.Item, error) {
	it, err := p.st.Read().ItemByID(ctx, id)
	if err != nil {
		return nil, nil, store.Item{}, err
	}
	if it.DeletedAt != nil {
		return nil, nil, store.Item{}, &InTrashError{ItemID: it.ID}
	}
	if it.Platform == source.PlatformPodcast {
		return p.podcastMediaFile(it)
	}
	if it.Platform != source.PlatformLocal || it.SourcePath == "" || p.files == nil {
		return nil, nil, it, ErrNoMedia
	}
	path := it.SourcePath
	if !filepath.IsAbs(path) {
		// 上傳的檔案存成相對資料目錄的路徑。
		path = filepath.Join(p.dataDir, filepath.FromSlash(path))
	}
	f, fi, err := p.files.Open(path)
	switch {
	case err == nil:
		return f, fi, it, nil
	case errors.Is(err, os.ErrNotExist), errors.Is(err, pathguard.ErrNotRegular):
		return nil, nil, it, ErrMediaMissing
	}
	return nil, nil, it, err
}

// msgPodcastNoAudio 是 Podcast 單集還沒有下載音檔（metadata 步驟還沒完成）時的訊息。
const msgPodcastNoAudio = "這一集的音檔還沒下載。"

// noAudioError 是 ErrNoMedia，但訊息是這一集特有的。
type noAudioError struct{}

func (noAudioError) Error() string        { return msgPodcastNoAudio }
func (noAudioError) Is(target error) bool { return target == ErrNoMedia }

// podcastMediaFile 開啟 Podcast 單集下載的音檔。檔名取自 metadata 快照，而且必須是 episode.<副檔名>，
// 只在 <data>/media/<id>/ 之內以 os.OpenRoot 開啟，確認是一般檔案。
// 回傳的影音複本把 SourcePath 設成媒體目錄內的相對路徑，API 用它的檔名決定 Content-Type。
func (p *Pipeline) podcastMediaFile(it store.Item) (*os.File, os.FileInfo, store.Item, error) {
	m, ok := readPodcastMeta(it.Metadata)
	if !ok {
		return nil, nil, it, noAudioError{}
	}
	if !episodeFilePattern.MatchString(m.Audio.File) {
		return nil, nil, it, ErrMediaMissing
	}
	it.SourcePath = path.Join("media", it.ID, m.Audio.File)
	dir, err := filepath.Abs(MediaCacheDir(p.dataDir, it.ID))
	if err != nil {
		return nil, nil, it, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, it, ErrMediaMissing
		}
		return nil, nil, it, err
	}
	defer root.Close()
	f, err := root.OpenFile(m.Audio.File, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, it, ErrMediaMissing
		}
		return nil, nil, it, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, it, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, it, ErrMediaMissing
	}
	return f, fi, it, nil
}
