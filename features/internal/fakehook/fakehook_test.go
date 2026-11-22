package fakehook

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordsArgvEnvAndDir(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "hooks.jsonl")
	argv := []string{Name, "/vault/a b.md", "$(touch /tmp/pwned)"}
	if code := Main(rec, argv, []string{"LANG=C.UTF-8", "NOTESTEME_ITEM_ID=abc"}, "/data/tmp/t1", &bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	runs, err := Runs(rec)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	r := runs[0]
	if r.Argv[2] != "$(touch /tmp/pwned)" || r.Env["LANG"] != "C.UTF-8" || r.Dir != "/data/tmp/t1" {
		t.Errorf("run = %+v", r)
	}
	if _, ok := r.Env["NOTESTEME_MASTER_KEY"]; ok {
		t.Error("沒有給的變數不應出現")
	}
}

func TestFlags(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "hooks.jsonl")
	if code := Main(rec, []string{Name, "--exit", "3"}, nil, "", &bytes.Buffer{}, nil); code != 3 {
		t.Errorf("--exit 3 = %d", code)
	}
	var out bytes.Buffer
	Main(rec, []string{Name, "--print-bytes", "10000"}, nil, "", &out, nil)
	if !strings.HasSuffix(strings.TrimSpace(out.String()), Marker) || out.Len() != 10000+len(Marker)+1 {
		t.Errorf("--print-bytes 輸出 %d 位元組", out.Len())
	}
	// --hang 會一直等：用會 panic 的 sleep 確認它真的在等待。
	defer func() {
		if recover() == nil {
			t.Error("--hang 應該呼叫 sleep")
		}
	}()
	Main(rec, []string{Name, "--hang"}, nil, "", &bytes.Buffer{}, func(time.Duration) { panic("sleeping") })
}
