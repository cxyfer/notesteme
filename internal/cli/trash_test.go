package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
)

// runWithStdin 和 run 相同，另外指定 stdin。
func runWithStdin(t *testing.T, stdin string, args ...string) (Result, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := Execute(ctx, args, app.IO{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut}, app.Env{})
	return res, out.String(), errOut.String()
}

// fakeTerminal 讓 strings.Reader 也被當成終端機。
func fakeTerminal(t *testing.T) {
	t.Helper()
	old := isTerminal
	isTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { isTerminal = old })
}

const (
	itemsBody = `{"items":[` +
		`{"id":"i1","ref":"yt:abc123xxxxx","title":"A"},` +
		`{"id":"b1","ref":"bili:BV1xx411c7mD/p1","title":"B1"},` +
		`{"id":"b2","ref":"bili:BV1xx411c7mD/p2","title":"B2"},` +
		`{"id":"b3","ref":"bili:BV1xx411c7mD/p3","title":"B3"},` +
		`{"id":"o1","ref":"bili:BV9zz999z9z/p1","title":"Other"}],"total":5}`
	trashBody = `{"items":[` +
		`{"id":"t1","ref":"yt:trashed0001","title":"T1","deleted_at":"2026-11-01T00:00:00Z","purge_at":"2026-12-01T00:00:00Z"},` +
		`{"id":"t2","ref":"bili:BV2yy222y2y/p1","title":"T2","deleted_at":"2026-11-01T00:00:00Z","purge_at":"2026-12-01T00:00:00Z"},` +
		`{"id":"t3","ref":"bili:BV2yy222y2y/p2","title":"T3","deleted_at":"2026-11-01T00:00:00Z","purge_at":"2026-12-01T00:00:00Z"}],"total":3}`
)

// delAPI 是刪除與垃圾桶的假 API。
func delAPI(t *testing.T, override func(w http.ResponseWriter, r *http.Request) bool) (*fakeAPI, string) {
	return newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/items":
			writeJSON(w, 200, itemsBody)
		case "GET /api/trash":
			writeJSON(w, 200, trashBody)
		case "DELETE /api/trash":
			writeJSON(w, 200, `{"purged":3}`)
		default:
			switch {
			case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/items/"):
				id := strings.TrimPrefix(r.URL.Path, "/api/items/")
				writeJSON(w, 200, `{"id":"`+id+`","ref":"r","deleted_at":"2026-11-15T00:00:00Z","purge_at":"2026-12-15T00:00:00Z","kept_outputs":[]}`)
			case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/trash/"):
				writeJSON(w, 200, `{"purged":1}`)
			case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/restore"):
				writeJSON(w, 200, `{"id":"x","ref":"r","title":"t","deleted_at":"2026-11-01T00:00:00Z","purge_at":"2026-12-01T00:00:00Z"}`)
			default:
				writeJSON(w, 404, `{"code":"not_found","message":"沒有這個路徑"}`)
			}
		}
	})
}

// notImplemented501 讓所有刪除與垃圾桶的端點回 501（B1、B2 合併之前的狀態）。
func notImplemented501(w http.ResponseWriter, r *http.Request) bool {
	if strings.Contains(r.URL.Path, "/trash") || r.Method == http.MethodDelete {
		writeJSON(w, 501, `{"code":"not_implemented","message":"尚未實作"}`)
		return true
	}
	return false
}

func mutations(f *fakeAPI) []string {
	var out []string
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			out = append(out, r)
		}
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("請求 = %v，預期 %v", got, want)
	}
}

// 不在終端機上又沒有 --yes：結束碼 2，提示 --yes，而且在連線之前就檢查（不送任何請求）。
func TestRmRefusesWithoutConfirmationOffTerminal(t *testing.T) {
	api, url := delAPI(t, nil)
	for _, args := range [][]string{
		{"items", "rm", "yt:abc123xxxxx", "--server", url, "--token", "t"},
		{"trash", "empty", "--server", url, "--token", "t"},
	} {
		res, out, errOut := runWithStdin(t, "y\n", args...)
		if res.Code != 2 || res.Mode != "" {
			t.Errorf("%v：結果 = %+v", args, res)
		}
		if !strings.Contains(errOut, "--yes") || out != "" {
			t.Errorf("%v：stdout = %q，stderr = %q", args, out, errOut)
		}
	}
	if len(api.requests) != 0 {
		t.Errorf("不應該送出任何請求：%v", api.requests)
	}
	// 沒有設定 stdin 的情況（harness）也一樣。
	res, _, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--server", url, "--token", "t")
	if res.Code != 2 || !strings.Contains(errOut, "--yes") {
		t.Errorf("stdin 為 nil：%+v %s", res, errOut)
	}
}

func TestRmMovesToTrash(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 || res.Mode != ModeClient {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/i1"})
	if out != "已移到垃圾桶：yt:abc123xxxxx\n" {
		t.Errorf("輸出 = %q", out)
	}
	// 沒有 --delete-outputs 時不帶 body。
	if api.bodies[len(api.bodies)-1] != "" {
		t.Errorf("body = %q", api.bodies[len(api.bodies)-1])
	}
}

func TestRmJSONOutputsAPIResponseAsJSONLines(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "b1", "--delete-outputs", "--yes", "--json", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"id":"i1"`) || !strings.HasPrefix(lines[1], `{"id":"b1"`) {
		t.Errorf("輸出 = %q", out)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/i1", "DELETE /api/items/b1"})
	for _, b := range api.bodies[len(api.bodies)-2:] {
		if b != `{"delete_outputs":true}` {
			t.Errorf("body = %q", b)
		}
	}
}

// Bilibili 代號不帶分P：套用到所有分P，不碰其他影片；帶分P 只套用到那一P。
func TestRmBilibiliWithoutPartAppliesToAllParts(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "items", "rm", "bili:BV1xx411c7mD", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/b1", "DELETE /api/items/b2", "DELETE /api/items/b3"})
	if strings.Count(out, "已移到垃圾桶：") != 3 {
		t.Errorf("輸出 = %q", out)
	}

	api, url = delAPI(t, nil)
	res, _, errOut = run(t, app.Env{}, "items", "rm", "bili:BV1xx411c7mD/p2", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/b2"})

	// 同一部影音被多個參數涵蓋時只處理一次。
	api, url = delAPI(t, nil)
	run(t, app.Env{}, "items", "rm", "bili:BV1xx411c7mD/p2", "bili:BV1xx411c7mD", "--yes", "--server", url, "--token", "t")
	eq(t, mutations(api), []string{"DELETE /api/items/b2", "DELETE /api/items/b1", "DELETE /api/items/b3"})
}

// 有任何一個代號找不到，整批都不做。
func TestRmUnresolvedRefAbortsWholeBatch(t *testing.T) {
	api, url := delAPI(t, nil)
	res, _, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "yt:missing0000", "--yes", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" || !strings.Contains(errOut, "yt:missing0000") {
		t.Errorf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), nil)
}

func TestRmPurge(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--purge", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/i1", "DELETE /api/trash/i1"})
	if out != "已永久刪除：yt:abc123xxxxx\n" {
		t.Errorf("輸出 = %q", out)
	}

	// --json：輸出最後一個 API 的回應（永久刪除）。
	_, url = delAPI(t, nil)
	_, out, _ = run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--purge", "--yes", "--json", "--server", url, "--token", "t")
	if strings.TrimSpace(out) != `{"purged":1}` {
		t.Errorf("--json 輸出 = %q", out)
	}
}

// 已在垃圾桶中：DELETE item 回 404 in_trash 時直接永久刪除。
func TestRmPurgeWhenAlreadyInTrash(t *testing.T) {
	api, url := delAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/items/i1" {
			writeJSON(w, 404, `{"code":"in_trash","message":"這部影音在垃圾桶中。"}`)
			return true
		}
		return false
	})
	res, _, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--purge", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/i1", "DELETE /api/trash/i1"})
}

// 清單裡沒有（垃圾桶中的影音不在清單中）：--purge 改從垃圾桶解析。
func TestRmPurgeResolvesFromTrash(t *testing.T) {
	api, url := delAPI(t, nil)
	res, _, errOut := run(t, app.Env{}, "items", "rm", "yt:trashed0001", "--purge", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	// 垃圾桶中的影音不能再移到垃圾桶：只做永久刪除。
	eq(t, mutations(api), []string{"DELETE /api/items/t1", "DELETE /api/trash/t1"})

	// 沒有 --purge 時不查垃圾桶。
	api, url = delAPI(t, nil)
	res, _, _ = run(t, app.Env{}, "items", "rm", "yt:trashed0001", "--yes", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" || api.count("GET /api/trash") != 0 {
		t.Errorf("結果 = %+v，請求 = %v", res, api.requests)
	}
}

// 沒有 --purge 時，404 in_trash 是錯誤（結束碼 1，保留錯誤碼）。
func TestRmInTrashErrorWithoutPurge(t *testing.T) {
	_, url := delAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" {
			writeJSON(w, 404, `{"code":"in_trash","message":"這部影音在垃圾桶中。"}`)
			return true
		}
		return false
	})
	res, _, errOut := run(t, app.Env{}, "items", "rm", "yt:abc123xxxxx", "--yes", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "in_trash" || !strings.Contains(errOut, "yt:abc123xxxxx") {
		t.Errorf("結果 = %+v %s", res, errOut)
	}
}

// 409 對應到結束碼 1；中途失敗就停下，已完成的不復原。
func TestRmStopsAtFirstFailure(t *testing.T) {
	api, url := delAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && r.URL.Path == "/api/items/b2" {
			writeJSON(w, 409, `{"code":"conflict","message":"衝突"}`)
			return true
		}
		return false
	})
	res, out, _ := run(t, app.Env{}, "items", "rm", "bili:BV1xx411c7mD", "--yes", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "conflict" {
		t.Errorf("結果 = %+v", res)
	}
	eq(t, mutations(api), []string{"DELETE /api/items/b1", "DELETE /api/items/b2"})
	if out != "已移到垃圾桶：bili:BV1xx411c7mD/p1\n" {
		t.Errorf("輸出 = %q", out)
	}
}

// API 回 501：CLI 回報 not_implemented，結束碼 1（B1、B2 合併之前相依的場景維持 pending）。
func TestDeleteCommandsReportNotImplemented(t *testing.T) {
	for _, args := range [][]string{
		{"items", "rm", "yt:abc123xxxxx", "--yes"},
		{"items", "rm", "yt:abc123xxxxx", "--purge", "--yes"},
		{"items", "restore", "yt:trashed0001"},
		{"trash", "ls"},
		{"trash", "empty", "--yes"},
	} {
		_, url := delAPI(t, notImplemented501)
		res, _, _ := run(t, app.Env{}, append(args, "--server", url, "--token", "t")...)
		if res.Code != 1 || res.ErrorCode != "not_implemented" {
			t.Errorf("%v：結果 = %+v", args, res)
		}
	}
}

func TestRestore(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "items", "restore", "yt:trashed0001", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"POST /api/trash/t1/restore"})
	if out != "已還原：yt:trashed0001\n" {
		t.Errorf("輸出 = %q", out)
	}
	// 只從垃圾桶解析，不讀媒體庫。
	if api.count("GET /api/items") != 0 {
		t.Errorf("請求 = %v", api.requests)
	}

	// Bilibili 不帶分P：垃圾桶中的所有分P；--json 為 JSON Lines。
	api, url = delAPI(t, nil)
	res, out, _ = run(t, app.Env{}, "items", "restore", "bili:BV2yy222y2y", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || len(strings.Split(strings.TrimSpace(out), "\n")) != 2 {
		t.Errorf("結果 = %+v，輸出 = %q", res, out)
	}
	eq(t, mutations(api), []string{"POST /api/trash/t2/restore", "POST /api/trash/t3/restore"})
}

func TestRestoreUnresolvedAbortsWholeBatch(t *testing.T) {
	api, url := delAPI(t, nil)
	res, _, errOut := run(t, app.Env{}, "items", "restore", "yt:trashed0001", "yt:abc123xxxxx", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" || !strings.Contains(errOut, "yt:abc123xxxxx") {
		t.Errorf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), nil)
}

func TestTrashLs(t *testing.T) {
	_, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "trash", "ls", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "yt:trashed0001\tT1\t剩餘 ") || !strings.HasSuffix(lines[0], " 天") {
		t.Errorf("輸出 = %q", out)
	}
	// --json 原樣輸出 API 的回應。
	res, out, _ = run(t, app.Env{}, "trash", "ls", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || strings.TrimSpace(out) != trashBody {
		t.Errorf("--json 輸出 = %q", out)
	}
}

func TestDaysLeft(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		purge time.Time
		want  int
	}{
		{now.Add(-time.Hour), 0},
		{now, 0},
		{now.Add(time.Minute), 1},
		{now.Add(24 * time.Hour), 1},
		{now.Add(24*time.Hour + time.Second), 2},
		{now.Add(30 * 24 * time.Hour), 30},
	} {
		if got := daysLeft(c.purge, now); got != c.want {
			t.Errorf("daysLeft(%v) = %d，預期 %d", c.purge.Sub(now), got, c.want)
		}
	}
}

func TestTrashEmpty(t *testing.T) {
	api, url := delAPI(t, nil)
	res, out, errOut := run(t, app.Env{}, "trash", "empty", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{"DELETE /api/trash"})
	if out != "已永久刪除 3 部影音。\n" {
		t.Errorf("輸出 = %q", out)
	}
	res, out, _ = run(t, app.Env{}, "trash", "empty", "--yes", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || strings.TrimSpace(out) != `{"purged":3}` {
		t.Errorf("--json 輸出 = %q", out)
	}
}

// 在終端機上詢問：y 才執行，其他回答、EOF 都不做，結束碼 1。
func TestConfirmationPrompt(t *testing.T) {
	fakeTerminal(t)
	for _, c := range []struct {
		stdin string
		want  int
		muts  []string
	}{
		{"y\n", 0, []string{"DELETE /api/items/i1"}},
		{"YES\n", 0, []string{"DELETE /api/items/i1"}},
		{"n\n", 1, nil},
		{"\n", 1, nil},
		{"", 1, nil},
	} {
		api, url := delAPI(t, nil)
		res, out, errOut := runWithStdin(t, c.stdin, "items", "rm", "yt:abc123xxxxx", "--server", url, "--token", "t")
		if res.Code != c.want {
			t.Errorf("stdin %q：結果 = %+v %s", c.stdin, res, errOut)
		}
		if !strings.Contains(errOut, "[y/N]") || strings.Contains(out, "[y/N]") {
			t.Errorf("stdin %q：提示應該印在 stderr：stdout = %q stderr = %q", c.stdin, out, errOut)
		}
		eq(t, mutations(api), c.muts)
	}

	api, url := delAPI(t, nil)
	res, _, _ := runWithStdin(t, "n\n", "trash", "empty", "--server", url, "--token", "t")
	if res.Code != 1 || len(mutations(api)) != 0 {
		t.Errorf("trash empty 拒絕：%+v %v", res, api.requests)
	}
	res, _, _ = runWithStdin(t, "y\n", "trash", "empty", "--server", url, "--token", "t")
	if res.Code != 0 || len(mutations(api)) != 1 {
		t.Errorf("trash empty 同意：%+v %v", res, api.requests)
	}
}

// 代號找不到時不會詢問。
func TestConfirmationAfterResolution(t *testing.T) {
	fakeTerminal(t)
	_, url := delAPI(t, nil)
	res, _, errOut := runWithStdin(t, "y\n", "items", "rm", "yt:missing0000", "--server", url, "--token", "t")
	if res.Code != 1 || strings.Contains(errOut, "[y/N]") {
		t.Errorf("結果 = %+v %s", res, errOut)
	}
}

func TestDeleteUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"items", "rm"},
		{"items", "rm", "--yes"},
		{"items", "rm", "yt:abc123xxxxx", "--nope"},
		{"items", "restore"},
		{"trash"},
		{"trash", "--json"},
		{"trash", "frobnicate"},
		{"trash", "ls", "extra"},
		{"trash", "empty", "extra", "--yes"},
	} {
		res, _, errOut := run(t, app.Env{}, args...)
		if res.Code != 2 || res.Mode != "" {
			t.Errorf("%v：結果 = %+v %s", args, res, errOut)
		}
	}
}
