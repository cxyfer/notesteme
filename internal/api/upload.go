package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 上傳被拒絕的錯誤代碼。
const (
	// CodeUploadTooLarge：檔案超過 library.upload_max_bytes（413）。
	CodeUploadTooLarge = "upload_too_large"
	// CodeUnsupportedMedia：檔案沒有音訊或影片（422，ING-R11）。
	CodeUnsupportedMedia = "unsupported_media"
	// CodeTooManyUploads：同時進行的上傳已達上限（429）。
	CodeTooManyUploads = "too_many_uploads"
)

const (
	// uploadIdleTimeout 是上傳的閒置逾時：每次讀取本體前重設，超過這段時間沒有收到資料就中斷。
	uploadIdleTimeout = 2 * time.Minute
	// maxConcurrentUploads 是同時進行的上傳上限（同一個資料目錄，TCP 與本機 socket 共用）。
	maxConcurrentUploads = 2
	// maxUploadParts 是一個上傳請求的 multipart part 數上限。
	maxUploadParts = 16
)

// uploadSlots 依資料目錄記錄並行上傳的名額：同一個 process 的各個 handler（TCP、本機 socket）共用。
var uploadSlots sync.Map // dataDir -> chan struct{}

// acquireUploadSlot 嘗試佔用一個名額；沒有名額時回傳 nil。
func acquireUploadSlot(dataDir string) (release func()) {
	v, _ := uploadSlots.LoadOrStore(dataDir, make(chan struct{}, maxConcurrentUploads))
	slots := v.(chan struct{})
	select {
	case slots <- struct{}{}:
		return func() { <-slots }
	default:
		return nil
	}
}

// idleReader 在每次讀取本體前把讀取期限設為「現在 + 閒置逾時」：
// 只要資料持續進來就不會逾時，卡住超過 uploadIdleTimeout 才中斷。
// 讀取期限是 net 層以實際時間比對的絕對時間，所以這裡用 time.Now 而不是注入的 clock（fake clock 會讓期限立刻到期）。
type idleReader struct {
	io.ReadCloser
	rc *http.ResponseController
}

func (i idleReader) Read(p []byte) (int, error) {
	// 不支援時（例如測試用的 ResponseRecorder）忽略。
	_ = i.rc.SetReadDeadline(time.Now().Add(uploadIdleTimeout))
	return i.ReadCloser.Read(p)
}

const (
	// uploadFormOverhead 是 multipart 本體在檔案之外的餘裕（欄位、邊界、標頭）。
	uploadFormOverhead = 1 << 20
	// maxOverridesBytes 是 overrides 欄位的上限；其他文字欄位（until、profile）的上限是 maxFieldBytes。
	maxOverridesBytes = 64 << 10
	maxFieldBytes     = 4 << 10
)

// uploadIntercept 在進入 Huma 之前處理 multipart 的 POST /api/items（上傳影音檔，ING-05）。
// 它掛在驗證 middleware 的內側（CSRF 與身分已檢查），與 cookieBodyLimit 同層：
// 只攔 Content-Type 是 multipart/form-data 的請求，其他（JSON 新增）原樣交給 next。
//
// 順序：scope → Content-Length（超過上限不讀本體）→ 並行名額（最多 2 個，否則 429）→
// MaxBytesReader 與閒置逾時（每次讀取前重設 2 分鐘的讀取期限，讀完後清除）→
// 串流寫到 <data>/tmp 並同時計算 sha256 → ffprobe → 搬到 uploads/<id>/<name> → 交易中建立（IngestUpload）。
// 所有路徑都會清掉暫存檔。
func uploadIntercept(d Deps) func(http.Handler) http.Handler {
	limit := d.UploadMaxBytes
	if limit <= 0 {
		limit = config.DefaultUploadMaxBytes
	}
	rt := &routes{d: d}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/items" || !isMultipart(r) {
				next.ServeHTTP(w, r)
				return
			}
			rt.upload(w, r, limit)
		})
	}
}

func isMultipart(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "multipart/form-data"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// formatLimit 把位元組數寫成給人看的大小（整除 GiB、MiB 時用該單位）。
func formatLimit(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

func uploadTooLarge(limit int64) *Error {
	return newError(http.StatusRequestEntityTooLarge, CodeUploadTooLarge,
		"上傳的檔案超過大小上限（"+formatLimit(limit)+"）。")
}

func (rt *routes) upload(w http.ResponseWriter, r *http.Request, limit int64) {
	ctx := r.Context()
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		writeError(w, asError(err))
		return
	}
	if rt.d.Pipeline == nil || rt.d.DataDir == "" {
		writeError(w, asError(rt.noPipeline(ctx)))
		return
	}
	// 宣告的長度已經超過上限：直接拒絕，不讀取本體。
	if r.ContentLength > limit+uploadFormOverhead {
		writeError(w, uploadTooLarge(limit))
		return
	}
	// 同時進行的上傳有上限：沒有名額時直接 429，不讀本體。
	release := acquireUploadSlot(rt.d.DataDir)
	if release == nil {
		writeError(w, newError(http.StatusTooManyRequests, CodeTooManyUploads, "目前有其他上傳正在進行，請稍後再試。"))
		return
	}
	defer release()
	// server 的 ReadTimeout 是整個請求的期限，大檔案上傳會被截斷；改成閒置逾時：每次讀取前重設期限。
	rc := http.NewResponseController(w)
	r.Body = http.MaxBytesReader(w, idleReader{ReadCloser: r.Body, rc: rc}, limit+uploadFormOverhead)

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg))
		return
	}
	tmp := filepath.Join(rt.d.DataDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		rt.d.Log.ErrorContext(ctx, "建立上傳暫存目錄失敗", "error", err)
		writeError(w, asError(nil))
		return
	}
	dir, err := os.MkdirTemp(tmp, "upload-*")
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "建立上傳暫存目錄失敗", "error", err)
		writeError(w, asError(nil))
		return
	}
	defer os.RemoveAll(dir)

	up, apiErr := rt.readUpload(mr, dir, limit)
	// 本體讀完（或失敗）：清除期限，之後的 ffprobe 與入庫可能很久，不能讓連線的背景讀取逾時而取消 request。
	_ = rc.SetReadDeadline(time.Time{})
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	// 上傳的鍵與 inbox 檔案共用命名空間：交給 pipeline 避開 inbox 頂層的同名項目。
	// inbox 解析失敗就當成沒有 inbox（與路徑新增一致）。
	if rt.d.Inbox != "" && rt.d.Paths != nil {
		if inbox, err := rt.d.Paths.Resolve(rt.d.Inbox); err == nil {
			up.Inbox = inbox
		}
	}
	out, err := rt.d.Pipeline.IngestUpload(ctx, up)
	if err != nil {
		if errors.Is(err, media.ErrNotMedia) {
			writeError(w, newError(http.StatusUnprocessableEntity, CodeUnsupportedMedia, "這個檔案沒有音訊或影片，無法新增。"))
			return
		}
		writeError(w, asError(rt.mapErr(ctx, err)))
		return
	}
	if out.Item.DeletedAt != nil {
		// 相同內容的影音在垃圾桶中：不重新建立，動作「還原」（DEL-R10）。
		writeError(w, inTrashError(http.StatusConflict, out.Item.ID))
		return
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	var body struct {
		Items []IngestedItem `json:"items"`
	}
	body.Items = []IngestedItem{{Item: toItem(out.Item), AlreadyInLibrary: !out.Created}}
	writeJSON(w, status, body)
}

// readUpload 串流讀出 multipart：file 必填且只能一個，until、profile、overrides 選填，其他欄位 422。
// 檔案邊寫入 dir 邊計算 sha256；超過 limit 回 413。回傳的 UploadRequest.TmpPath 在 dir 底下。
func (rt *routes) readUpload(mr *multipart.Reader, dir string, limit int64) (pipeline.UploadRequest, *Error) {
	var (
		up       pipeline.UploadRequest
		gotFile  bool
		profile  string
		overRaw  []byte
		haveOver bool
	)
	invalid := func(msg string) *Error { return newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg) }
	for parts := 0; ; {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return up, readError(err, limit)
		}
		if parts++; parts > maxUploadParts {
			return up, invalid(fmt.Sprintf("欄位太多（上限 %d 個）。", maxUploadParts))
		}
		switch part.FormName() {
		case "file":
			if gotFile {
				return up, invalid("一次只能上傳一個檔案（file）。")
			}
			gotFile = true
			name := part.FileName()
			if !pipeline.ValidUploadName(name) {
				return up, invalid("檔名不正確。")
			}
			sum, tmpPath, aerr := rt.saveUpload(part, dir, name, limit)
			if aerr != nil {
				return up, aerr
			}
			up.Name, up.TmpPath, up.SHA256 = name, tmpPath, sum
		case "until", "profile":
			b, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
			if err != nil {
				return up, readError(err, limit)
			}
			if len(b) > maxFieldBytes {
				return up, invalid(part.FormName() + " 太長。")
			}
			if part.FormName() == "until" {
				// 讀到就驗證：不要等整個檔案傳完（file 在 until 之後時，也在 ffprobe 之前就拒絕）。
				up.Until = strings.TrimSpace(string(b))
				if up.Until != "" && !pipeline.ValidStage(up.Until) {
					return up, invalid(fmt.Sprintf("until 必須是 %v 其中之一。", pipeline.Stages))
				}
			} else {
				profile = strings.TrimSpace(string(b))
			}
		case "overrides":
			b, err := io.ReadAll(io.LimitReader(part, maxOverridesBytes+1))
			if err != nil {
				return up, readError(err, limit)
			}
			if len(b) > maxOverridesBytes {
				return up, invalid("overrides 太長（上限 64 KiB）。")
			}
			overRaw, haveOver = b, true
		default:
			return up, invalid("不支援的欄位：" + part.FormName() + "。")
		}
	}
	if !gotFile {
		return up, invalid("請提供要上傳的檔案（欄位 file）。")
	}
	if haveOver && len(strings.TrimSpace(string(overRaw))) > 0 {
		if err := json.Unmarshal(overRaw, &up.Overrides); err != nil {
			return up, invalid("overrides 必須是 JSON 物件。")
		}
	}
	// 處理方案由 pipeline 驗證並解析（名稱不存在回錯誤），同 JSON 新增。
	up.Profile = profile
	return up, nil
}

// saveUpload 把檔案內容寫到 dir，同時計算 sha256；回傳雜湊與暫存檔路徑。
// 暫存檔保留原本的副檔名：ffprobe 對部分格式要靠副檔名辨識。
func (rt *routes) saveUpload(part io.Reader, dir, name string, limit int64) (sum, path string, apiErr *Error) {
	path = filepath.Join(dir, "upload"+safeExt(name))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		rt.d.Log.Error("建立上傳暫存檔失敗", "error", err)
		return "", "", asError(nil)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, limit+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", readError(err, limit)
	}
	if n > limit {
		return "", "", uploadTooLarge(limit)
	}
	return hex.EncodeToString(h.Sum(nil)), path, nil
}

// readError 把讀取本體的錯誤對應到回應：超過大小上限是 413，其他（格式錯誤、連線中斷）是 400；
// 寫入暫存檔失敗（磁碟滿）是 500。
func readError(err error, limit int64) *Error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return uploadTooLarge(limit)
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return asError(nil)
	}
	return newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg)
}

// safeExt 回傳檔名的副檔名；只接受英數字，其他一律不保留。
func safeExt(name string) string {
	ext := filepath.Ext(name)
	if len(ext) < 2 || len(ext) > 12 {
		return ""
	}
	for _, r := range ext[1:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return ext
}
