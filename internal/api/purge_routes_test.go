package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// purgeRig 在 itemsRig 上加上：新增並處理影音、用 SQL 直接移到垃圾桶（移到垃圾桶屬於 DEL-B1，這裡只測永久刪除）。
type purgeRig struct {
	*itemsRig
	dir string
}

func newPurgeRoutesRig(t *testing.T) *purgeRig {
	t.Helper()
	pr := &purgeRig{}
	pr.itemsRig = newItemsRig(t, func(d *pipeline.Deps) { pr.dir = d.DataDir })
	return pr
}

// add 新增一部影音並處理完，回傳它的 ID。
func (r *purgeRig) add(url string) string {
	r.t.Helper()
	rec := r.owner("POST", "/api/items", `{"url":"`+url+`"}`)
	if rec.Code != http.StatusCreated {
		r.t.Fatalf("新增 = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		r.t.Fatalf("回應 = %s, %v", rec.Body, err)
	}
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	return body.Items[0].ID
}

func (r *purgeRig) trash(id string) {
	r.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		r.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET deleted_at = ? WHERE id = ?`, r.clk.Now().UnixMilli(), id); err != nil {
		r.t.Fatal(err)
	}
}

func purgeErrCode(b []byte) string {
	var e Error
	_ = json.Unmarshal(b, &e)
	return e.Code
}

// 預覽：產出數、位元組、花費、輸出檔清單都在回應裡，而且是 JSON 的陣列與數字（outputs 不是 null）。
func TestDeletionPreviewRoute(t *testing.T) {
	r := newPurgeRoutesRig(t)
	id := r.add("https://www.youtube.com/watch?v=abc123xxxxx")
	if err := r.st.Write().InsertCostEntry(context.Background(), store.CostEntry{ItemID: id, USD: 0.5, State: store.CostActual}); err != nil {
		t.Fatal(err)
	}

	rec := r.owner("GET", "/api/items/"+id+"/deletion-preview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["outputs"]) != "[]" {
		t.Errorf("outputs = %s，預期 []", raw["outputs"])
	}
	var body struct {
		Artifacts int     `json:"artifacts"`
		Bytes     int64   `json:"bytes"`
		SpentUSD  float64 `json:"spent_usd"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Artifacts < 1 || body.Bytes < 1 || body.SpentUSD != 0.5 {
		t.Errorf("body = %+v", body)
	}
	// 預覽之後影音還在媒體庫。
	if rec := r.owner("GET", "/api/items/"+id, ""); rec.Code != http.StatusOK {
		t.Errorf("預覽之後 GET = %d", rec.Code)
	}
}

func TestDeletionPreviewRouteErrors(t *testing.T) {
	r := newPurgeRoutesRig(t)
	if rec := r.owner("GET", "/api/items/no-such-item/deletion-preview", ""); rec.Code != http.StatusNotFound || purgeErrCode(rec.Body.Bytes()) != CodeNotFound {
		t.Errorf("不存在：%d %s", rec.Code, rec.Body)
	}
	id := r.add("https://www.youtube.com/watch?v=abc123xxxxx")
	r.trash(id)
	if rec := r.owner("GET", "/api/items/"+id+"/deletion-preview", ""); rec.Code != http.StatusNotFound || purgeErrCode(rec.Body.Bytes()) != CodeInTrash {
		t.Errorf("垃圾桶中：%d %s，預期 404 in_trash", rec.Code, rec.Body)
	}
}

// 永久刪除單一影音：垃圾桶中的回 200 {purged: 1}；不在垃圾桶中的回 404，什麼都不刪。
func TestPurgeRoute(t *testing.T) {
	r := newPurgeRoutesRig(t)
	id := r.add("https://www.youtube.com/watch?v=abc123xxxxx")

	if rec := r.owner("DELETE", "/api/trash/"+id, ""); rec.Code != http.StatusNotFound || purgeErrCode(rec.Body.Bytes()) != CodeNotFound {
		t.Fatalf("不在垃圾桶中：%d %s，預期 404 not_found", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/items/"+id, ""); rec.Code != http.StatusOK {
		t.Fatal("不在垃圾桶中的影音不該被刪除")
	}

	r.trash(id)
	rec := r.owner("DELETE", "/api/trash/"+id, "")
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("永久刪除 = %d %s", rec.Code, rec.Body)
	}
	// 回應帶著被刪除的影音的 ID 與代號：影音已經不存在，CLI 的 --json 要輸出代號。
	var body struct {
		Purged int    `json:"purged"`
		ID     string `json:"id"`
		Ref    string `json:"ref"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Purged != 1 || body.ID != id || body.Ref != "yt:abc123xxxxx" {
		t.Errorf("body = %+v，預期 purged 1、id %s、ref yt:abc123xxxxx", body, id)
	}
	if rec := r.owner("GET", "/api/items/"+id, ""); rec.Code != http.StatusNotFound || purgeErrCode(rec.Body.Bytes()) != CodeNotFound {
		t.Errorf("永久刪除之後：%d %s，預期 404 not_found（不是 in_trash）", rec.Code, rec.Body)
	}
	// 再刪一次：已經不存在。
	if rec := r.owner("DELETE", "/api/trash/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("重複永久刪除 = %d", rec.Code)
	}
}

// 清空垃圾桶：回傳永久刪除的數量，媒體庫中的影音不受影響；空的垃圾桶回 0。
func TestEmptyTrashRoute(t *testing.T) {
	r := newPurgeRoutesRig(t)
	a := r.add("https://www.youtube.com/watch?v=aaaaaaaaaaa")
	b := r.add("https://www.youtube.com/watch?v=bbbbbbbbbbb")
	live := r.add("https://www.youtube.com/watch?v=ccccccccccc")
	r.trash(a)
	r.trash(b)

	for _, want := range []int{2, 0} {
		rec := r.owner("DELETE", "/api/trash", "")
		var body struct {
			Purged *int `json:"purged"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusOK || body.Purged == nil || *body.Purged != want {
			t.Fatalf("清空垃圾桶 = %d %s，預期 purged %d", rec.Code, rec.Body, want)
		}
		// 清空維持只有 {"purged": n}，不帶單一影音的欄位。
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		if _, has := raw["id"]; has || len(raw) != 1 {
			t.Errorf("清空垃圾桶的回應 = %s，預期只有 purged", rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/items/"+live, ""); rec.Code != http.StatusOK {
		t.Errorf("媒體庫中的影音 GET = %d", rec.Code)
	}
}
