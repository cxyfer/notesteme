package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// items show 的文字模式：skipped 或 blocked 而且有 block_reason 時，顯示「（reason）」，
// 之後照舊接「：message」（M2 S0 契約 3.5）。
func TestItemsShowTextShowsBlockReason(t *testing.T) {
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/items":
			writeJSON(w, 200, `{"items":[{"id":"i9","ref":"yt:abc123xxxxx"}]}`)
		default:
			writeJSON(w, 200, `{"id":"i9","ref":"yt:abc123xxxxx","title":"T","stages":[
				{"stage":"transcript","status":"done"},
				{"stage":"correct","status":"skipped","block_reason":"not_configured"},
				{"stage":"translate","status":"skipped"},
				{"stage":"publish","status":"blocked","block_reason":"cookie","message":"需要 cookie"},
				{"stage":"normalize","status":"pending","block_reason":"ignored"}
			]}`)
		}
	})
	res, out, errOut := run(t, app.Env{}, "items", "show", "yt:abc123xxxxx", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("show：%+v stderr=%s", res, errOut)
	}
	for _, want := range []string{
		"  correct    skipped（not_configured）\n",
		"  translate  skipped\n", // 沒有原因時不加括號
		"  publish    blocked（cookie）：需要 cookie\n",
		"  normalize  pending\n", // 其他狀態不顯示原因
		"  transcript done\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("輸出缺少 %q：\n%s", want, out)
		}
	}
}

// items show 的文字模式會在詳情後列出已寫出的輸出檔（OUT-R11）；--json 原樣輸出詳情，不多呼叫。
func TestItemsShowListsOutputFiles(t *testing.T) {
	var outputsCalls int
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/items":
			writeJSON(w, 200, `{"items":[{"id":"i9","ref":"local:lecture.mp4"}]}`)
		case "/api/items/i9/outputs":
			outputsCalls++
			writeJSON(w, 200, `{"outputs":[{"path":"/data/inbox/lecture.en.srt","target":"sidecar","format":"srt","lang":"en","sha256":"aa"}]}`)
		default:
			writeJSON(w, 200, `{"id":"i9","ref":"local:lecture.mp4","title":"lecture","stages":[{"stage":"publish","status":"done"}]}`)
		}
	})
	res, out, errOut := run(t, app.Env{}, "items", "show", "local:lecture.mp4", "--server", url, "--token", "t")
	if res.Code != 0 || !strings.Contains(out, "  輸出檔：\n    sidecar\tsrt\ten\t/data/inbox/lecture.en.srt\n") {
		t.Fatalf("show：%+v stdout=%s stderr=%s", res, out, errOut)
	}
	if _, out, _ = run(t, app.Env{}, "items", "show", "local:lecture.mp4", "--json", "--server", url, "--token", "t"); strings.Contains(out, "輸出檔") || outputsCalls != 1 {
		t.Errorf("--json 不該列出輸出檔（呼叫 %d 次）：%s", outputsCalls, out)
	}
}
