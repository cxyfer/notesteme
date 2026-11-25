package api

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/pathguard"
)

// 匯入檔（字幕與 ASS 樣式）的來源：伺服器上的檔案路徑，或直接帶內容（content）。
// 不可信的輸入：大小有上限，只讀一般檔案，錯誤訊息不回顯檔案內容。
//
// 讀取伺服器上的檔案（path）刻意不套用 GRD-R7 的 inbox 與 media_roots 限制：GRD-R7 規範的是新增（ingest）媒體，
// 這裡的檔案只會被解析成字幕或樣式。代價是持有 ingest 或 admin scope 的 token 可以讓伺服器讀任何路徑的檔案，
// 所以只接受字幕副檔名的一般檔案（FIFO 與裝置不讀），解析失敗的回應也不含檔案內容。待 owner 決定是否改成套用 GRD-R7
// （見 OUT 規格的待決問題）。CLI 一律送 content，檔案由 client 自己讀。
func readImportInput(path, content string, maxBytes int, exts []string) ([]byte, string, *Error) {
	switch {
	case path != "" && content != "":
		return nil, "", invalidInput("path 與 content 只能擇一。")
	case path == "" && content == "":
		return nil, "", invalidInput("請提供 path（伺服器上的檔案）或 content（檔案內容）。")
	case content != "":
		if len(content) > maxBytes {
			return nil, "", invalidInput("內容超過大小上限。")
		}
		return []byte(content), "", nil
	}
	if !filepath.IsAbs(path) {
		return nil, "", invalidInput("path 必須是絕對路徑。")
	}
	// 先解析 symlink 再檢查副檔名：x.srt -> /etc/shadow 這類連結不能繞過白名單。
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", invalidInput("無法讀取 path 指定的檔案：找不到，或不是一般檔案。")
	}
	path = resolved
	ext := strings.ToLower(filepath.Ext(path))
	ok := false
	for _, e := range exts {
		ok = ok || ext == e
	}
	if !ok {
		return nil, "", apierr.Newf(http.StatusUnprocessableEntity, CodeInvalidInput, "import_path_ext", apierr.List(exts))
	}
	f, fi, err := pathguard.Unrestricted().Open(path)
	if err != nil {
		return nil, "", invalidInput("無法讀取 path 指定的檔案：找不到，或不是一般檔案。")
	}
	defer f.Close()
	if fi.Size() > int64(maxBytes) {
		return nil, "", invalidInput("檔案超過大小上限。")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		return nil, "", invalidInput("無法讀取 path 指定的檔案，或超過大小上限。")
	}
	return data, filepath.Base(path), nil
}

func invalidInput(msg string) *Error {
	return newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
}
