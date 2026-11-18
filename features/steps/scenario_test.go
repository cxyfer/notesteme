package steps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

// writeToolCalls 把假工具的呼叫紀錄寫進場景根目錄（格式同 faketool 的 calls.jsonl）。
func writeToolCalls(t *testing.T, s *Scenario, calls ...faketool.Call) {
	t.Helper()
	dir := faketool.FakeDir(s.World.Root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, c := range calls {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "calls.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// secret 不只不能出現在 server 的 log，也不能出現在假工具的呼叫參數（NFR-SEC-3：yt-dlp 的命令列會被 ps 看到）。
func TestCheckLogsScansToolCallArguments(t *testing.T) {
	s := newTestScenario(t)
	if err := s.checkLogs(); err != nil {
		t.Fatalf("沒有任何 secret：%v", err)
	}

	// 正常的呼叫參數不誤報。
	writeToolCalls(t, s, faketool.Call{Tool: "yt-dlp", Argv: []string{"yt-dlp", "-J", "--", "https://www.youtube.com/watch?v=abc123xxxxx"}})
	if err := s.checkLogs(); err != nil {
		t.Fatalf("正常的呼叫參數不該誤報：%v", err)
	}

	// secret 出現在呼叫參數中。
	writeToolCalls(t, s, faketool.Call{Tool: "yt-dlp", Argv: []string{"yt-dlp", "--username", "x", "--password", harness.TestGroqKey}})
	err := s.checkLogs()
	if err == nil || !strings.Contains(err.Error(), "呼叫參數") {
		t.Fatalf("err = %v，預期指出呼叫參數含有 secret", err)
	}
	if strings.Contains(err.Error(), harness.TestGroqKey) && !strings.Contains(err.Error(), "yt-dlp") {
		t.Errorf("錯誤應指出是哪個工具：%v", err)
	}

	// server 的 log 仍然會掃。
	s = newTestScenario(t)
	_, _ = s.World.Log.Write([]byte("key=" + harness.TestSupadataKey))
	if err := s.checkLogs(); err == nil || !strings.Contains(err.Error(), "log") {
		t.Errorf("log 含 secret：err = %v", err)
	}
}
