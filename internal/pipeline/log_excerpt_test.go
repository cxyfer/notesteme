package pipeline

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/logctx"
	"github.com/cxyfer/notesteme/internal/store"
)

// PIPE-R16、NFR-OBS-2：失敗的步驟保存這次執行的 log 片段；片段中的 secret（環境變數、資料庫的 secret、
// 看起來像 secret 的寫法）在寫入資料庫之前就遮蔽。錯誤訊息刻意帶著 secret，確認遮蔽真的發生，不是本來就沒有。
func TestFailedStageKeepsRedactedLogExcerpt(t *testing.T) {
	const envKey = "gsk_env_secret_value_0123"
	const dbKey = "sk-db-secret-value-4567"
	r := newRig(t, func(d *Deps) {
		d.Log = slog.New(logctx.NewHandler(slog.NewJSONHandler(io.Discard, nil)))
		d.Env = map[string]string{"NOTESTEME_GROQ_API_KEY": envKey}
		d.SecretValues = func(context.Context) ([]string, error) { return []string{dbKey}, nil }
	})
	r.yt.probeErr = &errclass.Error{
		Class: errclass.ClassPermanent, Source: "yt-dlp",
		Detail:  "boom with " + envKey + " and " + dbKey + " and api_key=plainsecret99 and Authorization: Bearer abc.def.ghi",
		Message: "找不到這部影片。",
	}
	it := r.ingest("https://www.youtube.com/watch?v=abc123xxxxx", nil, "")
	r.run()

	run := r.stage(it, StageMetadata)
	if run.Status != store.StatusFailed {
		t.Fatalf("metadata = %s", run.Status)
	}
	ex := run.LogExcerpt
	if !strings.Contains(ex, "步驟失敗") || !strings.Contains(ex, "boom with") {
		t.Fatalf("片段應該有失敗的 log：%q", ex)
	}
	for _, secret := range []string{envKey, dbKey, "plainsecret99", "abc.def.ghi"} {
		if strings.Contains(ex, secret) {
			t.Errorf("片段含有 secret %q：%q", secret, ex)
		}
	}
	if strings.Count(ex, excerptMask) < 4 {
		t.Errorf("應該有 4 處遮蔽：%q", ex)
	}
	if strings.Contains(ex, "request_id=") || strings.Contains(ex, "task=") {
		t.Errorf("片段不帶識別欄位：%q", ex)
	}
}

// redactExcerpt 與 logExcerpt 的邊界情況。
func TestRedactExcerpt(t *testing.T) {
	ex := redactExcerpt("a token=abcd1234 b", nil)
	if ex != "a token="+excerptMask+" b" {
		t.Errorf("got %q", ex)
	}
	if got := redactExcerpt("max_tokens=1000 tokens=12", nil); got != "max_tokens=1000 tokens=12" {
		t.Errorf("不是 secret 的欄位不應該被遮蔽：%q", got)
	}
	// 長的 secret 先換：短的是長的一部分時不會留下殘段。
	if got := redactExcerpt("x abcdefgh123 y", []string{"abcdefgh", "abcdefgh123"}); got != "x "+excerptMask+" y" {
		t.Errorf("got %q", got)
	}
	p := &Pipeline{log: slog.New(slog.DiscardHandler)}
	if p.logExcerpt(context.Background(), nil) != "" {
		t.Error("沒有任何行時是空字串")
	}
	p.secretValues = func(context.Context) ([]string, error) { return nil, io.ErrUnexpectedEOF }
	if p.logExcerpt(context.Background(), []string{"x"}) != "" {
		t.Error("讀不到 secret 時不保存片段")
	}
}
