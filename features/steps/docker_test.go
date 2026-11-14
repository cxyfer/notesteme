package steps

import (
	"errors"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakecompose"
)

const wslComposeCmd = `docker compose -f deploy/docker/compose.yaml --profile wsl config --format json`

// OPS-R9 的場景：docker 指令由 fakecompose 執行，讀的是真正的 compose.yaml。
func TestDockerComposeConfig(t *testing.T) {
	s := newTestScenario(t)
	s.step = 1
	if err := s.ownerRuns(wslComposeCmd); err != nil {
		t.Fatal(err)
	}
	s.step = 2
	if err := s.commandExits(0); err != nil {
		t.Fatal(err)
	}
	s.step = 3
	doc := &godog.DocString{Content: `{ "services": { "notesteme": { "ports": [ { "host_ip": "127.0.0.1" } ] } } }`}
	if err := s.commandOutputJSON(doc); err != nil {
		t.Fatal(err)
	}

	// 斷言要能失敗：預期綁在 0.0.0.0 時不通過。
	s.step = 4
	bad := &godog.DocString{Content: `{ "services": { "notesteme": { "ports": [ { "host_ip": "0.0.0.0" } ] } } }`}
	if err := s.commandOutputJSON(bad); err == nil {
		t.Error("host_ip 不是 0.0.0.0，斷言應該失敗")
	}

	// 真正的測試 master key 不能出現在 config 的輸出裡。
	if key := s.World.Env["NOTESTEME_MASTER_KEY"]; key == "" || strings.Contains(s.cmd.out, key) {
		t.Errorf("測試用的 master key %q 不該出現在輸出（或 harness 沒有設定它）", key)
	}
}

// 其他 docker 指令（包含 compose 的其他子命令）明確失敗，不是 pending，也不是結束碼。
func TestDockerOtherCommandsFail(t *testing.T) {
	for _, cmd := range []string{
		"docker ps",
		"docker compose -f deploy/docker/compose.yaml up -d",
		"docker compose -f deploy/docker/compose.yaml config",
		"docker run hello-world",
	} {
		s := newTestScenario(t)
		err := s.ownerRuns(cmd)
		if err == nil {
			t.Errorf("%q：應該失敗", cmd)
			continue
		}
		if errors.Is(err, godog.ErrPending) {
			t.Errorf("%q：不支援的 docker 指令不能是 pending：%v", cmd, err)
		}
		if !errors.Is(err, fakecompose.ErrUnsupported) {
			t.Errorf("%q：err = %v，預期包含 ErrUnsupported", cmd, err)
		}
		if s.cmd != nil {
			t.Errorf("%q：不該留下指令結果", cmd)
		}
	}
}

// compose 檔不存在時是 compose 的失敗（結束碼 1 與 stderr），不是 harness 的錯誤。
func TestDockerComposeMissingFileExitsNonZero(t *testing.T) {
	s := newTestScenario(t)
	s.step = 1
	if err := s.ownerRuns("docker compose -f deploy/docker/none.yaml config --format json"); err != nil {
		t.Fatal(err)
	}
	s.step = 2
	if err := s.commandExits(1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.cmd.errOut, "no such file") {
		t.Errorf("stderr = %q", s.cmd.errOut)
	}
}
