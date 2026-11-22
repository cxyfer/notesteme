package app

import (
	"slices"
	"testing"
)

// P7：刪除 prompt 的「使用中」檢查涵蓋影音層的覆寫（items.overrides_json）。經由真實接線（API → prompts.Service →
// Pipeline.PromptUsersIn → 同一個寫入交易）：影音引用時 409 prompt_in_use 並列出 item:<代號>；影音進垃圾桶仍算；
// 永久刪除之後才能刪。
func TestPromptDelete_ItemOverridesBlockDeletion(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	c := login(t, srv)

	if code := c.json("POST", "/api/prompts", `{"id":"my-correct","kind":"correct","body":"校對 {{glossary}}"}`, nil); code != 201 {
		t.Fatalf("建立 prompt = %d", code)
	}
	var created struct {
		Items []detail `json:"items"`
	}
	body := `{"url":"https://www.youtube.com/watch?v=abc123xxxxx","overrides":{"stages":{"correct":{"executor":"agent","prompt":"my-correct@1"}}}}`
	if code := c.json("POST", "/api/items", body, &created); code != 201 {
		t.Fatalf("新增帶 overrides 的影音 = %d", code)
	}
	id := created.Items[0].ID

	var e struct {
		Code   string   `json:"code"`
		UsedBy []string `json:"used_by"`
	}
	if code := c.json("DELETE", "/api/prompts/my-correct", "", &e); code != 409 || e.Code != "prompt_in_use" || !slices.Equal(e.UsedBy, []string{"item:yt:abc123xxxxx"}) {
		t.Fatalf("影音引用時刪除 = %d %+v，預期 409 prompt_in_use used_by [item:yt:abc123xxxxx]", code, e)
	}

	// 垃圾桶中的影音還原後會用到這個 prompt：仍算使用中。
	if code := c.json("DELETE", "/api/items/"+id, "", nil); code != 200 {
		t.Fatalf("移到垃圾桶 = %d", code)
	}
	e.UsedBy = nil
	if code := c.json("DELETE", "/api/prompts/my-correct", "", &e); code != 409 || !slices.Equal(e.UsedBy, []string{"item:yt:abc123xxxxx"}) {
		t.Fatalf("影音在垃圾桶中時刪除 = %d %+v，預期仍是 409", code, e)
	}

	// 永久刪除影音之後沒有人引用，可以刪。
	if code := c.json("DELETE", "/api/trash/"+id, "", nil); code != 200 && code != 204 {
		t.Fatalf("永久刪除影音 = %d", code)
	}
	resp := c.do("DELETE", "/api/prompts/my-correct", "")
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("沒有人引用之後刪除 = %d，預期 204", resp.StatusCode)
	}
}
