package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"

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

// MediaETag 回傳媒體檔的 ETag（含引號）；不能確定雜湊對應目前的內容時回傳空字串，
// 由 http.ServeContent 改用 Last-Modified 判斷條件請求。
// 上傳的副本不可變，有雜湊就用。用路徑加入的檔案可能在同一路徑被替換，而雜湊只在探測時更新：
// 只有目前的 size 與 mtime 和記錄雜湊時的相同才用雜湊，否則舊的 If-None-Match 會得到 304，
// If-Range 也會把不同版本的內容拼在一起。
func MediaETag(it store.Item, fi os.FileInfo) string {
	if it.ContentSHA256 == "" {
		return ""
	}
	if it.Origin != store.OriginUpload {
		rec := recordedLocalStat(it.Metadata)
		if rec == nil || fi == nil || *rec != (localStat{Size: fi.Size(), MtimeNS: fi.ModTime().UnixNano()}) {
			return ""
		}
	}
	return `"` + it.ContentSHA256 + `"`
}

// MediaFile 開啟影音的媒體檔，供 API 以 http.ServeContent 串流。呼叫端必須關閉回傳的檔案。
//
// 錯誤：store.ErrNotFound（沒有這部影音）、*InTrashError（在垃圾桶中，DEL-R1）、ErrNoMedia、
// ErrMediaMissing，以及檔案在 root 之外的 pathguard.ErrOutsideRoots（API 回 403）。
// 非本機影音：M1 還沒有媒體快取（<data>/media 只會在之後的切片寫入），一律是 ErrNoMedia。
func (p *Pipeline) MediaFile(ctx context.Context, id string) (*os.File, os.FileInfo, store.Item, error) {
	it, err := p.st.Read().ItemByID(ctx, id)
	if err != nil {
		return nil, nil, store.Item{}, err
	}
	if it.DeletedAt != nil {
		return nil, nil, store.Item{}, &InTrashError{ItemID: it.ID}
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
