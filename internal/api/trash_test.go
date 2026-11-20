package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/youtube"
)

// ingestOne 新增一部影音並跑到結束，回傳影音 ID。
func (r *itemsRig) ingestOne(url string) string {
	r.t.Helper()
	rec := r.owner("POST", "/api/items", `{"url":"`+url+`"}`)
	if rec.Code != http.StatusCreated {
		r.t.Fatalf("新增 = %d %s", rec.Code, rec.Body)
	}
	id := decode[itemsBody](r.t, rec).Items[0].ID
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	return id
}

const abcURL = "https://www.youtube.com/watch?v=abc123xxxxx"

func errBody(t *testing.T, rec *httptest.ResponseRecorder) Error {
	t.Helper()
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("不是錯誤格式：%s", rec.Body)
	}
	return e
}

type trashBodyJSON struct {
	ID          string    `json:"id"`
	Ref         string    `json:"ref"`
	DeletedAt   time.Time `json:"deleted_at"`
	PurgeAt     time.Time `json:"purge_at"`
	KeptOutputs []any     `json:"kept_outputs"`
}

func TestDeleteMovesItemToTrash(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)

	rec := r.owner("DELETE", "/api/items/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	b := decode[trashBodyJSON](t, rec)
	if b.ID != id || b.Ref != "yt:abc123xxxxx" || !b.DeletedAt.Equal(r.clk.Now()) || !b.PurgeAt.Equal(b.DeletedAt.Add(30*24*time.Hour)) {
		t.Errorf("回應 = %+v", b)
	}
	if b.KeptOutputs == nil {
		t.Error("kept_outputs 應該是空陣列，不是 null")
	}
	// 帶 body 也可以（M1 沒有作用）。
	if rec := r.owner("DELETE", "/api/items/"+id, `{"delete_outputs":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("已在垃圾桶中再刪 = %d，預期 404", rec.Code)
	}
}

// 垃圾桶中的影音：所有讀取與處理的端點回 404 in_trash，內容帶動作與影音 ID（DEL-R1）。
func TestItemsInTrashAnswer404(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/items/" + id, ""},
		{"GET", "/api/items/" + id + "/cues", ""},
		{"GET", "/api/items/" + id + "/subtitles?format=srt", ""},
		{"GET", "/api/items/" + id + "/runs", ""},
		{"POST", "/api/items/" + id + "/rerun", `{"from":"normalize"}`},
		{"POST", "/api/items/" + id + "/cancel", ""},
		{"DELETE", "/api/items/" + id, ""},
	} {
		rec := r.owner(c.method, c.path, c.body)
		e := errBody(t, rec)
		if rec.Code != http.StatusNotFound || e.Code != CodeInTrash || e.ItemID != id ||
			len(e.Actions) != 1 || e.Actions[0] != "還原" || e.Message != "這部影音在垃圾桶中。" {
			t.Errorf("%s %s = %d %s，預期 404 in_trash（含動作與 item_id）", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// 媒體庫列表不含它。
	list := r.owner("GET", "/api/items", "")
	var lb struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &lb)
	if len(lb.Items) != 0 || lb.Total != 0 {
		t.Errorf("媒體庫 = %s，不應含垃圾桶中的影音", list.Body)
	}
}

func TestTrashListAndRestore(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)

	empty := r.owner("GET", "/api/trash", "")
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), `"items":[]`) {
		t.Fatalf("空垃圾桶 = %d %s", empty.Code, empty.Body)
	}
	r.owner("DELETE", "/api/items/"+id, "")
	rec := r.owner("GET", "/api/trash", "")
	var lb struct {
		Items []TrashedItem `json:"items"`
		Total int           `json:"total"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &lb) != nil || lb.Total != 1 || len(lb.Items) != 1 {
		t.Fatalf("GET /api/trash = %d %s", rec.Code, rec.Body)
	}
	got := lb.Items[0]
	if got.ID != id || got.Ref != "yt:abc123xxxxx" || got.Title != "Hello" || !got.PurgeAt.Equal(got.DeletedAt.Add(30*24*time.Hour)) {
		t.Errorf("垃圾桶項目 = %+v", got)
	}

	rec = r.owner("POST", "/api/trash/"+id+"/restore", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body)
	}
	if back := decode[TrashedItem](t, rec); back.ID != id || back.Ref != "yt:abc123xxxxx" {
		t.Errorf("還原結果 = %+v", back)
	}
	if rec := r.owner("GET", "/api/items/"+id+"/subtitles?format=srt", ""); rec.Code != 200 {
		t.Errorf("還原後字幕 = %d %s", rec.Code, rec.Body)
	}
	// 不在垃圾桶中的影音還原：404（不是 in_trash）。
	rec = r.owner("POST", "/api/trash/"+id+"/restore", "")
	if rec.Code != http.StatusNotFound || errBody(t, rec).Code != CodeNotFound {
		t.Errorf("還原不在垃圾桶中的影音 = %d %s，預期 404 not_found", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/trash/no-such-id/restore", ""); rec.Code != http.StatusNotFound {
		t.Errorf("還原不存在的影音 = %d，預期 404", rec.Code)
	}
}

// 刪除與還原只有 owner（或 admin）可以；token 在 AUTH 完成前不能呼叫（DEL-R12）。沒有登入是 401。
func TestTrashRoutesRequireSession(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)
	for _, c := range []struct{ method, path string }{
		{"DELETE", "/api/items/" + id}, {"GET", "/api/trash"}, {"POST", "/api/trash/" + id + "/restore"},
	} {
		if rec := r.do(c.method, c.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 沒有登入 = %d，預期 401", c.method, c.path, rec.Code)
		}
	}
}

// 新增垃圾桶中的影音：單一目標回 409 in_trash（含動作與 item_id），不重新建立、不還原（DEL-R10）。
func TestCreateItemInTrashAnswers409(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)
	r.owner("DELETE", "/api/items/"+id, "")

	rec := r.owner("POST", "/api/items", `{"url":"`+abcURL+`"}`)
	e := errBody(t, rec)
	if rec.Code != http.StatusConflict || e.Code != CodeInTrash || e.ItemID != id || len(e.Actions) != 1 || e.Actions[0] != "還原" ||
		e.Message != "這部影音在垃圾桶中。" {
		t.Fatalf("POST /api/items = %d %s，預期 409 in_trash", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/items/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("新增不應還原：GET = %d", rec.Code)
	}
	// 還原之後再新增：照舊回 200 already_in_library。
	r.owner("POST", "/api/trash/"+id+"/restore", "")
	rec = r.owner("POST", "/api/items", `{"url":"`+abcURL+`"}`)
	if rec.Code != http.StatusOK || !decode[itemsBody](t, rec).Items[0].AlreadyInLibrary {
		t.Errorf("還原後新增 = %d %s", rec.Code, rec.Body)
	}
}

// 播放清單展開時，垃圾桶中的項目標示 in_trash，其他照常建立（201）；全部都在垃圾桶中才回 409。
func TestPlaylistWithTrashedEntries(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.YouTube = stubYT{playlist: []youtube.PlaylistEntry{{ID: "aaaaaaaaaaa"}, {ID: "bbbbbbbbbbb"}}}
	})
	const list = "https://www.youtube.com/playlist?list=PL123"
	rec := r.owner("POST", "/api/items", `{"url":"`+list+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("新增播放清單 = %d %s", rec.Code, rec.Body)
	}
	items := decode[itemsBody](t, rec).Items
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	// 刪掉第一部。
	if rec := r.owner("DELETE", "/api/items/"+items[0].ID, ""); rec.Code != 200 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/items", `{"url":"`+list+`"}`)
	if rec.Code != http.StatusOK { // 沒有新建立的：200
		t.Fatalf("部分在垃圾桶中 = %d %s，預期 200", rec.Code, rec.Body)
	}
	got := decode[itemsBody](t, rec).Items
	if len(got) != 2 || !got[0].InTrash || got[0].AlreadyInLibrary || got[1].InTrash || !got[1].AlreadyInLibrary {
		t.Errorf("items = %+v，預期第一部 in_trash、第二部 already_in_library", got)
	}
	// 兩部都在垃圾桶中：409。
	if rec := r.owner("DELETE", "/api/items/"+items[1].ID, ""); rec.Code != 200 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/items", `{"url":"`+list+`"}`)
	if rec.Code != http.StatusConflict || errBody(t, rec).Code != CodeInTrash {
		t.Errorf("全部在垃圾桶中 = %d %s，預期 409 in_trash", rec.Code, rec.Body)
	}
}

// fakeTasks 記錄被要求結束的任務。
type fakeTasks struct{ got []string }

func (f *fakeTasks) Cancel(_ context.Context, ids []string) error {
	f.got = append(f.got, ids...)
	return nil
}

// 刪除時把取消的任務交給 worker 結束（PIPE-R15）。
func TestDeleteHandsCancelledTasksToTheWorker(t *testing.T) {
	ft := &fakeTasks{}
	r := newItemsRigAPI(t, func(d *Deps) { d.Tasks = ft })
	rec := r.owner("POST", "/api/items", `{"url":"`+abcURL+`"}`) // 任務還在排隊
	id := decode[itemsBody](t, rec).Items[0].ID
	if rec := r.owner("DELETE", "/api/items/"+id, ""); rec.Code != 200 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	if len(ft.got) != 1 {
		t.Errorf("交給 worker 結束的任務 = %v，預期 1 個", ft.got)
	}
}
