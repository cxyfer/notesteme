package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案是 bundle 的匯出與匯入（BND-R1、R3）：`export <item> --format bundle [--out]`、`import <bundle> [--add-versions] [--json]`。

// bundleSuffix 是 bundle 的副檔名；zipMagic 是 zip 的 local file header 簽章（BND-R3 的判斷）。
const bundleSuffix = ".notesteme.zip"

var zipMagic = []byte("PK\x03\x04")

// maxBundleFile 是 CLI 讀取 bundle 的上限：server 的上傳上限可以調整，這裡只防止讀進不合理的大檔。
const maxBundleFile = 1 << 30

// isBundleFile 判斷 import 的檔案是不是 bundle：檔名以 .notesteme.zip 結尾，或內容以 zip 簽章開頭。
func isBundleFile(path string) bool {
	if strings.HasSuffix(strings.ToLower(path), bundleSuffix) {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(zipMagic))
	n, _ := io.ReadFull(f, head)
	return n == len(zipMagic) && bytes.Equal(head, zipMagic)
}

// bundleRejected 是 server 拒絕 bundle（422）：內容不正確不是命令列參數的錯誤，結束碼是 1（BND-R10，同 config import）。
type bundleRejected struct{ ae *apiError }

func (e *bundleRejected) Error() string { return escapeControl(e.ae.Message) }

// runExportBundle 下載 GET /api/items/:id/bundle，寫到 out；out 為空時寫到目前目錄的 <檔名>.notesteme.zip。
func runExportBundle(ctx context.Context, s *session, arg, out string) (int, error) {
	id, err := s.resolveItem(ctx, arg)
	if err != nil {
		return 0, err
	}
	resp, err := s.be.open(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id)+"/bundle", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, errorFromResponse(resp.StatusCode, raw)
	}
	if out == "" {
		out = bundleNameFrom(resp.Header.Get("Content-Disposition"), arg)
	}
	if err := writeFile(out, resp.Body); err != nil {
		return 0, err
	}
	fmt.Fprintf(s.stdout, "已輸出到 %s\n", out)
	return exitOK, nil
}

// bundleNameFrom 取 Content-Disposition 的檔名，只留最後一段；不合格時依代號產生（BND-R1 的規則）。
func bundleNameFrom(cd, arg string) string {
	if _, params, err := mime.ParseMediaType(cd); err == nil {
		name := filepath.Base(filepath.Clean("/" + params["filename"]))
		if strings.HasSuffix(name, bundleSuffix) && !strings.ContainsAny(name, `/\`) && name != bundleSuffix {
			return name
		}
	}
	return pipeline.BundleFileName(arg)
}

// runImportBundle 以 multipart 送出 POST /api/bundles/import（BND-R3）。
func runImportBundle(ctx context.Context, s *session, path string, addVersions bool) (int, error) {
	data, err := readBundleFile(path)
	if err != nil {
		return 0, err
	}
	body, contentType, err := multipartBody("file", filepath.Base(path), data)
	if err != nil {
		return 0, err
	}
	p := "/api/bundles/import"
	if addVersions {
		p += "?on_existing=add_versions"
	}
	raw, _, err := s.be.callRaw(ctx, http.MethodPost, p, contentType, body)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
			return 0, &bundleRejected{ae: ae}
		}
		return 0, err
	}
	if s.g.json {
		s.printRaw(raw)
		return exitOK, nil
	}
	var res struct {
		Item struct {
			Ref   string `json:"ref"`
			Title string `json:"title"`
		} `json:"item"`
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
	}
	fmt.Fprintf(s.stdout, "已匯入 %s（%s）：%d 個產出，略過 %d 個。\n", escapeControl(res.Item.Ref), escapeControl(res.Item.Title), res.Imported, res.Skipped)
	return exitOK, nil
}

func readBundleFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("無法開啟 %s：%w", path, errUnwrapPath(err))
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, errUnwrapPath(err))
	}
	if !fi.Mode().IsRegular() {
		return nil, usagef("%s 不是一般檔案。", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBundleFile+1))
	if err != nil {
		return nil, fmt.Errorf("無法讀取 %s：%w", path, errUnwrapPath(err))
	}
	if len(data) > maxBundleFile {
		return nil, usagef("%s 超過 1 GiB，不是合理的 bundle。", path)
	}
	return data, nil
}
