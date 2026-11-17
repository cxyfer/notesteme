package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// 清空垃圾桶時有一部影音刪除失敗：其他的照常刪除，回應是 500 internal_error，並帶 purged
// 讓 client 知道已經刪了幾部（這個專案的錯誤一律用 Error 格式，client 以非 2xx 判斷失敗）。
func TestEmptyTrashPartialFailureReportsPurgedCount(t *testing.T) {
	r := newPurgeRoutesRig(t)
	good := r.add("https://www.youtube.com/watch?v=abc123xxxxx")
	r.trash(good)

	// 一部 ID 不是標準 UUID 的影音：永久刪除拒絕組出檔案路徑，所以會失敗。
	ctx := context.Background()
	bad, _, err := r.st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: "bad00000000", URL: "https://www.youtube.com/watch?v=bad00000000"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET id = 'not-a-uuid', deleted_at = ? WHERE id = ?`, r.clk.Now().UnixMilli(), bad.ID); err != nil {
		t.Fatal(err)
	}

	rec := r.owner("DELETE", "/api/trash", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d %s，預期 500", rec.Code, rec.Body)
	}
	var body struct {
		Code   string `json:"code"`
		Purged *int   `json:"purged"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != CodeInternal || body.Purged == nil || *body.Purged != 1 {
		t.Errorf("body = %s，預期 code internal_error、purged 1", rec.Body)
	}
	if _, err := r.st.Read().ItemByID(ctx, good); err == nil {
		t.Error("成功的那部應該已經永久刪除")
	}
}
