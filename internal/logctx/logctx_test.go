package logctx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func decode(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%v：%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func TestHandlerInjectsFieldsWithoutDuplicates(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))

	ctx := WithRequestID(context.Background(), "req-1")
	ctx, sc := WithTask(ctx, "task-1")
	log.InfoContext(ctx, "before stage known")
	sc.SetItemStage("item-1", "metadata")
	// 呼叫端已經寫了 item：不重複輸出。
	log.WarnContext(ctx, "explicit", "item", "item-1", "attempt", 2)
	// With 累積的欄位也算已有。
	log.With("stage", "metadata").InfoContext(ctx, "with stage")
	log.Info("no context")

	lines := decode(t, &buf)
	if len(lines) != 4 {
		t.Fatalf("lines = %d", len(lines))
	}
	if lines[0]["request_id"] != "req-1" || lines[0]["task"] != "task-1" || lines[0]["item"] != nil {
		t.Errorf("line 0 = %v", lines[0])
	}
	for _, i := range []int{1, 2} {
		m := lines[i]
		if m["request_id"] != "req-1" || m["task"] != "task-1" || m["item"] != "item-1" || m["stage"] != "metadata" {
			t.Errorf("line %d = %v", i, m)
		}
	}
	// JSON 解碼時重複的 key 會被後者蓋掉，要直接看原始文字。
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n")[1:3] {
		for _, k := range []string{`"item":`, `"stage":`} {
			if strings.Count(raw, k) != 1 {
				t.Errorf("%s 重複或缺少：%s", k, raw)
			}
		}
	}
	// 沒有 request ID 的 log（程序層級）帶 boot ID（NFR-OBS-1：每筆都有 request_id）。
	if id, _ := lines[3]["request_id"].(string); id != BootID() || !strings.HasPrefix(id, "boot-") || !ValidID(id) {
		t.Errorf("沒有 context 的 log 應該帶 boot ID %q：%v", BootID(), lines[3])
	}
}

func TestScopeCapturesLastLines(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil))).With("component", "test")
	ctx, sc := WithTask(WithRequestID(context.Background(), "r"), "t")
	sc.SetItemStage("i", "transcript")
	log.DebugContext(ctx, "debug 不會輸出，也不擷取")
	var wg sync.WaitGroup
	for i := range MaxLines + 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.InfoContext(ctx, "line", "n", i)
		}()
	}
	wg.Wait()
	log.ErrorContext(ctx, "最後一行", "error", strings.Repeat("長", 400))

	got := sc.Lines()
	if len(got) != MaxLines {
		t.Fatalf("lines = %d", len(got))
	}
	last := got[len(got)-1]
	if !strings.Contains(last, "最後一行") || !strings.Contains(last, "component=test") || !strings.HasSuffix(last, "…") {
		t.Errorf("last = %q", last)
	}
	if len(last) > MaxLineBytes || !utf8.ValidString(last) {
		t.Errorf("截斷後長度 %d、UTF-8 %v", len(last), utf8.ValidString(last))
	}
	for _, l := range got {
		if strings.Contains(l, "request_id=") || strings.Contains(l, "task=") || strings.Contains(l, "debug") {
			t.Errorf("片段不應有識別欄位或 debug：%q", l)
		}
	}
	if (*Scope)(nil).Lines() != nil {
		t.Error("nil scope")
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"abc-123_X.y":           true,
		"":                      false,
		"has space":             false,
		"line\nbreak":           false,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
		"中文":                    false,
	} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v", id, !want)
		}
	}
	if a, b := NewID(), NewID(); a == b || !ValidID(a) || len(a) != 16 {
		t.Errorf("NewID = %q, %q", a, b)
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("abc", 5) != "abc" {
		t.Error("short")
	}
	got := Truncate("一二三四五", 8) // 每字 3 bytes；… 也是 3 bytes
	if got != "一…" {
		t.Errorf("got %q", got)
	}
}
