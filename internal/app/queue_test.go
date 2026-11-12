package app

import "testing"

// 上限與暫停存在資料庫：重啟（Kill 後重新啟動同一份資料）之後保留，worker 依它領取（PIPE-R8、PIPE-R10）。
func TestQueueControlSurvivesRestart(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, srv)
	if code := c.json("PATCH", "/api/queue/limits", `{"download":1}`, nil); code != 200 {
		t.Fatalf("PATCH limits = %d", code)
	}
	if code := c.json("POST", "/api/queue/pause", "", nil); code != 200 {
		t.Fatalf("POST pause = %d", code)
	}
	srv.Kill()

	srv, err = StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	c = login(t, srv)
	var q struct {
		Paused  bool `json:"paused"`
		Classes []struct {
			Class string `json:"class"`
			Limit int    `json:"limit"`
		} `json:"classes"`
	}
	if code := c.json("GET", "/api/queue", "", &q); code != 200 {
		t.Fatalf("GET queue = %d", code)
	}
	if !q.Paused || q.Classes[0].Class != "download" || q.Classes[0].Limit != 1 {
		t.Fatalf("重啟後設定沒有保留：%+v", q)
	}

	// 暫停中：新增的影音不會被領取；恢復後才領取。
	ingest(t, c)
	if n, err := srv.RunRound(t.Context()); err != nil || n != 0 {
		t.Fatalf("暫停時 RunRound = %d, %v", n, err)
	}
	if code := c.json("POST", "/api/queue/resume", "", nil); code != 200 {
		t.Fatalf("POST resume = %d", code)
	}
	if n, err := srv.RunRound(t.Context()); err != nil || n != 1 {
		t.Fatalf("恢復後 RunRound = %d, %v", n, err)
	}
	if err := srv.WaitWorker(); err != nil {
		t.Fatal(err)
	}
}
