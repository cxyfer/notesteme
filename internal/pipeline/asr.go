package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/audio"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// maxExtraChunks 是切出的塊仍超過上限時，最多比原本多切幾塊（SRC-R12）。
const maxExtraChunks = 3

// asrPreprocess 是語音轉錄前處理的參數（ADR-0016），進快取鍵與來源紀錄：改動後舊的快取就不再命中。
// 編碼參數本身在 internal/media（argv 固定），這裡記錄會影響結果的值。
func asrPreprocess(maxFileBytes int64) map[string]any {
	return map[string]any{
		"codec": "opus", "bitrate_kbps": 32, "channels": 1, "sample_rate": 16000,
		"max_file_bytes": maxFileBytes, "chunk_safety": audio.Safety, "chunk_overlap_s": audio.Overlap, "cut_window_s": audio.CutWindow,
	}
}

// audioSource 取得 ASR 要轉錄的原始音訊（ADR-0016）：YouTube 用 yt-dlp 下載，本機影音直接用原檔，
// Podcast 用 metadata 下載的音檔（不在了就重新下載）。
type audioSource interface {
	// fetch 回傳音訊檔的絕對路徑，以及取得時用到的外部工具版本（寫進來源紀錄）。
	// dir 是這次轉錄專用的暫存目錄，由呼叫端清除。
	fetch(ctx context.Context, it store.Item, dir string) (path string, versions map[string]string, err error)
}

// audioSourceFor 回傳平台的音訊來源；平台還不支援（或依賴沒有設定）時為 nil。
func audioSourceFor(d Deps, platform string) audioSource {
	switch {
	case platform == source.PlatformYouTube && d.YouTube != nil:
		return youtubeAudio{d.YouTube}
	case platform == source.PlatformLocal && d.Files != nil:
		return localAudio{localAdapter{files: d.Files, media: d.Media, dataDir: d.DataDir}}
	case platform == source.PlatformPodcast && d.HTTP != nil:
		return podcastAudioSource{audioDownloader{http: d.HTTP, media: d.Media, dataDir: d.DataDir, version: d.Version}}
	}
	return nil
}

// youtubeAudio 以 yt-dlp 下載影片的音訊。
type youtubeAudio struct{ yt YouTube }

func (a youtubeAudio) fetch(ctx context.Context, it store.Item, dir string) (string, map[string]string, error) {
	// 版本要在轉錄之前取得：轉錄要付費，轉錄成功之後不能再有會失敗的步驟（失敗重試會重複付費）。
	version, err := a.yt.Version(ctx)
	if err != nil {
		return "", nil, err
	}
	path, err := a.yt.DownloadAudio(ctx, it.ExternalID, dir)
	if err != nil {
		return "", nil, &downloadError{err}
	}
	return path, map[string]string{"yt-dlp": version}, nil
}

// localAudio 直接使用本機影音檔（經 Guard 檢查與解析）。
type localAudio struct{ a localAdapter }

func (l localAudio) fetch(_ context.Context, it store.Item, _ string) (string, map[string]string, error) {
	f, abs, err := l.a.open(it.SourcePath, it.ExternalID)
	if err != nil {
		return "", nil, err
	}
	_ = f.Close()
	return abs, nil, nil
}

// transcriber 是 transcribeASR 用到的轉錄服務（Groq 實作它）。
type transcriber interface {
	Transcribe(ctx context.Context, audioPath, langHint string) (*groq.Result, error)
	// MaxFileBytes 是單一音檔的大小上限。
	MaxFileBytes() int64
}

// errChunkTooLarge 表示切出的塊仍超過單檔上限，要多切一塊重新規劃。
var errChunkTooLarge = errors.New("切出的音訊塊超過單檔上限")

// transcribeASR 是共用的語音轉錄流程（SRC-R12、ADR-0016）：
//  1. 把 in 前處理成單聲道 16 kHz 的 Opus（dir/prepared.ogg）。
//  2. 不超過 tr 的單檔上限就上傳一次。
//  3. 超過就切塊：以上限的 90% 估算塊數，在每個切點附近找靜音，每塊前後各多取 5 秒，
//     從原始檔重新編碼；有任一塊仍超過上限就多切一塊重新規劃（最多多切 maxExtraChunks 塊）。
//  4. 依序轉錄各塊（語言提示用 langHint，不明時用第一個回報語言的塊），再接回單一時間軸（audio.Stitch）。
//
// in 與 dir 必須是絕對路徑；dir 的內容由呼叫端清除。langHint 為空代表讓 Whisper 自行偵測。
// 切塊時不做語言不符的重試：整份轉錄結果的語言由呼叫端檢查。
func transcribeASR(ctx context.Context, m *media.Tool, tr transcriber, in, dir, langHint string) (*groq.Result, error) {
	if m == nil {
		return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "ffmpeg", Message: msgNoFFmpeg, Actions: []string{"安裝 ffmpeg"}}
	}
	prepared := filepath.Join(dir, "prepared.ogg")
	if err := m.PrepareASR(ctx, in, prepared); err != nil {
		return nil, err
	}
	fi, err := os.Stat(prepared)
	if err != nil {
		return nil, err
	}
	limit := tr.MaxFileBytes()
	if fi.Size() <= limit {
		return tr.Transcribe(ctx, prepared, langHint)
	}
	return transcribeChunks(ctx, m, tr, in, prepared, fi.Size(), limit, langHint)
}

// transcribeChunks 是 transcribeASR 的切塊路徑；size 是前處理後的大小，已確認超過 limit。
func transcribeChunks(ctx context.Context, m *media.Tool, tr transcriber, in, prepared string, size, limit int64, langHint string) (*groq.Result, error) {
	info, err := m.Probe(ctx, prepared)
	if err != nil {
		return nil, err
	}
	d := info.DurationS
	if d <= 0 {
		return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "ffprobe", Message: "無法取得音訊長度，無法切塊。"}
	}

	n := max(2, audio.ChunkCount(size, limit, audio.Safety))
	var (
		chunks []audio.Chunk
		files  []string
	)
	for extra := 0; ; extra++ {
		chunks, files, err = planChunks(ctx, m, in, prepared, d, n+extra, limit)
		if err == nil {
			break
		}
		if !errors.Is(err, errChunkTooLarge) {
			return nil, err
		}
		if extra >= maxExtraChunks {
			return nil, &errclass.Error{
				Class: errclass.ClassPermanent, Source: "ffmpeg", Message: "音檔切塊後仍超過語音轉錄服務的大小上限。",
				Detail: fmt.Sprintf("切成 %d 塊後仍有塊超過 %d bytes", n+extra, limit), Err: err,
			}
		}
	}

	parts := make([][]subtitle.Cue, len(chunks))
	var lang string
	hint := langHint
	for i, f := range files {
		res, err := tr.Transcribe(ctx, f, hint)
		_ = os.Remove(f) // 傳完就刪，減少暫存用量
		switch {
		case err == nil:
			parts[i] = res.Cues
			if lang == "" && res.Lang != "" {
				lang = res.Lang
				if hint == "" {
					hint = groq.LangHint(res.Lang)
				}
			}
		case errors.Is(err, errclass.ErrNoResult):
			// 這一塊沒有語音（例如靜音）；其他塊仍可能有。
		default:
			return nil, err
		}
	}
	cues := audio.Stitch(chunks, parts)
	if len(cues) == 0 {
		return nil, groq.ErrNoTranscript
	}
	return &groq.Result{Lang: lang, Cues: cues, DurationS: d}, nil
}

// planChunks 規劃成 n 塊並切出檔案（prepared 同目錄下的 chunk-<i>.ogg）：
// 在每個切點（總長的 k/n）附近找靜音，再從原始檔 in 重新編碼各塊。有任一塊超過 limit 時
// 刪除已切出的檔案並回傳 errChunkTooLarge。
func planChunks(ctx context.Context, m *media.Tool, in, prepared string, d float64, n int, limit int64) ([]audio.Chunk, []string, error) {
	cuts := make([]float64, 0, n-1)
	for k := 1; k < n; k++ {
		target := d * float64(k) / float64(n)
		from := max(0, target-audio.CutWindow/2)
		sil, err := m.Silences(ctx, prepared, from, min(audio.CutWindow, d-from))
		if err != nil {
			return nil, nil, err
		}
		cuts = append(cuts, audio.PickCut(target, sil, audio.CutWindow))
	}
	chunks := audio.Chunks(d, cuts, audio.Overlap)
	dir := filepath.Dir(prepared)
	var files []string
	cleanup := func() {
		for _, f := range files {
			_ = os.Remove(f)
		}
	}
	for i, c := range chunks {
		out := filepath.Join(dir, fmt.Sprintf("chunk-%d.ogg", i))
		files = append(files, out)
		if err := m.ExtractASRChunk(ctx, in, out, c.StartS, c.EndS-c.StartS); err != nil {
			cleanup()
			return nil, nil, err
		}
		fi, err := os.Stat(out)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if fi.Size() > limit {
			cleanup()
			return nil, nil, errChunkTooLarge
		}
	}
	return chunks, files, nil
}
