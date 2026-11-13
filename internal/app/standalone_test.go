package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// call 對 standalone 的 handler 送出請求，不帶任何憑證。
func call(t *testing.T, h http.Handler, method, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestStandaloneIngestAndRunUntilIdle(t *testing.T) {
	env := testEnv(t)
	r := &ytRunner{}
	env.Runner = r
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	// 鎖檔記錄持有者是 cli，沒有 Listen 與 Socket。
	info, err := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"])
	if err != nil || info == nil || info.Holder != "cli" || info.PID != os.Getpid() || info.Listen != "" || info.Socket != "" {
		t.Fatalf("lock info = %+v, %v", info, err)
	}
	// standalone 沒有 socket。
	if _, err := os.Lstat(socketPath(env.Vars["NOTESTEME_DATA"])); err == nil {
		t.Error("standalone 不應該建立 socket")
	}

	var created struct {
		Items []detail `json:"items"`
	}
	code, body := call(t, sa.Handler(), "POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abcdefghijk"}`)
	if code != 201 {
		t.Fatalf("POST /api/items = %d %s", code, body)
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.Items) != 1 {
		t.Fatalf("回應 = %s, %v", body, err)
	}
	id := created.Items[0].ID

	// 新增之後任務留在佇列：standalone 不會自己執行（沒有背景 worker）。
	var d detail
	_, body = call(t, sa.Handler(), "GET", "/api/items/"+id, "")
	if err := json.Unmarshal(body, &d); err != nil || d.status("metadata") != "pending" || len(r.calls) != 0 {
		t.Fatalf("新增後 detail = %s（yt-dlp 呼叫 %d 次）", body, len(r.calls))
	}

	if err := sa.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, body = call(t, sa.Handler(), "GET", "/api/items/"+id, "")
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.Title != "Hello" || d.status("metadata") != "done" || d.status("transcript") != "done" || d.status("normalize") != "done" {
		t.Fatalf("detail = %s", body)
	}

	// 沒有任務時 RunUntilIdle 立刻回傳。
	if err := sa.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}

	// 關閉後清空鎖檔並釋放鎖；可重複呼叫。
	if err := sa.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sa.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if info, _ := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"]); info != nil {
		t.Errorf("關閉後鎖檔應該是空的，got %+v", info)
	}
	lock, err := store.LockDir(env.Vars["NOTESTEME_DATA"])
	if err != nil {
		t.Fatalf("關閉後鎖應該已釋放：%v", err)
	}
	_ = lock.Close()
}

// 受保護的路由不需要憑證，但仍是同一組路由（沒實作的回 501）。
func TestStandaloneHandlerIsLocal(t *testing.T) {
	sa, err := OpenStandalone(t.Context(), testEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	if code, body := call(t, sa.Handler(), "GET", "/api/items", ""); code != 200 {
		t.Errorf("GET /api/items = %d %s", code, body)
	}
	if code, _ := call(t, sa.Handler(), "POST", "/api/estimate", ""); code != 501 {
		t.Errorf("POST /api/estimate = %d，預期 501", code)
	}
}

// serve 持有鎖時 OpenStandalone 回傳 store.ErrLocked（原樣），呼叫端讀 LockInfo 決定改走 client。
func TestOpenStandaloneWhileServeHoldsLock(t *testing.T) {
	env := testEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()

	_, err = OpenStandalone(t.Context(), env)
	if err != store.ErrLocked { //nolint:errorlint // 要確認是原樣的 ErrLocked，不是包裝過的
		t.Fatalf("err = %v，預期 store.ErrLocked", err)
	}
	info, err := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"])
	if err != nil || info == nil || info.Holder != "serve" || info.Socket != socketPath(env.Vars["NOTESTEME_DATA"]) || info.Listen != srv.Addr() {
		t.Errorf("lock info = %+v, %v", info, err)
	}
}

// 兩個 standalone 互斥，CLI 持有時 serve 拒絕啟動並說明是 notesteme 指令在使用。
func TestServeRefusesWhileCLIHoldsLock(t *testing.T) {
	env := testEnv(t)
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := OpenStandalone(t.Context(), env); !errors.Is(err, store.ErrLocked) {
		t.Errorf("第二個 standalone err = %v，預期 ErrLocked", err)
	}

	_, err = StartServer(t.Context(), env)
	if !errors.Is(err, store.ErrLocked) {
		t.Fatalf("err = %v，預期仍是 ErrLocked", err)
	}
	want := "資料目錄正被 notesteme 指令使用中（" + strconv.Itoa(os.Getpid()) + "），請等它結束。"
	if err.Error() != want {
		t.Errorf("訊息 = %q，預期 %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "另一個 notesteme serve") {
		t.Error("CLI 持有時不應該說是另一個 serve")
	}

	// 失敗的 serve 不能動到 CLI 的鎖檔內容。
	if info, _ := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"]); info == nil || info.Holder != "cli" {
		t.Errorf("鎖檔 = %+v，預期仍是 cli", info)
	}

	if err := sa.Close(); err != nil {
		t.Fatal(err)
	}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("CLI 結束後 serve 應該能啟動：%v", err)
	}
	srv.Kill()
}

// serve 持有時，第二個 serve 的訊息維持 CLI-R1 的逐字內容。
func TestSecondServeMessageUnchanged(t *testing.T) {
	env := testEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	_, err = StartServer(t.Context(), env)
	if err == nil || err.Error() != "資料目錄已經由另一個 notesteme serve 使用中。" {
		t.Fatalf("err = %v", err)
	}
}

// standalone 的啟動檢查與 serve 相同：沒有 master key 時不碰資料目錄。
func TestStandaloneGuards(t *testing.T) {
	env := testEnv(t)
	delete(env.Vars, "NOTESTEME_MASTER_KEY")
	if _, err := OpenStandalone(t.Context(), env); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(env.Vars["NOTESTEME_DATA"]); !os.IsNotExist(err) {
		t.Error("master key 檢查失敗時不應該建立資料目錄")
	}
}
