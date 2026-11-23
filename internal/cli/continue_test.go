package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestItemsContinueUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"缺 ref":   {"items", "continue"},
		"空字串 ref": {"items", "continue", " "},
		"不認得的旗標":  {"items", "continue", "--bogus", "yt:a"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, data := testEnv(t)
			res, _, errOut := run(t, env, args...)
			if res.Code != 2 || res.Mode != "" {
				t.Fatalf("結果 = %+v，預期結束碼 2（stderr：%s）", res, errOut)
			}
			if _, err := os.Stat(data); err == nil {
				t.Error("參數錯誤不應該碰資料目錄")
			}
		})
	}
}

func TestItemsContinueClientMode(t *testing.T) {
	var batches [][]string
	var fake *fakeAPI
	fake, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/items/continue" {
			writeJSON(w, 404, `{"code":"not_found","message":"找不到。"}`)
			return
		}
		fake.mu.Lock()
		body := []byte(fake.bodies[len(fake.bodies)-1])
		fake.mu.Unlock()
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, 400, `{"code":"bad_request","message":"壞的 JSON"}`)
			return
		}
		batches = append(batches, req.IDs)
		var cont, skip []string
		for _, id := range req.IDs {
			if strings.HasPrefix(id, "gone") {
				b, _ := json.Marshal(map[string]string{"id": id, "code": "not_found"})
				skip = append(skip, string(b))
				continue
			}
			b, _ := json.Marshal(id)
			cont = append(cont, string(b))
		}
		writeJSON(w, 200, `{"continued":[`+strings.Join(cont, ",")+`],"skipped":[`+strings.Join(skip, ",")+`]}`)
	})

	res, out, errOut := run(t, app.Env{}, "items", "continue", "yt:a", "yt:b", "--server", url, "--token", "tok")
	if res.Code != 0 || res.Mode != ModeClient || out != "已繼續：yt:a\n已繼續：yt:b\n" {
		t.Fatalf("全部繼續：%+v %q %s", res, out, errOut)
	}
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("應該只送一批：%v", batches)
	}

	// 有任何一部沒被繼續 → 結束碼 1，並列出原因。
	res, out, _ = run(t, app.Env{}, "items", "continue", "yt:a", "gone1", "--server", url, "--token", "tok")
	if res.Code != 1 || !strings.Contains(out, "已繼續：yt:a") || !strings.Contains(out, "略過：gone1（找不到這部影音）") {
		t.Errorf("部分略過：%+v %q", res, out)
	}

	// --json 輸出合併後的結果。
	res, out, _ = run(t, app.Env{}, "items", "continue", "--json", "gone2", "--server", url, "--token", "tok")
	if res.Code != 1 || !strings.Contains(out, `"skipped":[{"id":"gone2","code":"not_found"}]`) || !strings.Contains(out, `"continued":[]`) {
		t.Errorf("--json：%+v %q", res, out)
	}

	// 超過 100 個時分批送出，每批不超過 100。
	batches = nil
	refs := make([]string, 0, 250)
	for i := range 250 {
		refs = append(refs, "id"+strings.Repeat("x", i%3)+string(rune('a'+i%26)))
	}
	args := append([]string{"items", "continue"}, refs...)
	args = append(args, "--server", url, "--token", "tok")
	res, out, errOut = run(t, app.Env{}, args...)
	if res.Code != 0 || len(batches) != 3 || len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("分批：%+v batches=%d %s", res, len(batches), errOut)
	}
	if strings.Count(out, "已繼續：") != 250 {
		t.Errorf("分批後應列出全部 250 部")
	}
}

func TestContinueSkipReason(t *testing.T) {
	for code, want := range map[string]string{"not_found": "找不到這部影音", "in_trash": "影音在垃圾桶中", "not_awaiting_user": "沒有在等待你決定是否繼續", "weird": "weird"} {
		if got := continueSkipReason(code); got != want {
			t.Errorf("%s → %q，預期 %q", code, got, want)
		}
	}
}
