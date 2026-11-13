package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// 來源紀錄取自現行產出；上游以變體名稱回傳；沒有或符合多個時失敗並說明。
func TestArtifactProvenance(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	id := ingest(t, login(t, srv))
	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}

	src, err := srv.ArtifactProvenance(t.Context(), id, "source")
	if err != nil {
		t.Fatal(err)
	}
	var prov struct {
		Source       string            `json:"source"`
		ToolVersions map[string]string `json:"tool_versions"`
	}
	if err := json.Unmarshal(src.Provenance, &prov); err != nil {
		t.Fatalf("provenance 不是 JSON：%v\n%s", err, src.Provenance)
	}
	if prov.Source != "youtube-manual" || prov.ToolVersions["yt-dlp"] != "2026.11.01" {
		t.Errorf("source 的來源紀錄 = %s", src.Provenance)
	}
	if len(src.ParentVariants) != 0 {
		t.Errorf("source 沒有上游，得到 %v", src.ParentVariants)
	}

	norm, err := srv.ArtifactProvenance(t.Context(), id, "normalized")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(norm.ParentVariants, []string{"source"}) {
		t.Errorf("normalized 的上游 = %v，預期 [source]", norm.ParentVariants)
	}

	if _, err := srv.ArtifactProvenance(t.Context(), id, "nope"); err == nil || !strings.Contains(err.Error(), "沒有") {
		t.Errorf("沒有符合的產出應該失敗：%v", err)
	}
	// kind 同時符合 source 與 normalized 兩個現行產出：失敗並列出各組。
	if _, err := srv.ArtifactProvenance(t.Context(), id, "transcript"); err == nil ||
		!strings.Contains(err.Error(), "transcript/source") || !strings.Contains(err.Error(), "transcript/normalized") {
		t.Errorf("transcript 符合多個產出，應該失敗並列出各組：%v", err)
	}
}
