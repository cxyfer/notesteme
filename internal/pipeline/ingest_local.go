package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是本機影音的新增入口（ING-05）：inbox 自動新增、用路徑新增、上傳。
// 三者共用 ingestLocal：差別只在去重、tombstone 與識別鍵的規則（見 LocalTarget.Origin）。

// MaxUploadNameBytes 是上傳檔名的長度上限（位元組）。
const MaxUploadNameBytes = 255

// LocalTarget 是要新增的本機檔案。
type LocalTarget struct {
	// Key 是 external_id：inbox 檔是相對 inbox 的路徑（`/` 分隔），其他是解析後的絕對路徑；
	// 上傳檔是清理過的檔名，撞名時 ingestLocal 在副檔名前加 -2、-3。
	Key string
	// SourcePath 寫進 items.source_path：inbox 與 media_roots 的檔案是絕對路徑，上傳檔是相對資料目錄的 uploads/<id>/<name>。
	SourcePath string
	// SHA256 是內容雜湊（小寫十六進位）；用路徑新增時還沒有，為空字串（之後由 metadata 步驟補上）。
	SHA256 string
	// Origin 是 store.OriginInbox、OriginManual 或 OriginUpload。
	// inbox 遇到 tombstone 不建立影音、也不移除 tombstone；手動與上傳先移除 tombstone（DEL-R11）。
	Origin string
	// Info 是 ffprobe 的結果；目前只供呼叫端記錄，新增時不使用（metadata 步驟會重新探測）。
	Info *media.Info
	// ID 預先指定影音 ID（上傳檔要先知道 ID 才能決定 uploads/<id>/）；空白時由 store 產生。
	ID string
	// Overrides、Until 是手動新增與上傳時的單次覆寫與「做到哪個步驟為止」（ING-R7）。
	Overrides map[string]any
	Until     string
}

// LocalOutcome 是 IngestLocal 的結果。State 是 store 的 Inbox* 常數；
// 沒有建立影音時（Created 為 false），Item 是既有的那一筆（tombstoned 時為零值）。
type LocalOutcome struct {
	State   string
	Item    store.Item
	Created bool
}

// IngestLocal 新增一個本機檔案並立刻建立 metadata 任務，優先順序為手動新增（ING-R12）。
//
// 一個交易內依序判斷：
//  1. 同鍵的影音已存在：在垃圾桶是 in_trash；雜湊相同（或有一邊還沒有雜湊）是 already；否則是 conflict。
//  2. inbox 才檢查 tombstone：成立是 tombstoned（不移除 tombstone，DEL-R11）。
//     手動與上傳在這裡改為移除 tombstone（依鍵；上傳另外依雜湊）。
//  3. 雜湊相同的影音已存在（含垃圾桶中的）：duplicate。
//  4. 建立影音與 metadata 任務；commit 之後發布 item.created（store 負責）並喚醒 worker。
//
// 上傳（OriginUpload）與其他不同：先看內容是否已存在（duplicate），再替撞名的檔名加 -2、-3。
func (p *Pipeline) IngestLocal(ctx context.Context, t LocalTarget) (LocalOutcome, error) {
	if t.Key == "" {
		return LocalOutcome{}, errors.New("pipeline: LocalTarget.Key 不可為空")
	}
	if t.Origin == "" {
		t.Origin = store.OriginManual
	}
	if t.Until != "" && !ValidStage(t.Until) {
		return LocalOutcome{}, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	global, err := p.globalConfig(ctx)
	if err != nil {
		return LocalOutcome{}, err
	}
	eff, err := BuildEffective(global, t.Overrides)
	if err != nil {
		return LocalOutcome{}, err
	}
	sha, cfgJSON, err := eff.Snapshot()
	if err != nil {
		return LocalOutcome{}, err
	}
	overrides := json.RawMessage(`{}`)
	if len(t.Overrides) > 0 {
		if overrides, err = json.Marshal(t.Overrides); err != nil {
			return LocalOutcome{}, err
		}
	}

	var out LocalOutcome
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		out = LocalOutcome{}
		upload := t.Origin == store.OriginUpload
		key := t.Key

		if !upload {
			switch it, err := r.ItemByKey(ctx, source.PlatformLocal, key, 0); {
			case err == nil:
				switch {
				case it.DeletedAt != nil:
					out = LocalOutcome{State: store.InboxInTrash, Item: it}
				case it.ContentSHA256 == "" || t.SHA256 == "" || it.ContentSHA256 == t.SHA256:
					out = LocalOutcome{State: store.InboxAlready, Item: it}
				default:
					out = LocalOutcome{State: store.InboxConflict, Item: it}
				}
				return nil
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}

		if t.Origin == store.OriginInbox {
			if blocked, err := r.LocalTombstoneBlocks(ctx, key, t.SHA256); err != nil {
				return err
			} else if blocked {
				out = LocalOutcome{State: store.InboxTombstoned}
				return nil
			}
		}

		switch dup, err := r.ItemByLocalSHA(ctx, t.SHA256); {
		case err == nil:
			out = LocalOutcome{State: store.InboxDuplicate, Item: dup}
			return nil
		case !errors.Is(err, store.ErrNotFound):
			return err
		}

		if upload {
			// 檔名撞名：加 -2、-3（插在副檔名前）；垃圾桶中的影音也佔用名稱。
			var err error
			if key, err = uniqueLocalKey(ctx, r, key); err != nil {
				return err
			}
		}
		if t.Origin != store.OriginInbox {
			// 手動新增與上傳視為「想要這個檔案」：移除永久刪除留下的 tombstone（DEL-R11）。
			if _, err := r.DeleteTombstones(ctx, source.PlatformLocal, key, 0); err != nil {
				return err
			}
			if upload {
				if _, err := r.DeleteLocalTombstonesBySHA(ctx, t.SHA256); err != nil {
					return err
				}
			}
		}

		it, created, err := r.InsertItemIfAbsent(ctx, store.NewItem{
			ID: t.ID, Platform: source.PlatformLocal, ExternalID: key, SourcePath: t.SourcePath, ContentSHA256: t.SHA256,
			Origin: t.Origin, Overrides: overrides, UntilStage: t.Until,
		})
		if err != nil {
			return err
		}
		if !created {
			// 同鍵的影音在這個交易中不會出現（上面已檢查，SQLite 單一寫入連線）；保險起見當成已存在。
			out = LocalOutcome{State: store.InboxAlready, Item: it}
			return nil
		}
		snap, err := r.EnsureSnapshot(ctx, sha, cfgJSON)
		if err != nil {
			return err
		}
		// 所有步驟一開始都是 pending；until 之後的步驟不建立任務（PIPE-R1）。
		var first store.StageRun
		for _, stage := range Stages {
			run, err := r.InsertStageRun(ctx, store.NewStageRun{
				ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: snap,
			})
			if err != nil {
				return err
			}
			if stage == StageMetadata {
				first = run
			}
		}
		if _, err := insertStageTask(ctx, r, it.ID, first, queue.PriorityManual, false); err != nil {
			return err
		}
		out = LocalOutcome{State: store.InboxIngested, Item: it, Created: true}
		return nil
	})
	if err != nil {
		return LocalOutcome{}, err
	}
	if out.Created {
		p.wake()
	}
	return out, nil
}

// uniqueLocalKey 回傳沒有被使用的鍵：name 可用就用它，否則在副檔名前加 -2、-3……
func uniqueLocalKey(ctx context.Context, r *store.Repo, name string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		key := name
		if n > 1 {
			key = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		switch _, err := r.ItemByKey(ctx, source.PlatformLocal, key, 0); {
		case errors.Is(err, store.ErrNotFound):
			return key, nil
		case err != nil:
			return "", err
		}
	}
}

// LocalOptions 是用路徑新增時的選項。
type LocalOptions struct {
	// Inbox 是 inbox 目錄（解析前後皆可，只用來決定識別鍵）；空白代表沒有 inbox，一律用絕對路徑。
	Inbox string
	// Profile、Overrides、Until 同 IngestRequest。
	Profile   string
	Overrides map[string]any
	Until     string
}

// LocalKey 回傳本機檔案的識別鍵：在 inbox 底下是相對 inbox 的路徑（`/` 分隔），否則是絕對路徑。
// path 與 inbox 都必須已經解析過（Guard.Resolve）。
func LocalKey(inbox, path string) string {
	if inbox != "" {
		if rel, err := filepath.Rel(inbox, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// IngestPath 以路徑新增本機檔案（手動，GRD-R7）。path 必須是已經解析過的絕對路徑（呼叫端已用 Guard.Resolve 檢查在 root 內）。
//
// 延遲驗證：不檢查檔案是否存在、是不是影音，也不計算雜湊，立刻建立 metadata 任務；
// 檔案不存在時 metadata 步驟以 permanent「找不到這個檔案。」失敗。
// 同鍵的影音已存在時不重新建立（已存在或在垃圾桶中，與 Ingest 相同）。
func (p *Pipeline) IngestPath(ctx context.Context, path string, opts LocalOptions) (IngestResult, error) {
	if !filepath.IsAbs(path) {
		return IngestResult{}, invalidf("路徑必須是絕對路徑。")
	}
	if opts.Profile != "" {
		// TODO(PRF)：處理方案屬於 M2，同 Ingest。
		return IngestResult{}, &NotImplementedError{What: "處理方案"}
	}
	out, err := p.IngestLocal(ctx, LocalTarget{
		Key: LocalKey(opts.Inbox, path), SourcePath: path, Origin: store.OriginManual,
		Overrides: opts.Overrides, Until: opts.Until,
	})
	if err != nil {
		return IngestResult{}, err
	}
	switch out.State {
	case store.InboxIngested:
		return IngestResult{Item: out.Item}, nil
	case store.InboxInTrash:
		return IngestResult{Item: out.Item, InTrash: true}, nil
	default:
		return IngestResult{Item: out.Item, AlreadyInLibrary: true}, nil
	}
}

// UploadRequest 是上傳的檔案。檔案已經完整寫入 TmpPath（資料目錄的 tmp 底下）。
type UploadRequest struct {
	// Name 是清理過的檔名（見 ValidUploadName）。
	Name string
	// TmpPath 是暫存檔的絕對路徑；成功建立影音時被搬走，其他情況由呼叫端清理。
	TmpPath string
	// SHA256 是內容雜湊（小寫十六進位）。
	SHA256 string
	// Overrides、Until 同 IngestRequest。
	Overrides map[string]any
	Until     string
}

// errNoMedia 表示沒有設定 ffprobe 或資料目錄，無法處理上傳。
var errNoMedia = errors.New("pipeline: 沒有設定媒體工具")

// ValidUploadName 檢查上傳的檔名：非空、不是 . 或 ..、沒有控制字元、沒有 / 與 \，長度不超過 255 位元組。
func ValidUploadName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > MaxUploadNameBytes {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// IngestUpload 新增上傳的檔案（origin upload）：ffprobe → 搬到 uploads/<id>/<name> → 在交易中建立影音。
//
// 結果：新建立（Created）、相同內容已存在（State duplicate，Item 可能在垃圾桶中，由呼叫端決定 200 或 409）。
// 不是影音時回傳包裝了 media.ErrNotMedia 的錯誤；ffprobe 無法執行是 *UpstreamError。
// 沒有新建立時，暫存檔與 uploads/<id> 都不會留下；暫存檔本身仍由呼叫端清理。
func (p *Pipeline) IngestUpload(ctx context.Context, req UploadRequest) (LocalOutcome, error) {
	if !ValidUploadName(req.Name) {
		return LocalOutcome{}, invalidf("檔名不正確。")
	}
	if p.media == nil || p.dataDir == "" {
		return LocalOutcome{}, &UpstreamError{Msg: "無法執行 ffprobe，請確認已安裝（可執行 notesteme doctor）。", Err: errNoMedia}
	}
	info, err := p.media.Probe(ctx, req.TmpPath)
	if err != nil {
		if errors.Is(err, media.ErrNotMedia) || ctx.Err() != nil {
			return LocalOutcome{}, err
		}
		return LocalOutcome{}, &UpstreamError{Msg: upstreamProbeMsg(err), Err: err}
	}

	// 先搬到最終位置再建立影音（ffprobe → rename → 交易）；沒有建立影音時再搬回來。
	id := store.NewID()
	rel := filepath.Join("uploads", id, req.Name)
	dir := filepath.Join(p.dataDir, "uploads", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return LocalOutcome{}, err
	}
	if err := os.Rename(req.TmpPath, filepath.Join(dir, req.Name)); err != nil {
		_ = os.RemoveAll(dir)
		return LocalOutcome{}, err
	}
	out, err := p.IngestLocal(ctx, LocalTarget{
		ID: id, Key: req.Name, SourcePath: filepath.ToSlash(rel), SHA256: req.SHA256, Origin: store.OriginUpload, Info: info,
		Overrides: req.Overrides, Until: req.Until,
	})
	if err != nil || !out.Created {
		// 沒有建立影音：搬走的檔案要還回去，呼叫端才能統一清理暫存檔。
		_ = os.Rename(filepath.Join(dir, req.Name), req.TmpPath)
		_ = os.RemoveAll(dir)
		return out, err
	}
	return out, nil
}

// upstreamProbeMsg 是 ffprobe 無法執行時給使用者看的訊息。
func upstreamProbeMsg(err error) string {
	var ce *errclass.Error
	if errors.As(err, &ce) && ce.Message != "" {
		return ce.Message
	}
	return "ffprobe 讀取檔案失敗，請稍後再試。"
}
