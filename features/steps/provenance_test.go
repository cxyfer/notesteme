package steps

import (
	"strings"
	"testing"
)

// server 已停止時，來源紀錄的斷言要明確失敗（比照 liveStage）：不隱含啟動新的 server，也不能 panic。
func TestArtifactHasProvenanceFailsWhenTheServerIsStopped(t *testing.T) {
	s := newTestScenario(t)
	s.stopped = true
	err := s.artifactHasProvenance("source", "yt:abc123xxxxx", table([]string{"field", "value"}, []string{"source", "supadata"}))
	if err == nil || !strings.Contains(err.Error(), "server 已停止") {
		t.Fatalf("err = %v，預期說明 server 已停止", err)
	}
	if s.server != nil {
		t.Error("不該隱含啟動新的 server")
	}
}
