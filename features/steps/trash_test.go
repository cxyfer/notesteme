package steps

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

func TestExternalIDOf(t *testing.T) {
	for ref, want := range map[string]string{
		"yt:abc123xxxxx":        "abc123xxxxx",
		"bili:BV1xx411c7mD/p2":  "BV1xx411c7mD",
		"pod:show/ep-42":        "ep-42",
		"local:lecture.mp4":     "lecture.mp4",
		"local:talks/intro.m4a": "talks/intro.m4a",
	} {
		if got := externalIDOf(ref); got != want {
			t.Errorf("externalIDOf(%q) = %q，預期 %q", ref, got, want)
		}
	}
}

// 影音在垃圾桶中：只有 404 加上錯誤碼 in_trash 才算；其他 404、其他狀態與其他錯誤都不是。
func TestInTrashBody(t *testing.T) {
	body := `{"code":"in_trash","message":"這部影音在垃圾桶中。","actions":["還原"],"item_id":"i-1"}`
	err := fmt.Errorf("包一層：%w", &apiFailure{method: "GET", path: "/api/items/i-1", status: http.StatusNotFound, body: []byte(body)})
	b, ok := inTrashBody(err)
	if !ok || b.Message != "這部影音在垃圾桶中。" || len(b.Actions) != 1 || b.Actions[0] != "還原" || b.ItemID != "i-1" {
		t.Errorf("inTrashBody = %+v, %v", b, ok)
	}
	for name, e := range map[string]error{
		"nil":       nil,
		"一般錯誤":      errors.New("boom"),
		"404 不是垃圾桶": &apiFailure{status: http.StatusNotFound, body: []byte(`{"code":"not_found"}`)},
		"409 是新增":   &apiFailure{status: http.StatusConflict, body: []byte(body)},
		"內容不是 JSON": &apiFailure{status: http.StatusNotFound, body: []byte("no")},
	} {
		if _, ok := inTrashBody(e); ok {
			t.Errorf("%s 不應該被當成垃圾桶中的影音", name)
		}
	}
	// 錯誤訊息維持原本的格式。
	f := &apiFailure{method: "GET", path: "/api/x", status: 500, body: []byte("oops")}
	if got := f.Error(); got != "GET /api/x：HTTP 500 oops" {
		t.Errorf("Error() = %q", got)
	}
}

func TestLeftovers(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"item-1/a.json", "item-2/b.json", "x/yt-abc123xxxxx.part"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := leftovers(root, "item-1", "abc123xxxxx", "")
	if err != nil || len(got) != 3 { // item-1、item-1/a.json、x/yt-abc123xxxxx.part
		t.Errorf("leftovers = %v, %v", got, err)
	}
	if got, err := leftovers(filepath.Join(root, "missing"), "item-1"); err != nil || len(got) != 0 {
		t.Errorf("目錄不存在：%v, %v", got, err)
	}
	if got, _ := leftovers(root, ""); len(got) != 0 {
		t.Errorf("空的 needle 不比對：%v", got)
	}
}

// 不知道影音 ID 時失敗，不能空洞地通過；知道時掃描 artifacts、media、uploads、tmp。
func TestDataDirHasNoFilesOf(t *testing.T) {
	s := newTestScenario(t)
	if err := s.dataDirHasNoFilesOf("yt:abc123xxxxx"); err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "沒有記下") {
		t.Errorf("沒有記下 ID 應該失敗：%v", err)
	}
	if err := s.mediaCacheHasNoFilesOf("yt:abc123xxxxx"); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("媒體快取的步驟同樣不能空洞地通過：%v", err)
	}

	const id = "0192f3a0-0000-7000-8000-000000000001"
	s.World.Refs["yt:abc123xxxxx"] = id
	if err := s.dataDirHasNoFilesOf("yt:abc123xxxxx"); err != nil {
		t.Errorf("沒有任何檔案：%v", err)
	}
	for _, sub := range []string{"artifacts", "media", "uploads", "tmp"} {
		dir := filepath.Join(s.World.DataDir(), sub, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.dataDirHasNoFilesOf("yt:abc123xxxxx"); err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("%s 有殘留應該失敗並指出位置：%v", sub, err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	// 其他影音的檔案不算。
	other := filepath.Join(s.World.DataDir(), "artifacts", "0192f3a0-0000-7000-8000-000000000002")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.dataDirHasNoFilesOf("yt:abc123xxxxx"); err != nil {
		t.Errorf("其他影音的檔案：%v", err)
	}

	// 媒體快取也比對平台 ID（例如下載到 media/ 的檔名）。
	if err := os.WriteFile(filepath.Join(s.World.DataDir(), "media", "x-abc123xxxxx.mp4"), nil, 0o644); err != nil {
		if err := os.MkdirAll(filepath.Join(s.World.DataDir(), "media"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(s.World.DataDir(), "media", "x-abc123xxxxx.mp4"), nil, 0o644)
	}
	if err := s.mediaCacheHasNoFilesOf("yt:abc123xxxxx"); err == nil {
		t.Error("檔名含平台 ID 的媒體應該被找到")
	}
}

func TestPendingIfNotImplemented(t *testing.T) {
	if err := pendingIfNotImplemented(fmt.Errorf("包一層：%w", &pipeline.NotImplementedError{What: "x"})); !errors.Is(err, godog.ErrPending) {
		t.Errorf("還沒實作應該是 pending：%v", err)
	}
	boom := errors.New("boom")
	if err := pendingIfNotImplemented(boom); !errors.Is(err, boom) || errors.Is(err, godog.ErrPending) {
		t.Errorf("其他錯誤原樣回傳：%v", err)
	}
	if pendingIfNotImplemented(nil) != nil {
		t.Error("nil 維持 nil")
	}
}

func TestParseUSD(t *testing.T) {
	if v, err := parseUSD("0.42"); err != nil || v != 0.42 {
		t.Errorf("parseUSD = %v, %v", v, err)
	}
	for _, bad := range []string{"", "abc", "-1"} {
		if _, err := parseUSD(bad); err == nil {
			t.Errorf("%q 應該回報錯誤", bad)
		}
	}
}

// 前一個步驟送出的請求是 501 才算；更早的請求、其他狀態與其他錯誤都不算。
func TestPreviousRequestNotImplemented(t *testing.T) {
	s := newTestScenario(t)
	if s.previousRequestNotImplemented() {
		t.Error("還沒有請求")
	}
	s.step = 2
	s.last = &response{status: http.StatusNotImplemented, body: []byte(`{"code":"not_implemented"}`), step: 1}
	if !s.previousRequestNotImplemented() {
		t.Error("前一個步驟的 501 not_implemented")
	}
	s.step = 3
	if s.previousRequestNotImplemented() {
		t.Error("更早的請求不算")
	}
	s.step = 2
	s.last = &response{status: http.StatusNotImplemented, body: []byte(`{"code":"internal_error"}`), step: 1}
	if s.previousRequestNotImplemented() {
		t.Error("501 但不是 not_implemented")
	}
	s.last = &response{status: http.StatusNotFound, body: []byte(`{"code":"not_implemented"}`), step: 1}
	if s.previousRequestNotImplemented() {
		t.Error("不是 501")
	}
}
